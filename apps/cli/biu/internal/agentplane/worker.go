// biu daemon worker poller（S3-8 主循环）。
//
// 流程：
//
//	register → heartbeat goroutine → poll loop → execute work → publish frames →
//	loop next
//
// 单进程内：
//
//	┌─ heartbeat goroutine (30s tick) ────────────────┐
//	│                                                  │
//	▼                                                  ▼
//	main loop:  PollWork() ── work ──▶ runWork() ──▶ ack/nak
//	            └─ no work ──▶ continue
//
// 退出：ctx cancel → heartbeat 跟 main loop 都退出 → Deregister 释放
// environment（可选；让 brain janitor 90s 后自动标 offline 也行）。
//
// engine 实现：用 biu CLI 的 biumindkit.Agent。每条 work 一个 fresh agent，
// 跑完 close。复杂度（PriorMessages / PermissionPolicy 等）由 AgentBuilder
// 闭包自定义。
//
// **错误策略**：
//   - 网络错（连不上 brain）→ 指数退避重试，不退出 daemon
//   - 4xx（鉴权 / not found）→ log 后继续轮询；调用方自行修配置
//   - work 执行错 → Nak（broker 60s 后 redeliver；MaxDeliver=5 后丢入 max-deliver pile）

package agentplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	agentpkg "github.com/biumind/biumind/packages/go-sdk/biu/agent"
	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/google/uuid"
)

// AgentBuilder 给定一条 work payload + worker 提供的 askPermission /
// askUser 闭包, 返回一个准备好的 *biumindkit.Agent。daemon 启动时由 wiring
// 层注入 —— 让 worker 包不直接依赖 anthropic 配置 / MCP 注册等具体细节。
//
// askPermission 走 brain control queue 反向问 client(见 askPermissionFor)。
// builder 应该把它传给 biumindkit.Options.PermissionPolicy 让 engine 在触
// 发 PermissionAsk 时调用。如果 builder 出于策略原因(单测 / fully-allow
// 模式)不想把它接入,可以忽略;deny 兜底仍然安全。
//
// askUser（agent-ask-form P2-b）同款链路：elicitation 控制帧 → client
// FormCard 作答 → control queue 投回。builder 把它传给
// biumindkit.Options.AskUser 让 AskUserQuestion 工具进目录；忽略（传 nil）
// 则工具隐藏（P0 安全默认）。
type AgentBuilder func(ctx context.Context, work WorkPayload, askPermission biumindkit.PermissionPolicyFn, askUser biumindkit.AskUserFn) (*biumindkit.Agent, error)

// WorkPayload 跟 brain 端 router.go::WorkPayload 字段同步。S3-6 brain 投递；
// S3-8 worker 反序列化。
type WorkPayload struct {
	SessionID uuid.UUID `json:"session_id"`
	UserID    uuid.UUID `json:"user_id"`
	Mode      string    `json:"mode"`
	Prompt    string    `json:"prompt,omitempty"`
	Model     string    `json:"model,omitempty"`
	// ProviderID 锁定走哪个 chat.providers.provider_id slug。
	ProviderID   string `json:"provider_id,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	// Workdir 由 brain 透传:agent / task 模式下让 daemon 在指定目录跑工具。
	// daemon 在 buildAgent 时把它写到 biumindkit Options.Cwd +
	// PermissionUpdate.AddDirectories(session destination)。空 → 使用 daemon
	// 启动时的 cwd。安全门:--allowed-roots flag 限制 brain 能让 daemon 访问
	// 的根路径(避免 brain 让 daemon 跑 ~/.ssh)。
	Workdir string `json:"workdir,omitempty"`
	// RuntimeEnvMode（Runtime v3 轴 B）：'none' | 'local' | 'cloud'。daemon
	// 在 buildAgent 时翻成 biumindkit ExecHost：local（默认）本机执行；cloud
	// 经 ToolExecHost 分流到 services/sandbox（R5，当前 stub）；none 拒绝。
	// 空 → exechost.For 兜底 local。
	RuntimeEnvMode string `json:"runtime_env_mode,omitempty"`
	// Backend（Runtime v3 R3/Q3）：'biumindkit'(默认/空) | 'claude-cli' |
	// 'codex-cli'。非空且 IsCliBackend → daemon 走外部 CLI Runner 路径
	// （CLI 自执行工具,D1）。
	Backend string `json:"backend,omitempty"`
	// UserBearer（P4）：brain 从 create-session 请求抽出的用户 JWT,投 NATS 给
	// daemon。daemon 用它作 model-relay 的 Authorization → relay 拿 claims.UserID
	// 原生解析该 user 的 BYOK(BYOK.Match),与 chat 路径同构。空(离线重派 / 未带
	// JWT) → daemon 回退 BIUMIND_TOKEN/PAT 走平台池,BYOK 不生效。
	UserBearer string `json:"user_bearer,omitempty"`
	// ToolPolicy（R6.3 / D7）：brain 按目标 device 的 agent_devices.tool_policy
	// stamp 的 preset（readonly|workspace-write|full）。daemon 取它与本地
	// --tool-policy flag 的**交集**（更严者）作有效能力地板。空 → daemon flag 说
	// 了算（task 模式无 device / 老 brain）。非密，明文（不进 sealBYOK 信封）。
	ToolPolicy string `json:"tool_policy,omitempty"`
	// History 是 brain 服务端从 chat.messages 组装的 prior 多轮(不含当前 Prompt,
	// 按时间升序 user/assistant 交替)。daemon 转成 biumindkit PriorMessages 注入,
	// 让每个无状态 work 的 agent 看到对话上下文(否则永远单轮)。空 → 单轮。
	// Runtime v3 §8.2 翻案:brain 作真相源,历史不再由客户端带入。
	History []ChatTurn `json:"history,omitempty"`
	// ClientSideRecordID/BaseURL/Protocol（B2）：brain 透传 client-side BYOK 信号。
	// daemon 据此从 loopback 内存 store（/internal/client-credential 推入）按
	// record_id 取 key + base_url/protocol 建 engine 直连上游，跳 relay。
	// 空 → daemon 走 relay（withBearer / PAT 平台池）。
	ClientSideRecordID string `json:"client_side_record_id,omitempty"`
	ClientSideBaseURL  string `json:"client_side_base_url,omitempty"`
	ClientSideProtocol string `json:"client_side_protocol,omitempty"`
}

// ChatTurn 是一轮历史消息(与 brain agentplane.ChatTurn 字段同步)。
// Role: "user" | "assistant"。
type ChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// WorkerConfig 集中配置 daemon 行为。
type WorkerConfig struct {
	EnvironmentName string   // machine_name；默认 hostname
	Capabilities    []string // 注册时上报；让 brain 知道这台 daemon 支持啥
	PoolTag         string   // 通常空（runtime worker 才填）
	WorkerKind      string   // 默认 'biu_daemon'
	HeartbeatPeriod time.Duration
	PollWait        time.Duration
	BackoffInitial  time.Duration
	BackoffMax      time.Duration

	// RunnerBuilder（Runtime v3 R3/Q3）：当 WorkPayload.Backend 指向外部 CLI
	// backend（claude-cli 等）时返回一个 agent.Runner；返回 nil → 走内建
	// biumindkit 路径。nil builder（未注入）→ 永远走内建路径。
	RunnerBuilder func(WorkPayload) agentpkg.Runner

	// PublicKey / Privkey：X25519 密钥对（raw 32B）。P4 前用于 BYOK 信封加密
	// (sealBYOK/EncBYOK)，该链已删。现保留给 S3-4 mcp_config / work_secret 信封
	// 加密复用 —— PublicKey 注册时 hex 上报 brain，Privkey 留本机解密未来投递的
	// 密文。两者同时为空 → 不上报 pubkey。
	PublicKey []byte
	Privkey   []byte

	// ResolveWorkdir（R6.3 路径地板）：校验 + 默认化 work.Workdir。空 →
	// 返回默认根；越界 → 返回 error（该 work 被拒）。biumindkit 路径在 builder
	// 闭包里已做同样校验；外部 CLI backend 路径（runExternalBackend）必须也过
	// 这道门——否则手机/web 端可投 backend=claude-cli + 任意 workdir 让 daemon
	// 在 ~/.ssh 等越界目录起 CLI，架空 --allowed-roots 路径地板。nil（未注入 /
	// 单测）→ 不校验，保持透传（生产由 agent_cmd 注入）。
	ResolveWorkdir func(string) (string, error)

	// OnRegistered（B2）：register / re-register 成功后回调，传最新 env_id。
	// serve stdout 打印 BIU_DAEMON_ENV_ID=<uuid> → BiuDaemonManager (Flutter)
	// 解析持有本机 daemon 的 env_id —— client-side BYOK 命中时用它作
	// environment_id，保证 loopback 推的 key 与 brain 投 work 的 env_id 同机
	// （work 按 env_id 定向投 NATS，选错则远端 daemon 收到却无 key）。nil → 不通知。
	OnRegistered func(envID string)
}

func (c *WorkerConfig) defaults() {
	if c.WorkerKind == "" {
		c.WorkerKind = "biu_daemon"
	}
	if c.HeartbeatPeriod <= 0 {
		// R8：15s（配 brain JanitorHeartbeatTTL=45s,3× 容忍网络抖动）。
		// 此前 30s/90s 让 daemon 崩溃后最坏 ~120s 才被判离线;收紧到 ~60s,
		// 客户端更快收到失败帧停止 spinner。
		c.HeartbeatPeriod = 15 * time.Second
	}
	if c.PollWait <= 0 || c.PollWait > 30*time.Second {
		c.PollWait = 30 * time.Second
	}
	if c.BackoffInitial <= 0 {
		c.BackoffInitial = 1 * time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
}

// Worker 是 daemon 主体。Run(ctx) 阻塞直到 ctx 取消。
type Worker struct {
	client     *Client
	cfg        WorkerConfig
	buildAgent AgentBuilder
	logger     *slog.Logger

	mu         sync.Mutex
	envID      uuid.UUID // 注册成功后填
	registered bool

	// agents 跟踪当前在飞的 Agent，按 sessionID 索引。InterruptSession()
	// 据此查找对应 *biumindkit.Agent 调用 Interrupt() —— 让 brain ingress
	// 收到 client 的 control_cancel_request 后能直接路由到本地 daemon 上
	// 真正在跑的 agent。handleWork 进出时分别 register / unregister。
	//
	// cancelStartedAt 配合 agents:InterruptSession 命中时打 timestamp,
	// handleWork 在看到 biumindkit.Done{StopReason:"interrupted"} 事件时
	// observe 延迟到 metrics.RecordCancelLatency。同 agentsMu 守护以防 race。
	agentsMu        sync.Mutex
	agents          map[uuid.UUID]*biumindkit.Agent
	cancelStartedAt map[uuid.UUID]time.Time

	// pendingPermsMu 保护 pendingPerms。独立于 agentsMu —— PermissionPolicy
	// 的等待 goroutine 可能跟 InterruptSession 并发,分开锁避免死锁。
	//
	// key = request_id (UUID 全局唯一,跨 session 不冲突);
	// value = 等待 brain control queue 投回 permission_response 的 chan。
	// chan buffer=1 让 answerPermission 永远不阻塞;关 chan 表示已应答。
	pendingPermsMu sync.Mutex
	pendingPerms   map[string]chan biumindkit.PermissionDecision

	// pendingAsks 是提问表单（agent-ask-form P2-b）的 pending map，与
	// pendingPerms 同构。entry 带 sessionID：session 结束 / 中断时
	// cancelPendingAsks 按会话批量投 cancel 释放等候 goroutine
	// （防泄漏；见 elicitation.go）。锁独立同理。
	pendingAsksMu sync.Mutex
	pendingAsks   map[string]pendingAskEntry
}

// permissionTimeout 是单条 can_use_tool 等待 client 应答的最大时长。超过
// 自动 deny —— 跟 bridge mode 一致(30s)。var 而非 const 让单测能缩到几
// 百毫秒。
var permissionTimeout = 30 * time.Second

func NewWorker(client *Client, cfg WorkerConfig, build AgentBuilder, logger *slog.Logger) *Worker {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		client:          client,
		cfg:             cfg,
		buildAgent:      build,
		logger:          logger,
		agents:          map[uuid.UUID]*biumindkit.Agent{},
		cancelStartedAt: map[uuid.UUID]time.Time{},
		pendingPerms:    map[string]chan biumindkit.PermissionDecision{},
		pendingAsks:     map[string]pendingAskEntry{},
	}
}

// SetToken 热更新 brain PAT, 转发给 client。bridge HTTP POST /internal/token
// 调它 (BiuDaemonManager 监听 access_token refresh 后推新 token)。不重启 daemon、
// 不断当前 agent 会话。见 client.SetToken。
func (w *Worker) SetToken(token string) {
	w.client.SetToken(token)
}

// InterruptSession 触发本地 daemon 内对应 sessionID 的 agent 立即停。
// 返回 true 表示找到了在飞的 agent 并调用了 Interrupt（异步 —— 实际
// engine 停在下一个 yield 点），false 表示 sessionID 没在跑。
//
// 调用方典型场景：brain ingress 收到 client 的 SDKControlCancelRequest
// 后查表得知该会话的 environment_id 是本机 → 通过新增的 cancel HTTP
// endpoint 转给本进程，进程调用 InterruptSession(sessionID)。
//
// 安全性：Interrupt() 本身幂等且不持锁阻塞 —— 多次调用 / 重复 cancel /
// agent 已自然结束都不会 panic 或泄漏。
//
// 命中时打 timestamp 进 cancelStartedAt — handleWork 在看到 Done{interrupted}
// 事件时配套 observe 延迟,这是 daemon-side 的 cancel SLO 数据。多次按 stop
// 只记第一次时间戳,跟 brain ChatRunner 同语义。
func (w *Worker) InterruptSession(sessionID uuid.UUID) bool {
	w.agentsMu.Lock()
	agent, ok := w.agents[sessionID]
	if ok {
		if _, already := w.cancelStartedAt[sessionID]; !already {
			w.cancelStartedAt[sessionID] = time.Now()
		}
	}
	w.agentsMu.Unlock()
	if !ok {
		return false
	}
	_ = agent.Interrupt()
	return true
}

// observeCancelLatency 在 agent 通过 Done{interrupted} 报告 clean stop
// 时调,从 cancelStartedAt 算延迟 observe 进 metrics。命中时清掉时间戳
// 避免重复 observe(理论上同 sessionID 只 emit 一次 Done,但防御一下)。
// 没命中(用户没按 cancel,session 自然结束)时 noop。
func (w *Worker) observeCancelLatency(sessionID uuid.UUID) {
	w.agentsMu.Lock()
	startedAt, hadCancel := w.cancelStartedAt[sessionID]
	if hadCancel {
		delete(w.cancelStartedAt, sessionID)
	}
	w.agentsMu.Unlock()
	if hadCancel {
		metrics.RecordCancelLatency("agent", time.Since(startedAt))
	}
}

// askPermissionFor 返回一个绑定到指定 sessionID 的 PermissionPolicyFn。
// engine 触发 PermissionAsk 时由 biumindkit 内部调用：
//
//  1. 生成 request_id;注册 chan 到 pendingPerms map
//  2. 包成 SDKControlRequest{can_use_tool} 通过 publishFrame 推到 .out NATS
//     subject — brain ingress 自然地透传到 client WS
//  3. 阻塞等 chan(答复经 brain control queue → handleControl → answerPermission)
//     或 30s timeout 或 ctx 取消(turn 被 interrupt)
//  4. 任何错误路径都 deny —— 默认拒绝是安全侧
//
// 当前实现忽略 PermissionRequest.Suggestions(走基本 PermissionPolicyFn,
// 不带 PermissionPolicyExtFn)。后续要 GUI 端选 "allow + add dir" 一键时
// 把 suggestions 字段塞到 SDKControlRequest 透传给 client。
func (w *Worker) askPermissionFor(sessionID uuid.UUID) biumindkit.PermissionPolicyFn {
	return func(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		requestID := uuid.NewString()
		respCh := make(chan biumindkit.PermissionDecision, 1)

		w.pendingPermsMu.Lock()
		w.pendingPerms[requestID] = respCh
		w.pendingPermsMu.Unlock()
		defer func() {
			w.pendingPermsMu.Lock()
			delete(w.pendingPerms, requestID)
			w.pendingPermsMu.Unlock()
		}()

		inputJSON, _ := json.Marshal(req.Input)
		frame := &sdkproto.SDKControlRequest{
			Type:      sdkproto.TypeControlRequest,
			RequestID: requestID,
			Request: &sdkproto.PermissionRequest{
				SubtypeF:       sdkproto.SubtypeCanUseTool,
				ToolName:       req.ToolName,
				Input:          inputJSON,
				ToolUseID:      req.ToolUseID,
				DecisionReason: req.Reason,
			},
		}

		w.logger.Debug("biu worker: askPermission",
			"session_id", sessionID, "request_id", requestID,
			"tool_name", req.ToolName, "tool_use_id", req.ToolUseID)
		// 推帧失败 → deny。常见失败:brain 接 broker 短暂 5xx;daemon 这边
		// 不重试(engine 在等结果不应该卡住),走 deny + 让用户重新触发。
		if err := w.publishFrame(ctx, sessionID, frame); err != nil {
			w.logger.Warn("askPermission: publish frame failed",
				"err", err, "session_id", sessionID, "request_id", requestID)
			return biumindkit.PermDeny
		}

		select {
		case decision := <-respCh:
			return decision
		case <-ctx.Done():
			return biumindkit.PermDeny
		case <-time.After(permissionTimeout):
			w.logger.Info("askPermission: timed out",
				"session_id", sessionID, "request_id", requestID,
				"tool_name", req.ToolName)
			return biumindkit.PermDeny
		}
	}
}

// answerPermission 把 brain 经 control queue 投回的 permission_response 投
// 递给等候的 askPermission goroutine。找不到 request_id → 静默丢弃(可能
// askPermission 已 timeout 或 ctx 取消提前退出)。
func (w *Worker) answerPermission(requestID string, decision biumindkit.PermissionDecision) {
	w.pendingPermsMu.Lock()
	respCh, ok := w.pendingPerms[requestID]
	if ok {
		delete(w.pendingPerms, requestID)
	}
	w.pendingPermsMu.Unlock()
	if !ok {
		return
	}
	respCh <- decision
}

// trackAgent / untrackAgent 维护 in-flight agent 索引。trackAgent 在
// handleWork 拿到 *Agent 后立刻调；untrackAgent 在 Submit channel 关
// 闭后调（defer 里）—— 保证查表只返回真正在跑的。
func (w *Worker) trackAgent(sessionID uuid.UUID, a *biumindkit.Agent) {
	w.agentsMu.Lock()
	w.agents[sessionID] = a
	w.agentsMu.Unlock()
}

func (w *Worker) untrackAgent(sessionID uuid.UUID) {
	w.agentsMu.Lock()
	delete(w.agents, sessionID)
	w.agentsMu.Unlock()
}

// safeGo 起一个带 panic 兜底的后台 goroutine。heartbeatLoop / controlLoop
// 原本裸 go,任一 panic 会**整 daemon 进程退出**(实测过 daemon 在 agent 工具
// 执行后突然消失,无 stack 可查)。这里 recover + 把 stack 用 slog 打出来(经
// serve 的 daemon.log 落盘),既不让单个 goroutine panic 拖垮整进程,又留下
// 取证依据。注意:goroutine 本身停了(不自动重启)——heartbeat 停 → janitor
// 90s/45s 后判离线 + 推失败帧(R8),客户端能收到明确错误,远好于静默崩溃。
func (w *Worker) safeGo(ctx context.Context, name string, fn func(context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				w.logger.Error("biu worker: background goroutine panic (recovered)",
					"goroutine", name, "panic", fmt.Sprintf("%v", r),
					"stack", string(debug.Stack()))
			}
		}()
		fn(ctx)
	}()
}

// Run 是 daemon 入口。失败注册时返错；进入主循环后**不退出**直到 ctx 取消，
// 网络错是退避重试。
func (w *Worker) Run(ctx context.Context) error {
	if err := w.register(ctx); err != nil {
		return fmt.Errorf("worker: register: %w", err)
	}
	w.logger.Info("biu worker registered",
		"environment_id", w.envID, "machine_name", w.cfg.EnvironmentName)
	defer w.deregister()

	// Heartbeat goroutine
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	w.safeGo(hbCtx, "heartbeat", w.heartbeatLoop)

	// Control loop goroutine —— 反向 poll cancel / reload 等指令。
	// 跟 work loop 平行跑,不被 work fetch 占满。失败的话退避重试。
	ctrlCtx, stopCtrl := context.WithCancel(ctx)
	defer stopCtrl()
	w.safeGo(ctrlCtx, "control", w.controlLoop)

	// Poll loop（main goroutine）
	w.pollLoop(ctx)
	return nil
}

func (w *Worker) register(ctx context.Context) error {
	// R6.2：有 X25519 pubkey 则 hex 上报,brain 会用它加密 BYOK；无则留空,
	// brain 回退明文(back-compat)。
	var pubHex string
	if len(w.cfg.PublicKey) == agentcrypto.X25519KeySize {
		pubHex = hex.EncodeToString(w.cfg.PublicKey)
	}
	resp, err := w.client.Register(ctx, RegisterReq{
		WorkerKind:   w.cfg.WorkerKind,
		MachineName:  w.cfg.EnvironmentName,
		Capabilities: w.cfg.Capabilities,
		PoolTag:      w.cfg.PoolTag,
		PublicKey:    pubHex,
	})
	if err != nil {
		return err
	}
	id, err := uuid.Parse(resp.EnvironmentID)
	if err != nil {
		return fmt.Errorf("bad environment_id: %w", err)
	}
	w.mu.Lock()
	w.envID = id
	w.registered = true
	cb := w.cfg.OnRegistered
	w.mu.Unlock()
	// B2: 通知 serve 打印 BIU_DAEMON_ENV_ID（re-register 时 env_id 一致，
	// Flutter 幂等更新无副作用）。
	if cb != nil {
		cb(id.String())
	}
	return nil
}

func (w *Worker) deregister() {
	w.mu.Lock()
	id := w.envID
	registered := w.registered
	w.mu.Unlock()
	if !registered {
		return
	}
	// 用独立 ctx 给短超时 —— 顶层 ctx 已经 cancel 了
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.client.Deregister(ctx, id); err != nil {
		w.logger.Warn("biu worker deregister failed", "err", err)
	}
}

// heartbeatLoop 每 HeartbeatPeriod 发一次 heartbeat。失败仅 log，不退出 ——
// brain janitor 90s TTL 会把 daemon 标 offline；下个 heartbeat 成功后自动
// 拉回 online（store.Heartbeat 内部 SET state='online'）。
func (w *Worker) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.HeartbeatPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ctxHB, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := w.client.Heartbeat(ctxHB, w.envID)
			cancel()
			if err != nil {
				w.logger.Warn("biu worker heartbeat failed", "err", err)
			}
		}
	}
}

// controlLoop 反向 poll control 队列(cancel / reload / 等)。
//   - 收到一条 cancel_session → InterruptSession(sessionID) 触发对应
//     agent 的 ErrInterrupted cancel cause。
//   - 不识别的 type → ack 后忽略,日志降级 INFO(老 daemon 跑新指令时
//     不该 spam ERROR)。
//   - 网络错 / 5xx → 退避重试,跟 pollLoop 同款。
//   - brain 业务 404'd（env 真被删）→ 静默退出本 loop;主 pollLoop 会
//     re-register,re-register 后 envID 一致 (Worker.envID 字段被更新),
//     下次循环拉到新 consumer。基础设施 404(代理 HTML 错误页)走普通退避。
func (w *Worker) controlLoop(ctx context.Context) {
	backoff := w.cfg.BackoffInitial
	for {
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		envID := w.envID
		w.mu.Unlock()
		if envID == uuid.Nil {
			// 还没 register 完成 —— 短暂 sleep 等主 pollLoop 拉起。
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		ctrl, err := w.client.PollControl(ctx, envID, w.cfg.PollWait)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsBrainNotFound() {
				// environment 被删 / re-registering。睡一会儿等 pollLoop
				// 那边重建,然后继续。
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
			w.logger.Warn("poll control failed; backing off",
				"err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff, w.cfg.BackoffMax)
			continue
		}
		backoff = w.cfg.BackoffInitial
		if ctrl == nil {
			continue
		}
		w.logger.Debug("biu worker: control fetched",
			"env_id", envID, "ack_token", ctrl.AckToken,
			"bytes", len(ctrl.Body))
		w.handleControl(ctx, envID, ctrl)
	}
}

// controlBody 跟 brain ingress.maybeRouteCancel / maybeRoutePermissionResponse
// （permission 与 elicitation 回包共用此投递 schema）对齐。字段都 omitempty
// 是为了未来扩展不破坏老 daemon 解析。
type controlBody struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	// permission_response / elicitation_response only:
	Subtype  string          `json:"subtype,omitempty"`  // "success" | "error"
	Response json.RawMessage `json:"response,omitempty"` // permission_result / elicitation_response JSON
	Error    string          `json:"error,omitempty"`
}

// handleControl 解析一条 control 消息并路由。无论结果如何都 ack ——
// cancel 重投也是同一个 cancel,nak 没意义。
func (w *Worker) handleControl(ctx context.Context, envID uuid.UUID, ctrl *ControlItem) {
	defer func() {
		// 总是 ack,即使解析失败 / 不识别 / session 已结束 —— 重投没用。
		ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.client.AckControl(ackCtx, envID, ctrl.AckToken); err != nil {
			w.logger.Warn("ack control failed", "err", err)
		}
	}()

	var body controlBody
	if err := json.Unmarshal(ctrl.Body, &body); err != nil {
		w.logger.Warn("control: invalid payload", "err", err)
		return
	}
	switch body.Type {
	case "cancel_session":
		sid, err := uuid.Parse(body.SessionID)
		if err != nil {
			w.logger.Warn("control: invalid session_id", "raw", body.SessionID)
			return
		}
		hit := w.InterruptSession(sid)
		w.logger.Info("control: cancel_session",
			"session_id", sid, "hit", hit, "request_id", body.RequestID)
	case "permission_response":
		decision := decodePermissionDecision(body)
		w.answerPermission(body.RequestID, decision)
		w.logger.Info("control: permission_response",
			"request_id", body.RequestID,
			"subtype", body.Subtype,
			"decision", decisionName(decision))
	case "elicitation_response":
		// 提问表单（agent-ask-form P2-b）的 client 回包。解析失败 /
		// subtype=error → cancel，等候方立刻 soft error 出局。
		ans := decodeElicitationAnswer(body)
		w.answerAsk(body.RequestID, ans)
		w.logger.Info("control: elicitation_response",
			"request_id", body.RequestID,
			"subtype", body.Subtype,
			"action", ans.Action)
	default:
		// 未知 type —— 老 daemon 跑新协议时合法,不要 spam ERROR。
		w.logger.Info("control: ignoring unknown type", "type", body.Type)
	}
}

// decodePermissionDecision 把 brain 转发过来的 control_response body 解成
// biumindkit.PermissionDecision。subtype=error 或解析失败一律 deny。
//
// permission_result schema (sdkproto/v1/permissions.go::PermissionResult):
//
//	{ "behavior": "allow" | "deny", "updatedInput": ..., "message": "..." }
//
// 当前只看 behavior 字段;updatedInput / updatedPermissions 待 PermissionPolicyExt
// 接通后再消费。
func decodePermissionDecision(body controlBody) biumindkit.PermissionDecision {
	if body.Subtype == sdkproto.ControlSubtypeError {
		return biumindkit.PermDeny
	}
	if len(body.Response) == 0 {
		return biumindkit.PermDeny
	}
	var result struct {
		Behavior string `json:"behavior"`
	}
	if err := json.Unmarshal(body.Response, &result); err != nil {
		return biumindkit.PermDeny
	}
	if result.Behavior == sdkproto.PermissionAllow {
		return biumindkit.PermAllow
	}
	return biumindkit.PermDeny
}

// decisionName 给 log 用 —— biumindkit.PermissionDecision 是 int,直接 print
// 看到数字不友好。
func decisionName(d biumindkit.PermissionDecision) string {
	switch d {
	case biumindkit.PermAllow:
		return "allow"
	case biumindkit.PermDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// pollLoop 是 main loop。退避策略：
//   - 成功 / 超时无 work → 立即下一轮（pollWait 已经隐式 backoff）
//   - 网络错 / 5xx / 基础设施 404（frp/nginx HTML 错误页，非 brain 业务 404）
//     → 指数退避到 BackoffMax
//   - brain 业务 404（environment 真被删）→ 退避后重新 register 再继续；
//     register 失败不退出（多半是 brain 还没恢复 / PAT 等 client 推新 token），
//     回到主循环下一轮再试
func (w *Worker) pollLoop(ctx context.Context) {
	backoff := w.cfg.BackoffInitial
	for {
		if ctx.Err() != nil {
			return
		}
		work, err := w.client.PollWork(ctx, w.envID, w.cfg.PollWait)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsBrainNotFound() {
				// 退避后再 register —— 防止 brain 抖一抖(短暂返 404)
				// 触发 worker 死循环 register,把 agent_environments 表
				// 在几分钟内堆爆几百万行(实测过)。
				w.logger.Warn("environment 404'd; re-registering after backoff",
					"env_id", w.envID, "backoff", backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if regErr := w.register(ctx); regErr != nil {
					// re-register 失败不再退出 —— 多半是 brain / 中间层还没
					// 恢复(502 / 代理层 404),或 PAT 过期等 client 经
					// /internal/token 推新 token。回到主循环,下一轮
					// PollWork 会再次 404 → 再试,直到恢复。
					w.logger.Warn("re-register failed; will retry",
						"err", regErr, "backoff", backoff)
				}
				// 注意 backoff 不重置 —— 让连续 404 走指数,避免 brain 抖
				// 期间持续灌新行
				backoff = nextBackoff(backoff, w.cfg.BackoffMax)
				continue
			}
			// 网络 / 5xx —— 退避重试
			w.logger.Warn("poll work failed; backing off",
				"err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff, w.cfg.BackoffMax)
			continue
		}
		backoff = w.cfg.BackoffInitial // 成功一次 → 重置退避

		if work == nil {
			continue // 超时无 work —— 立即下一轮
		}
		w.logger.Debug("biu worker: work fetched",
			"env_id", w.envID, "ack_token", work.AckToken,
			"bytes", len(work.Body))
		w.handleWork(ctx, work)
	}
}

// handleWork 跑一条 work：反序列化 → 起 agent → submit → 翻译 events 推帧
// → ack/nak。
//
// 单条 work 处理失败（agent 起不来 / 中间报错）→ Nak，broker 60s 后 redeliver。
// MaxDeliver=5 后 broker 丢消息（避免 poison loop）。
func (w *Worker) handleWork(ctx context.Context, work *WorkItem) {
	var payload WorkPayload
	// Panic isolation for this goroutine (frame-translation loop +
	// runExternalBackend orchestration). Engine panics are recovered inside
	// biumindkit.Submit's own goroutine; this catches the rest so one bad
	// work item can never crash the long-poll worker. Nak so the broker can
	// redeliver (MaxDeliver caps the poison loop).
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic handling work: %v", r)
			w.logger.Error("recovered work panic", "err", err,
				"session_id", payload.SessionID, "stack", string(debug.Stack()))
			w.publishErrorFrame(ctx, payload, err)
			_ = w.client.NakWork(ctx, w.envID, work.AckToken)
		}
	}()
	if err := json.Unmarshal(work.Body, &payload); err != nil {
		w.logger.Error("invalid work payload", "err", err)
		_ = w.client.NakWork(ctx, w.envID, work.AckToken)
		return
	}
	w.logger.Debug("biu worker: work payload",
		"session_id", payload.SessionID, "user_id", payload.UserID,
		"mode", payload.Mode, "model", payload.Model,
		"prompt_bytes", len(payload.Prompt),
		"system_bytes", len(payload.SystemPrompt))

	// P4: BYOK 投递链 (EncBYOK 信封解密) 已删 —— daemon 改用 payload.UserBearer
	// (委托 user JWT) 作 model-relay Authorization, relay 原生解析 BYOK。
	// X25519 privkey 保留给 S3-4 mcp_config 信封加密 (当前无消费者)。

	// 跑 agent；任何阶段失败都 Nak。错误转 SDKResultError frame 推给 client，
	// 让 ingress 那边 finalize hook 写 agent_session_results。
	//
	// Runtime v3 R3/Q3：外部 CLI backend（claude-cli 等）走独立路径——
	// agent.Runner 跑外部子进程,事件经 externalEventToFrame 翻成 SDK 帧。
	// D1：CLI 自执行工具,无 permission frame。RunnerBuilder 返 nil（含非
	// 外部 backend / 未注入）→ 落到下面的内建 biumindkit 路径。
	if w.cfg.RunnerBuilder != nil {
		if runner := w.cfg.RunnerBuilder(payload); runner != nil {
			w.runExternalBackend(ctx, work, payload, runner)
			return
		}
	}

	// 把 askPermissionFor(sessionID) 传给 builder —— builder 应当把它接到
	// biumindkit.Options.PermissionPolicy。这样 engine 触发 PermissionAsk
	// 时,会通过 publishFrame → brain ingress → client WS 询问;client 应
	// 答经 brain control queue 投回到 worker 的 pendingPerms map。
	//
	// askUserFor(sessionID) 同款链路（agent-ask-form P2-b）：elicitation
	// 控制帧让 client 弹 FormCard，回包经 control queue 投回 pendingAsks。
	// builder 接到 biumindkit.Options.AskUser 后 AskUserQuestion 工具才对
	// 模型可见（nil = 隐藏，P0 安全默认）。
	askPerm := w.askPermissionFor(payload.SessionID)
	askUser := w.askUserFor(payload.SessionID)
	agent, err := w.buildAgent(ctx, payload, askPerm, askUser)
	if err != nil {
		w.logger.Error("build agent", "err", err, "session_id", payload.SessionID)
		w.publishErrorFrame(ctx, payload, err)
		_ = w.client.NakWork(ctx, w.envID, work.AckToken)
		return
	}
	defer agent.Close()

	// 把 agent 注册进 in-flight 表 —— InterruptSession() 据此找到它。
	// untrack 必须在 Submit channel 关闭之后（defer 顺序：先 untrack，
	// 再 Close —— Close 内部 fire SessionEnd 时 agent 已经从表里摘掉，
	// 避免别人再 Interrupt 一个正在被关的 agent）。
	w.trackAgent(payload.SessionID, agent)
	defer w.untrackAgent(payload.SessionID)
	// session 结束 / 中断时释放该会话全部待答提问 —— 等候的 askUserFor
	// goroutine 立刻 Cancelled 出局，不干等 askUserTimeout（防泄漏）。
	defer w.cancelPendingAsks(payload.SessionID)

	for ev := range agent.Submit(ctx, payload.Prompt) {
		// 在事件 → frame 翻译之前先检测 Done{interrupted}。这是 daemon-side
		// cancel SLO 的「停下来了」信号:engine 已经走完 clean-stop(合成
		// tool_result + emit Done),帧即将通过 publishFrame 发出去。在
		// publishFrame 之前 observe 才能让 latency 不包括 NATS publish
		// 那段(那是 brain 端的事)。
		if d, ok := ev.(biumindkit.Done); ok && d.StopReason == "interrupted" {
			w.observeCancelLatency(payload.SessionID)
		}
		frame := sdkbridge.ToSDKFrame(ev, payload.SessionID.String())
		if frame == nil {
			// 某些 biumindkit event 不映射到 SDK 帧（例如 AssistantText /
			// AssistantBlock(text) 是 StreamingText 的重复快照）。skip 不
			// publish 避免 client 重复渲染。
			continue
		}
		if err := w.publishFrame(ctx, payload.SessionID, frame); err != nil {
			w.logger.Warn("publish frame failed",
				"err", err, "session_id", payload.SessionID)
			// 单帧失败不打断剩余帧推送 —— ingress 那边能推多少推多少
		}
	}
	// Submit channel 关闭 = turn 结束（Done event 已经被 toSDKFrame 翻成
	// SDKResultSuccess 推过去）—— ack
	if err := w.client.AckWork(ctx, w.envID, work.AckToken); err != nil {
		w.logger.Warn("ack work failed", "err", err)
	}
}

// runExternalBackend 跑一个外部 CLI backend（agent.Runner，如 Claude Code）。
// 把 WorkPayload 组成 agent.Request → runner.Run → agent.Event 经
// externalEventToFrame 翻成 SDK 帧 publish 给 client。D1：CLI 自执行工具,
// biumind 只观察展示,不走 permission;无 in-flight Interrupt 注册（外部进程
// 由 ctx 取消 / TimeoutSec 兜底）。
//
// R3 happy-path：每条 work 起一个全新外部会话（不 resume）。session 续传
// （提取 sessionId 持久化 + --resume）留后续。
func (w *Worker) runExternalBackend(ctx context.Context, work *WorkItem, payload WorkPayload, runner agentpkg.Runner) {
	// R6.3 路径地板：外部 CLI backend 同样不能盲信 brain 给的 Workdir。空 →
	// 默认根；越界 → 拒该 work（报错帧 + Nak），与 biumindkit 路径 build error
	// 处理对齐。nil guard（单测未注入）→ 透传。
	workdir := payload.Workdir
	if w.cfg.ResolveWorkdir != nil {
		resolved, err := w.cfg.ResolveWorkdir(payload.Workdir)
		if err != nil {
			w.logger.Error("external backend workdir rejected",
				"err", err, "session_id", payload.SessionID, "backend", payload.Backend)
			w.publishErrorFrame(ctx, payload, err)
			_ = w.client.NakWork(ctx, w.envID, work.AckToken)
			return
		}
		workdir = resolved
	}
	req := agentpkg.Request{
		Prompt:       payload.Prompt,
		SystemPrompt: payload.SystemPrompt,
		Model:        payload.Model,
		Workdir:      workdir,
		TimeoutSec:   600, // 兜底硬超时；no-output watchdog 留 R8
	}
	// A1：清平台 key + 注入（A2 留空），让 CLI 用用户自己的 ~/.claude 订阅。
	if cfg, ok := agentpkg.ResolveBackend(payload.Backend); ok {
		req.ClearEnv = cfg.ClearEnv
		req.Env = cfg.Env
		req.Model = cfg.ResolveModel(payload.Model)
	}
	w.logger.Info("agentplane: external backend run",
		"session_id", payload.SessionID, "backend", payload.Backend,
		"runner", runner.Name(), "model", req.Model)

	ch, err := runner.Run(ctx, req)
	if err != nil {
		w.logger.Error("external backend start", "err", err,
			"session_id", payload.SessionID, "backend", payload.Backend)
		w.publishErrorFrame(ctx, payload, err)
		_ = w.client.NakWork(ctx, w.envID, work.AckToken)
		return
	}
	sid := payload.SessionID.String()
	for ev := range ch {
		frame := externalEventToFrame(ev, sid)
		if frame == nil {
			continue
		}
		if err := w.publishFrame(ctx, payload.SessionID, frame); err != nil {
			w.logger.Warn("publish frame failed (external)",
				"err", err, "session_id", payload.SessionID)
		}
	}
	if err := w.client.AckWork(ctx, w.envID, work.AckToken); err != nil {
		w.logger.Warn("ack work failed (external)", "err", err)
	}
}

// publishFrame 包了 marshal + client.PublishFrame。
func (w *Worker) publishFrame(ctx context.Context, sessionID uuid.UUID, frame sdkproto.Frame) error {
	body, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if w.logger.Enabled(ctx, slog.LevelDebug) {
		w.logger.Debug("biu worker: publish frame",
			"session_id", sessionID, "bytes", len(body))
	}
	return w.client.PublishFrame(ctx, sessionID, body)
}

// publishErrorFrame 给客户端发个 SDKResultError 帧标记本次 turn 失败。
// 用 sdkbridge.ToSDKFrame 把一个 biumindkit.Error event 翻成 frame —— 复用
// 已有 mapping 路径。
func (w *Worker) publishErrorFrame(ctx context.Context, payload WorkPayload, cause error) {
	frame := sdkbridge.ToSDKFrame(biumindkit.Error{
		Err:         cause,
		Recoverable: false,
	}, payload.SessionID.String())
	_ = w.publishFrame(ctx, payload.SessionID, frame)
}

// nextBackoff 双倍退避 +/- 抖动，cap 在 max。简单实现，无 jitter
// 也无所谓 —— 单 daemon 不会 thundering herd。
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		next = max
	}
	return next
}
