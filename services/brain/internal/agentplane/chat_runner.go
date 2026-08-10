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
	providerspkg "github.com/biumind/biumind/services/brain/internal/chat/providers"
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

	// AnthropicAPIKey + AnthropicEndpoint 是 **legacy** 直连 anthropic 兼容
	// 上游的凭证。早期设计 chat 模式绕过 model-relay 直连,不带 user
	// 身份。当 RelayURL 设置时这两条**仅作 absolute fallback**(双保险:
	// RelayURL 空 + UserBearer 空时才用,生产路径不再走这里)。
	//
	// 通过 AGENT_PLANE_ANTHROPIC_API_KEY / _ENDPOINT env 配置;生产建议
	// 留空让 RelayURL+UserBearer 接管。
	AnthropicAPIKey   string
	AnthropicEndpoint string

	// RelayURL 是 model-relay HTTP 端点(`http://model-relay:7001`)。设
	// 置时 chat 模式默认通过它走:用每次请求透传过来的 user JWT
	// (WorkPayload.UserBearer)做 Authorization,model-relay 按 channel
	// 路由 + 用户级配额 + admin 管理的凭证 — 跟 picker 选 BiuMind Cloud
	// 的语义一致。
	RelayURL string

	// DefaultModel 是 client 没传 thread.model(显示"BiuMind 默认")时的
	// fallback。空 → 兜底到 "claude-sonnet-4-6" 保持老行为。运维通过
	// AGENT_PLANE_DEFAULT_CHAT_MODEL 切到当前 model-relay 上 active 的
	// model id,避免 admin 把 claude-sonnet-4-6 设 disabled 后 chat 全废。
	DefaultModel string

	// ProvidersStore 用于按 (userID, providerID slug) 查 chat.providers
	// 行 → 拿 BYOK 元数据 (enabled/source/base_url)。可空(单测/dev) —
	// 空时不查表, 全部走平台 fallback。生产 wired 是 *providerspkg.Store;
	// 接口形态是为了单测能 fake。
	ProvidersStore ProviderResolver
	// KeyResolver 现取用户明文 key + endpoint (P3: 来自 identity, brain
	// 不再存 key)。可空(单测/dev/未配 IDENTITY_URL) — 空时 ResolveBYOKCreds
	// 不走 BYOK。生产 wired 是 *providerspkg.IdentityBYOKClient。
	KeyResolver BYOKKeyResolver

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

// ProviderResolver 是 ChatRunner 对 providers store 的最小依赖,只要能按
// (userID, providerID slug) 返回 *Provider 即可。生产实现是
// *providerspkg.Store(它的 GetByProviderID 已经匹配此签名)。
type ProviderResolver interface {
	GetByProviderID(ctx context.Context, userID uuid.UUID, providerID string) (*providerspkg.Provider, error)
}

// NewChatRunner 构造 ChatRunner。loop 必填(chat.NewAgentLoop(nil, toolReg)
// 即可,HTTPSender 不需要);queue/store 必填用于 publish + finalize。
//
// 凭证选择优先级(resolveCreds 决定):
//  1. BYOK(provider 命中且 source!=official 且 enabled+server fetch+有 key)
//     → 用 provider.APIKey + provider.BaseURL
//  2. RelayURL+UserBearer 都非空 → 透传 user JWT 到 model-relay
//     (生产推荐:per-user 计费 + admin channel 路由 + BYOK 都走得通)
//  3. 否则 → AnthropicAPIKey+AnthropicEndpoint(legacy 直连模式)
//
// defaultModel 空 → 老行为(claude-sonnet-4-6 fallback)。
// providersStore 可空(测试 / dev),空时所有 turn 都走平台 key,不查 BYOK。
// relayURL 空 → 关闭 PassThrough 模式,只剩 BYOK + legacy 两条。
// keyResolver 可空(测试 / dev / 未配 IDENTITY_URL) — P3: BYOK key 从 identity
// 现取; 空时 ResolveBYOKCreds 不走 BYOK (落平台 fallback)。
func NewChatRunner(
	q *Queue,
	store *Store,
	loop *chatpkg.AgentLoop,
	apiKey, endpoint, defaultModel, relayURL string,
	providersStore ProviderResolver,
	keyResolver BYOKKeyResolver,
	logger *slog.Logger,
) *ChatRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChatRunner{
		Queue:             q,
		Store:             store,
		Loop:              loop,
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: endpoint,
		RelayURL:          relayURL,
		DefaultModel:      defaultModel,
		ProvidersStore:    providersStore,
		KeyResolver:       keyResolver,
		Logger:            logger,
		inflight:          map[uuid.UUID]context.CancelCauseFunc{},
		cancelStartedAt:   map[uuid.UUID]time.Time{},
	}
}

// resolveCreds 按 payload 决定本次 turn 用哪对 (apiKey, endpoint, useBearer)。
// 优先级见 NewChatRunner 文档:BYOK > model-relay PassThrough > legacy direct。
//
// useBearer 决定 biumindkit 用哪种 HTTP auth header:
//   - true  → `Authorization: Bearer <apiKey>` (model-relay PassThrough)
//   - false → `x-api-key: <apiKey>`            (Anthropic 原生 / BYOK 直连)
//
// 这条信号必须从 resolveCreds 出来 — 上层 SingleTurnInput.UseRelayAuth 透传
// 给 biumindkit 决定 header 形态;不然 PassThrough 用 x-api-key 写到 header
// 上,model-relay authMiddleware 看不到 Bearer 直接 401 "missing bearer"。
func (cr *ChatRunner) resolveCreds(
	ctx context.Context,
	userID uuid.UUID,
	providerID, userBearer string,
) (apiKey, endpoint string, useBearer bool) {
	// 1. BYOK: provider 命中且配置完整。BYOK key 是 Anthropic 直连风格
	//    (sk-ant-...),用 x-api-key header 跟原生 Anthropic API 直连。
	r := ResolveBYOKCreds(ctx, cr.ProvidersStore, cr.KeyResolver, userID, providerID, cr.Logger)
	if r.UseBYOK {
		apiKey = r.APIKey
		endpoint = r.BaseURL
		if endpoint == "" {
			endpoint = cr.RelayURL
			if endpoint == "" {
				endpoint = cr.AnthropicEndpoint
			}
		}
		return apiKey, endpoint, false // BYOK = anthropic 原生 x-api-key
	}
	// 2. PassThrough: 透传 user JWT 到 model-relay。生产路径,bearer auth。
	if cr.RelayURL != "" && userBearer != "" {
		return userBearer, cr.RelayURL, true
	}
	// 3. Legacy direct: 老路径,绕过 model-relay 直连内网网关 / api.anthropic.com。
	//    保留老语义 (x-api-key) — 老 deployments 里 your-llm-gateway.example.com
	//    那种 LiteLLM gateway 通常 x-api-key + Bearer 都能 — 不主动改 header。
	if cr.RelayURL != "" && userBearer == "" {
		cr.Logger.Warn("chat runner: empty user bearer with RelayURL set; "+
			"falling back to legacy direct creds",
			"user_id", userID, "provider_id", providerID)
	}
	return cr.AnthropicAPIKey, cr.AnthropicEndpoint, false
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
	// 注意：凭证判空不在这里做。AnthropicAPIKey 只是 legacy 直连 fallback，
	// BYOK / model-relay PassThrough 都不需要它 —— 提前判空会把生产推荐的
	// PassThrough 路径误杀（实测：新部署未配 AGENT_PLANE_ANTHROPIC_API_KEY
	// 时 session 创建后毫秒级 finalize failed，客户端 WS 全部 409）。
	// 判空在 resolveCreds 之后（见下）。

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

	// 按 payload.ProviderID + UserBearer 解析 (apiKey, endpoint, useBearer):
	// BYOK > model-relay PassThrough > legacy direct。详见 resolveCreds。
	apiKey, endpoint, useBearer := cr.resolveCreds(
		ctx, payload.UserID, payload.ProviderID, payload.UserBearer,
	)
	if apiKey == "" {
		cr.publishErrorAndFinalize(ctx, sess, "missing_api_key",
			"no LLM credentials resolved (BYOK miss, model-relay passthrough "+
				"unavailable, AGENT_PLANE_ANTHROPIC_API_KEY unset)")
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

	input := chatpkg.SingleTurnInput{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: endpoint,
		UseRelayAuth:      useBearer,
		Model:             firstNonEmptyChatStr(payload.Model, cr.DefaultModel, "claude-sonnet-4-6"),
		System:            payload.SystemPrompt,
		Prompt:            payload.Prompt,
		History:           history,
		Images:            images,
		Emitter:           emitter,
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
