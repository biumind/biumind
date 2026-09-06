// Chat-mode in-process runner（S4-5 / S4-7 plumbing）。
//
// 职责仅限**路由 + NATS 帧发射**：把 chat session 的执行委托给
// `chat.AgentLoop.RunV2` —— chat 模式的"内核"（biumindkit 驱动）只在
// chat 包里有一份，agentplane 只是把 NATS frame publisher 接进 RunV2
// 的 EventEmitter 接口。
//
// 架构：
//
//	router.createChatSession  →  ChatRunner.RunSession
//	                                ↓
//	chat.NewFrameEmitter(sid, queue.PublishSessionFrame)
//	                                ↓
//	chat.AgentLoop.RunV2(input{ Emitter: frameEmitter, … })
//	                                ↓ (内部跑 biumindkit.Submit)
//	frameEmitter.TextDelta / ToolStarted / …
//	                                ↓
//	queue.PublishSessionFrame(biu.session.<sid>.out)  →  ingress  →  WS client
//
// 这样 chat 模式的 LLM 驱动 + tool catalog + history 转换全在 chat 包里
// 由 RunV2 owns；agentplane 只决定"调用 chat.RunV2 还是 enqueue work
// queue（agent / task mode）"。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// ChatRunner orchestrates chat-mode session execution. Brain creates one
// at boot (cmd/brain/main.go) and stashes it on Server.ChatRunner; the
// router invokes RunSession when a chat session is created.
type ChatRunner struct {
	Queue  *Queue
	Store  *Store
	Loop   *chatpkg.AgentLoop // chat.AgentLoop with tools.Registry wired
	Logger *slog.Logger

	// RelayURL 是 model-relay HTTP 端点(`http://model-relay:7001`)。chat
	// 去 env 化后这是**唯一**凭证路径:用每次请求透传过来的 user JWT
	// (WorkPayload.UserBearer)做 Authorization,model-relay 按 channel
	// 路由 + 用户级配额 + admin 管理的凭证;BYOK 由 relay 侧按 identity
	// 的 protocol 选 adaptor 统一接住(BYOK-Unification INV-5)。空 →
	// chat session 创建后立即 finalize failed (missing_bearer)。
	RelayURL string

	// DefaultModel 是 AGENT_PLANE_DEFAULT_CHAT_MODEL 的 env 覆盖值,在
	// 默认模型兜底链里排第二(见 defaultChatModel)。空 → 落到硬兜底
	// "claude-sonnet-4-6"。
	DefaultModel string

	// DefaultModels 从 relay 解析 admin 配置的默认 chat model
	// (is_default_chat → GET /v1/internal/models/default-chat),进程内
	// 缓存。可空(单测/dev) — 空时兜底链跳过 relay 这一级。
	DefaultModels *DefaultModelResolver

	// Elicitations 是 chat 模式提问表单（AskUserQuestion → elicitation
	// 控制帧）的进程内 pending map。非 nil 时 runSessionImpl 把
	// askUserFn 接进 chat.SingleTurnInput.AskUser —— 模型可见
	// AskUserQuestion 且有人应答；nil = 工具不进 catalog（无应答链路，
	// 防 Decision channel 死锁,agent-ask-form P0）。main.go 注入,同时
	// 也灌给 Ingress 做回包分流。
	Elicitations *ElicitationCenter

	// inflight 跟踪在跑的 session — sessionID → cancelCauseFunc。
	// InterruptSession() 据此找到 cancel func 并 fire biumindkit.ErrInterrupted,
	// 让 biumindkit / engine 走 clean-stop 路径(Done{interrupted} + 合成
	// tool_result)。runSessionImpl 进出时分别注册 / 释放。
	//
	// cancelStartedAt 配合 inflight 用 —— InterruptSession 命中时打时间
	// 戳,untrackInflight 退出时算延迟 observe 进 metrics.RecordCancelLatency。
	// 同 mu 守护以防 race。
	inflightMu      sync.Mutex
	inflight        map[uuid.UUID]context.CancelCauseFunc
	cancelStartedAt map[uuid.UUID]time.Time
}

// NewChatRunner 构造 ChatRunner。loop 必填(chat.NewAgentLoop(nil, toolReg)
// 即可,HTTPSender 不需要);queue/store 必填用于 publish + finalize。
//
// 凭证模型(chat 去 env 化):chat 模式一律走 model-relay PassThrough ——
// 透传 user JWT (WorkPayload.UserBearer) 做 Authorization,BYOK 由 relay
// 侧统一接住。RelayURL 空或 UserBearer 空 → session finalize failed
// (missing_bearer),见 runSessionImpl。
//
// 默认模型兜底链(defaultChatModel):relay default-chat (DefaultModels)
// > defaultModel (AGENT_PLANE_DEFAULT_CHAT_MODEL env 覆盖) >
// claude-sonnet-4-6 硬兜底。defaultModels 可空(单测 / dev)。
func NewChatRunner(
	q *Queue,
	store *Store,
	loop *chatpkg.AgentLoop,
	defaultModel, relayURL string,
	defaultModels *DefaultModelResolver,
	logger *slog.Logger,
) *ChatRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChatRunner{
		Queue:           q,
		Store:           store,
		Loop:            loop,
		RelayURL:        relayURL,
		DefaultModel:    defaultModel,
		DefaultModels:   defaultModels,
		Logger:          logger,
		inflight:        map[uuid.UUID]context.CancelCauseFunc{},
		cancelStartedAt: map[uuid.UUID]time.Time{},
	}
}

// resolveCreds 决定本次 turn 的 (apiKey, endpoint, useBearer)。chat 去
// env 化后只剩一条路:model-relay PassThrough —— user JWT 当 Bearer 透传,
// RelayURL 当 endpoint。useBearer 恒 true,让 biumindkit 用
// `Authorization: Bearer` header(而不是 x-api-key),relay 的
// authMiddleware 才认;BYOK 由 relay 侧按 identity protocol 统一接住。
//
// ok=false(RelayURL 空 / userBearer 空)时调用方 finalize failed
// (missing_bearer),不发 LLM 请求。
func (cr *ChatRunner) resolveCreds(userBearer string) (apiKey, endpoint string, useBearer, ok bool) {
	if cr.RelayURL == "" || userBearer == "" {
		return "", "", false, false
	}
	return userBearer, cr.RelayURL, true, true
}

// defaultChatModel 解析 client 没传 thread.model(显示 "BiuMind 默认")
// 时的 fallback model。兜底链:relay default-chat (admin 在 models 表
// 标 is_default_chat) > DefaultModel (env 覆盖) > claude-sonnet-4-6。
// relay 不可达 / 未配时 DefaultModels 返 "",自然落到下一级。
func (cr *ChatRunner) defaultChatModel(ctx context.Context) string {
	if cr.DefaultModels != nil {
		if m := cr.DefaultModels.DefaultChatModel(ctx); m != "" {
			return m
		}
	}
	return firstNonEmptyChatStr(cr.DefaultModel, "claude-sonnet-4-6")
}

// InterruptSession 触发某 sessionID 的 chat-mode session 立即停。
// 返回 true 表示找到了在跑的 session 并触发了 cancel(异步,engine 在
// 下一个 yield 点 emit Done{interrupted}),false 表示该 session 不在
// 本进程的 chat 模式跑(可能是 agent / task 模式 -> 走 environment 控
// 制队列分支)。
//
// 给 ingress.SetChatInterrupt 用 —— main.go 注入闭包到 ingress。幂等
// (重复调用 / session 已结束都不 panic)。
//
// 命中时打个 timestamp 进 cancelStartedAt;runSessionImpl 通过 defer 在
// 退出时计算延迟,observe 进 brain_agent_cancel_latency_seconds 直方图。
// 这是按 stop 到真正 idle 的端到端 SLO,应该 <1s。
func (cr *ChatRunner) InterruptSession(sessionID uuid.UUID) bool {
	cr.inflightMu.Lock()
	cancel, ok := cr.inflight[sessionID]
	if ok {
		// 多次按 stop 只记第一次时间戳 — 从用户视角第一次按下就是
		// "开始等"。第二次第三次不重置,让 P95 反映真情况。
		if _, already := cr.cancelStartedAt[sessionID]; !already {
			cr.cancelStartedAt[sessionID] = time.Now()
		}
	}
	cr.inflightMu.Unlock()
	if !ok {
		return false
	}
	cancel(biumindkit.ErrInterrupted)
	return true
}

func (cr *ChatRunner) trackInflight(sessionID uuid.UUID, cancel context.CancelCauseFunc) {
	cr.inflightMu.Lock()
	cr.inflight[sessionID] = cancel
	cr.inflightMu.Unlock()
}

// untrackInflight 释放 sessionID 的 inflight 索引,如果之前 InterruptSession
// 标记过开始 timestamp,这里 observe 端到端延迟。返回值是 true 时表
// 示这次确实被 cancel 过 — 调用方据此决定要不要打成功 metric(可选)。
func (cr *ChatRunner) untrackInflight(sessionID uuid.UUID) bool {
	cr.inflightMu.Lock()
	startedAt, wasCancelled := cr.cancelStartedAt[sessionID]
	delete(cr.inflight, sessionID)
	delete(cr.cancelStartedAt, sessionID)
	cr.inflightMu.Unlock()
	if wasCancelled {
		metrics.RecordCancelLatency("chat", time.Since(startedAt))
	}
	return wasCancelled
}

// RunSession 在新 goroutine 里跑一条 chat session。同步返回让 router 立即
// 回 session_token，goroutine 异步驱动 LLM + 推帧。
func (cr *ChatRunner) RunSession(ctx context.Context, sess *Session, payload WorkPayload) {
	go cr.runSessionImpl(ctx, sess, payload)
}

func (cr *ChatRunner) runSessionImpl(ctx context.Context, sess *Session, payload WorkPayload) {
	sessionID := sess.SessionID

	// Wrap with cancel-cause so InterruptSession() can fire ErrInterrupted
	// and let biumindkit emit Done{interrupted} instead of an error.
	subCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	cr.trackInflight(sessionID, cancel)
	defer cr.untrackInflight(sessionID)
	ctx = subCtx

	cr.Logger.Debug("chat runner: enter",
		"session_id", sessionID, "user_id", payload.UserID,
		"thread_id", payload.ThreadID, "model", payload.Model,
		"provider_id", payload.ProviderID,
		"prompt_bytes", len(payload.Prompt),
		"system_bytes", len(payload.SystemPrompt),
		"images", len(payload.Images))

	if cr.Queue == nil {
		cr.Logger.Error("chat runner: Queue nil; cannot publish frames", "session_id", sessionID)
		return
	}
	if cr.Loop == nil {
		cr.publishErrorAndFinalize(ctx, sess, "loop_nil",
			"brain ChatRunner has no chat.AgentLoop wired (chat mode unavailable)")
		return
	}
	// 注意：凭证判空不在这里做。RelayURL / UserBearer 的判空在
	// resolveCreds 里(见下) —— 提前判空曾把生产 PassThrough 路径误杀
	// (Phase A ea4cd46 修复的 missing_api_key 事故)。

	// 给客户端打开 WS 留窗口期 —— createChatSession 写完 row 立刻 spawn
	// 这个 goroutine，但 HTTP response 还没回到 client、client 还没 dial
	// WS。Ingress 的 ephemeral consumer 是 DeliverNew，太早 publish 客户
	// 端会漏掉。1s 在 LAN/loopback 足够；正解（subscribe `.in` 收
	// user_message 触发模式）留 v2。
	select {
	case <-time.After(1 * time.Second):
	case <-ctx.Done():
		return
	}

	// FrameEmitter 是 chat WS 路径的下游 —— RunV2 把 biumindkit 事件投递
	// 给 EventEmitter，FrameEmitter 翻成 SDK Protocol 帧 publish 到 NATS。
	emitter := chatpkg.NewFrameEmitter(sessionID.String(), func(payload []byte) error {
		if cr.Logger.Enabled(ctx, slog.LevelDebug) {
			cr.Logger.Debug("chat runner: publish frame",
				"session_id", sessionID, "bytes", len(payload))
		}
		return cr.Queue.PublishSessionFrame(ctx, sessionID, payload)
	})
	emitter.OnError = func(err error) {
		cr.Logger.Warn("chat runner: emitter publish failed",
			"session_id", sessionID, "err", err)
	}

	// 凭证解析(chat 去 env 化):一律 model-relay PassThrough —— 透传
	// user JWT 做 Bearer,BYOK 由 relay 侧接住。RelayURL 空或 bearer 空
	// → 立即 finalize failed (missing_bearer),客户端 WS 收 error 帧。
	apiKey, endpoint, useBearer, ok := cr.resolveCreds(payload.UserBearer)
	if !ok {
		cr.publishErrorAndFinalize(ctx, sess, "missing_bearer",
			"chat mode requires model-relay PassThrough "+
				"(MODEL_RELAY_URL unset or user bearer missing)")
		return
	}

	// 调 chat.AgentLoop.RunSingleTurn —— 真正的 chat 内核（biumindkit
	// 驱动 + tool catalog + history 转换）在 chat 包里。这里只组 input。
	// UseRelayAuth=useBearer 让 biumindkit 用 Bearer header (PassThrough)
	// 而不是 x-api-key — 不设的话 model-relay authMiddleware 看不到 Bearer
	// 直接 401 "missing bearer"。
	// 把 wire 上的 ChatImageInput 转成 chat 包的 ImageInput。两层结构相同
	// 但 chat 包跟 agentplane 解耦,不直接 import 对方,所以这里手转一下。
	var images []chatpkg.ImageInput
	if len(payload.Images) > 0 {
		images = make([]chatpkg.ImageInput, 0, len(payload.Images))
		for _, img := range payload.Images {
			images = append(images, chatpkg.ImageInput{
				MimeType: img.MimeType,
				Data:     img.Data,
			})
		}
	}

	// 把 payload.History 转成 chat 包的 PriorTurn。空 → 单轮（向后兼容）。
	// §8.2 翻案后 payload.History 由 **brain 服务端**从 chat.messages 组装
	// (router.persistUserAndAssemble),不再来自客户端。
	var history []chatpkg.PriorTurn
	if len(payload.History) > 0 {
		history = make([]chatpkg.PriorTurn, 0, len(payload.History))
		for _, t := range payload.History {
			history = append(history, chatpkg.PriorTurn{Role: t.Role, Content: t.Content})
		}
	}

	// Model 兜底链:client 传的 thread.model > relay default-chat >
	// AGENT_PLANE_DEFAULT_CHAT_MODEL env > claude-sonnet-4-6。
	model := payload.Model
	if model == "" {
		model = cr.defaultChatModel(ctx)
	}

	input := chatpkg.SingleTurnInput{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: endpoint,
		UseRelayAuth:      useBearer,
		Model:             model,
		System:            payload.SystemPrompt,
		Prompt:            payload.Prompt,
		History:           history,
		Images:            images,
		Emitter:           emitter,
	}
	// 提问表单（agent-ask-form P1-b）：注入了 ElicitationCenter 才把
	// AskUser 接进引擎 —— 模型可见 AskUserQuestion 且提问经 elicitation
	// 控制帧有人应答。未注入 = 工具不进 catalog（P0 防死锁）。
	if cr.Elicitations != nil {
		input.AskUser = cr.askUserFn(sessionID)
	}
	llmStart := time.Now()
	result, err := cr.Loop.RunSingleTurn(ctx, input)
	if err != nil {
		cr.Logger.Debug("chat runner: RunSingleTurn failed",
			"session_id", sessionID, "model", input.Model,
			"latency_ms", time.Since(llmStart).Milliseconds(),
			"err", err)
		cr.publishErrorFrame(ctx, sess, err)
		cr.finalizeFailed(ctx, sess, err.Error())
		return
	}
	cr.Logger.Debug("chat runner: RunSingleTurn ok",
		"session_id", sessionID, "model", input.Model,
		"latency_ms", time.Since(llmStart).Milliseconds(),
		"stop_reason", result.StopReason,
		"prompt_tokens", result.PromptTokens,
		"completion_tokens", result.CompletionTokens)
	// 推一帧 result_success 让客户端知道收尾（RunV2 内部 biumindkit Done
	// 已经 emit 了，但 RunV2 当前不把它投到 EventEmitter；为了 wire 完整，
	// 这里手动补一帧）
	cr.publishDoneFrame(ctx, sess, result)

	if err := FinalizeSessionResult(ctx, cr.Store, sess, FinalizeOpts{
		Status: "completed",
	}); err != nil {
		cr.Logger.Warn("chat runner: finalize failed", "session_id", sessionID, "err", err)
	}
	cr.Logger.Info("chat runner: session completed",
		"session_id", sessionID, "stop_reason", result.StopReason)
}

// publishDoneFrame 推一帧 SDKResultSuccess —— 客户端 ingress 收到这一帧
// 知道 LLM turn 完整结束。
func (cr *ChatRunner) publishDoneFrame(ctx context.Context, sess *Session, result *chatpkg.AgentRunResult) {
	if cr.Queue == nil {
		return
	}
	frame := sdkbridge.ToSDKFrame(biumindkit.Done{
		StopReason:   result.StopReason,
		InputTokens:  result.PromptTokens,
		OutputTokens: result.CompletionTokens,
	}, sess.SessionID.String())
	raw, err := json.Marshal(frame)
	if err != nil {
		cr.Logger.Warn("chat runner: marshal done frame failed", "err", err)
		return
	}
	if err := cr.Queue.PublishSessionFrame(ctx, sess.SessionID, raw); err != nil {
		cr.Logger.Warn("chat runner: publish done frame failed",
			"session_id", sess.SessionID, "err", err)
	}
}

// publishErrorFrame 推一帧 SDKResultError 让客户端知道 LLM turn 失败了。
func (cr *ChatRunner) publishErrorFrame(ctx context.Context, sess *Session, runErr error) {
	if cr.Queue == nil {
		return
	}
	frame := sdkbridge.ToSDKFrame(biumindkit.Error{
		Err:         runErr,
		Recoverable: false,
	}, sess.SessionID.String())
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	_ = cr.Queue.PublishSessionFrame(ctx, sess.SessionID, raw)
}

// publishErrorAndFinalize 推一条 SDKResultError 帧 + 写 results 表 +
// state=failed —— 客户端收到帧后 ingress 自然把 WS 关掉。
// 必须打 WARN 日志：chat 模式的 ErrorMessage 不落 agent_session_results
// （FinalizeSessionResult 只 update state），日志是唯一的排障线索。
func (cr *ChatRunner) publishErrorAndFinalize(ctx context.Context, sess *Session, code, msg string) {
	cr.Logger.Warn("chat runner: session finalize-failed",
		"session_id", sess.SessionID, "code", code, "reason", msg)
	if cr.Queue != nil {
		errFrame := sdkbridge.ToSDKFrame(biumindkit.Error{
			Err:         fmt.Errorf("%s: %s", code, msg),
			Recoverable: false,
		}, sess.SessionID.String())
		raw, _ := json.Marshal(errFrame)
		_ = cr.Queue.PublishSessionFrame(ctx, sess.SessionID, raw)
	}
	cr.finalizeFailed(ctx, sess, msg)
}

func (cr *ChatRunner) finalizeFailed(ctx context.Context, sess *Session, msg string) {
	if cr.Store == nil {
		return
	}
	if err := FinalizeSessionResult(ctx, cr.Store, sess, FinalizeOpts{
		Status:       "failed",
		ErrorMessage: msg,
	}); err != nil {
		cr.Logger.Warn("chat runner: finalize-failed write failed",
			"session_id", sess.SessionID, "err", err)
	}
}

// firstNonEmptyChatStr 简化 model fallback 取值。
func firstNonEmptyChatStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
