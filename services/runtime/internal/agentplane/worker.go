// Worker —— S11-2 runtime work poller。
//
// 长轮询 brain `biu.work.<env_id>` subject 拿 work，per-task 起 biumindkit.Agent
// 跑，把每条事件翻译成 SDK Protocol v1 帧推回 brain。沿用 biu daemon 同模式
// （apps/cli/biu/internal/agentplane/worker.go），但下游内核是 biumindkit
// 默认目录（cloud tools 走 ExtraTools 注入）。
//
// 跟 biu daemon 区别：
//
//	biu daemon → 用户本机：可用 SDK 文件 / Bash / 本地 MCP；ToolUse 询问回 client
//	runtime    → 平台沙箱：只放 cloud-runtime 工具（time / web / wiki / memory）；
//	             权限策略默认 PermissionAllow（任务被托管在 sandbox 里）
//
// 错误处理：
//
//   - PollWork 4xx not_found → re-register 一次，失败也继续 poll loop
//   - 其它 PollWork 错误 → 指数退避（1s → 30s）
//   - WorkPayload 解析失败 → Nak（让 brain redeliver；一般是 brain↔runtime
//     版本不齐，运维人介入）
//   - biumindkit Submit 失败 → 推一帧 SDKResultError + Ack（避免 redelivery 循环）

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/google/uuid"
)

// AgentBuilder 让调用方决定每条 work 怎么构造 biumindkit.Agent。runtime
// main.go 注入的实现会带平台 Anthropic API key + cloud tools 注册。返回
// nil agent + nil err 视为 "skip this work" —— Worker 直接 Ack 不跑。
type AgentBuilder func(ctx context.Context, work WorkPayload) (*biumindkit.Agent, error)

// WorkerConfig —— 调优参数；零值有合理默认。
type WorkerConfig struct {
	// PollWait 单次 PollWork 等待最大时长。≤0 = 30s。
	PollWait time.Duration
	// BackoffInitial / BackoffMax 控制 PollWork 异常时的指数退避。零值 1s / 30s。
	BackoffInitial time.Duration
	BackoffMax     time.Duration
}

// Worker 是 runtime 长轮询主循环。绑定一个已经注册好的 Registrar +
// AgentBuilder，调用 Run(ctx) 阻塞直到 ctx 取消。
type Worker struct {
	reg    *Registrar
	build  AgentBuilder
	cfg    WorkerConfig
	logger *slog.Logger

	// agents 跟踪当前在飞的 Agent,按 sessionID 索引。InterruptSession()
	// 据此查找对应 *biumindkit.Agent 调用 Interrupt() — 让 brain ingress
	// 通过 control queue 把 client cancel 投到这里时能真正打断。handleWork
	// 进出时分别 register / unregister。
	//
	// cancelStartedAt 配合 agents 跟 metrics:InterruptSession 命中时打
	// timestamp,handleWork 在看到 biumindkit.Done{StopReason:"interrupted"}
	// 时 observe 到 metrics.RecordCancelLatency。同 agentsMu 守护防 race。
	agentsMu        sync.Mutex
	agents          map[uuid.UUID]*biumindkit.Agent
	cancelStartedAt map[uuid.UUID]time.Time
}

// NewWorker 构造 Worker。reg 必须已经成功注册（NewRegistrar 返回的）；
// build 必填（runtime 没默认 agent 实现）。
func NewWorker(reg *Registrar, build AgentBuilder, cfg WorkerConfig, logger *slog.Logger) *Worker {
	if cfg.PollWait <= 0 {
		cfg.PollWait = 30 * time.Second
	}
	if cfg.BackoffInitial <= 0 {
		cfg.BackoffInitial = 1 * time.Second
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		reg:             reg,
		build:           build,
		cfg:             cfg,
		logger:          logger,
		agents:          map[uuid.UUID]*biumindkit.Agent{},
		cancelStartedAt: map[uuid.UUID]time.Time{},
	}
}

// InterruptSession 触发 runtime 内对应 sessionID 的 agent 立即停。返回
// true 表示找到并触发了 Interrupt() (异步 — engine 在下一个 yield 点
// emit Done{interrupted}),false 表示该 sessionID 不在本进程的 agents 表里
// (可能已结束 / 在另一台 runtime 实例上)。
//
// 命中时打 timestamp 让 handleWork 后续 observe 端到端延迟。多次按 stop
// 不重置时间戳,P95 反映用户视角的真实延迟。
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

// trackAgent / untrackAgent 维护 in-flight agent 索引。trackAgent 在
// handleWork 拿到 *Agent 后立刻调;untrackAgent 在 Submit channel 关闭后
// 调(defer)。
func (w *Worker) trackAgent(sessionID uuid.UUID, a *biumindkit.Agent) {
	w.agentsMu.Lock()
	w.agents[sessionID] = a
	w.agentsMu.Unlock()
}

func (w *Worker) untrackAgent(sessionID uuid.UUID) {
	w.agentsMu.Lock()
	delete(w.agents, sessionID)
	delete(w.cancelStartedAt, sessionID) // 防止泄漏(被 cancel 但 Done 没触发到 observe)
	w.agentsMu.Unlock()
}

// observeCancelLatency 在 agent 通过 Done{interrupted} 报告 clean stop 时
// 调,从 cancelStartedAt 算延迟 observe 进 metrics。命中时清掉时间戳避免
// 重复 observe(理论上同 sessionID 只 emit 一次 Done,但防御一下)。没命中
// 时 noop。
func (w *Worker) observeCancelLatency(sessionID uuid.UUID) {
	w.agentsMu.Lock()
	startedAt, hadCancel := w.cancelStartedAt[sessionID]
	if hadCancel {
		delete(w.cancelStartedAt, sessionID)
	}
	w.agentsMu.Unlock()
	if hadCancel {
		metrics.RecordCancelLatency("task", time.Since(startedAt))
	}
}

// Run 阻塞循环。退出条件：ctx.Done(). 不会因为 PollWork 错误退出 ——
// 永远继续重试（指数退避）；调用方靠 ctx 关停。
func (w *Worker) Run(ctx context.Context) error {
	if w.reg == nil {
		return fmt.Errorf("agentplane: Worker.Run: nil Registrar")
	}
	if w.build == nil {
		return fmt.Errorf("agentplane: Worker.Run: nil AgentBuilder")
	}
	w.logger.Info("agentplane worker: poll loop start",
		"environment_id", w.reg.EnvironmentID(), "poll_wait", w.cfg.PollWait)

	// Control loop goroutine — 反向 poll cancel / reload 等指令,跟 work
	// loop 平行跑。失败的话退避重试,跟主 pollLoop 同款。
	ctrlCtx, stopCtrl := context.WithCancel(ctx)
	defer stopCtrl()
	go w.controlLoop(ctrlCtx)

	backoff := w.cfg.BackoffInitial
	for {
		if err := ctx.Err(); err != nil {
			w.logger.Info("agentplane worker: ctx cancelled, exiting")
			return nil
		}
		work, err := w.reg.PollWork(ctx, w.cfg.PollWait)
		if err != nil {
			// not_found → 环境被 janitor 标 offline；让 register loop
			// 自己 re-register（heartbeatLoop 已经处理）。这里只 backoff。
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsNotFound() {
				w.logger.Warn("agentplane worker: poll 404, waiting for re-register",
					"err", err)
				time.Sleep(backoff)
				backoff = nextBackoff(backoff, w.cfg.BackoffMax)
				continue
			}
			// ctx cancel 路径：HTTP 请求会被打断，poll 返回错；不再 backoff
			// （ctx.Err 在循环顶判断会退出）
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			w.logger.Warn("agentplane worker: poll failed; backoff",
				"err", err, "backoff", backoff)
			time.Sleep(backoff)
			backoff = nextBackoff(backoff, w.cfg.BackoffMax)
			continue
		}
		// 成功 poll 重置 backoff
		backoff = w.cfg.BackoffInitial
		if work == nil {
			// 空 long-poll 超时 —— 立即下一轮
			continue
		}
		w.handleWork(ctx, work)
	}
}

// handleWork 处理一条 work：解 payload → 跑 biumindkit → publish frames → Ack。
func (w *Worker) handleWork(ctx context.Context, item *WorkItem) {
	var payload WorkPayload
	if err := json.Unmarshal(item.Body, &payload); err != nil {
		w.logger.Error("agentplane worker: bad work payload",
			"err", err, "body_prefix", string(item.Body[:min(64, len(item.Body))]))
		// 让 brain redeliver 给别的实例（也许另一个 runtime 版本能解）；
		// MaxDeliver 会兜底。
		_ = w.reg.NakWork(context.Background(), item.AckToken)
		return
	}
	w.logger.Info("agentplane worker: handling",
		"session_id", payload.SessionID, "mode", payload.Mode,
		"model", payload.Model)
	w.logger.Debug("agentplane worker: work payload",
		"session_id", payload.SessionID, "user_id", payload.UserID,
		"thread_id", payload.ThreadID, "pool_tag", payload.PoolTag,
		"prompt_bytes", len(payload.Prompt),
		"system_bytes", len(payload.SystemPrompt))

	agent, err := w.build(ctx, payload)
	if err != nil {
		w.logger.Error("agentplane worker: build agent failed",
			"session_id", payload.SessionID, "err", err)
		w.publishErrorFrame(ctx, payload.SessionID, err)
		_ = w.reg.AckWork(context.Background(), item.AckToken)
		return
	}
	if agent == nil {
		// builder 决定 skip 这条 work（builder 自行已记录原因）
		_ = w.reg.AckWork(context.Background(), item.AckToken)
		return
	}
	defer agent.Close()

	// 把 agent 注册进 in-flight 表 — InterruptSession() 据此找到它。
	// untrack 必须在 Submit channel 关闭后(defer)才能保证查表只返回真
	// 在跑的 agent。
	w.trackAgent(payload.SessionID, agent)
	defer w.untrackAgent(payload.SessionID)

	for ev := range agent.Submit(ctx, payload.Prompt) {
		// 在事件 → frame 翻译之前先检测 Done{interrupted}。这是
		// runtime-side cancel SLO 的 「停下来了」 信号:engine 已经走完
		// clean-stop,帧即将通过 PublishFrame 发出去。在 publish 之前
		// observe 才能让 latency 不包括 NATS publish 那段(那是 brain
		// 端的延迟)。
		if d, ok := ev.(biumindkit.Done); ok && d.StopReason == "interrupted" {
			w.observeCancelLatency(payload.SessionID)
		}
		raw, marshalErr := json.Marshal(sdkbridge.ToSDKFrame(ev, payload.SessionID.String()))
		if marshalErr != nil {
			w.logger.Warn("agentplane worker: marshal frame",
				"session_id", payload.SessionID, "err", marshalErr)
			continue
		}
		if w.logger.Enabled(ctx, slog.LevelDebug) {
			w.logger.Debug("agentplane worker: publish frame",
				"session_id", payload.SessionID, "bytes", len(raw))
		}
		if pubErr := w.reg.PublishFrame(ctx, payload.SessionID, raw); pubErr != nil {
			w.logger.Warn("agentplane worker: publish frame",
				"session_id", payload.SessionID, "err", pubErr)
			// 继续推剩余帧 —— 客户端最坏 timeout / desync，但 LLM turn
			// 不能因 broker 瞬断停下
		}
	}
	if err := w.reg.AckWork(context.Background(), item.AckToken); err != nil {
		w.logger.Warn("agentplane worker: ack failed",
			"session_id", payload.SessionID, "err", err)
	}
	w.logger.Info("agentplane worker: session done",
		"session_id", payload.SessionID)
}

// controlLoop 反向 poll control 队列(cancel / reload / 等)。
//   - 收到 cancel_session → InterruptSession(sessionID) 触发对应 agent
//     的 ErrInterrupted cancel cause。
//   - 不识别的 type → ack 后忽略,日志降级 INFO(老 runtime 跑新指令时
//     不该 spam ERROR)。
//   - 网络错 / 5xx → 退避重试,跟 pollLoop 同款。
//   - environment 404'd → 静默退,主 register loop 会 re-register;下次
//     循环拉到新的 control consumer。
func (w *Worker) controlLoop(ctx context.Context) {
	backoff := w.cfg.BackoffInitial
	for {
		if ctx.Err() != nil {
			return
		}
		ctrl, err := w.reg.PollControl(ctx, w.cfg.PollWait)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsNotFound() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			w.logger.Warn("agentplane worker: poll control failed; backoff",
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
		w.logger.Debug("agentplane worker: control fetched",
			"env_id", w.reg.EnvironmentID(), "bytes", len(ctrl.Body))
		w.handleControl(ctx, ctrl)
	}
}

// controlBody 跟 brain ingress.maybeRouteCancel 投递的 schema 对齐。
// 字段都 omitempty 是为了未来扩展不破坏老 runtime 解析。
type controlBody struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// handleControl 解析一条 control 消息并路由。无论结果如何都 ack ——
// cancel 重投也是同一个 cancel,nak 没意义。
func (w *Worker) handleControl(ctx context.Context, ctrl *ControlItem) {
	defer func() {
		ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.reg.AckControl(ackCtx, ctrl.AckToken); err != nil {
			w.logger.Warn("agentplane worker: ack control failed", "err", err)
		}
	}()

	var body controlBody
	if err := json.Unmarshal(ctrl.Body, &body); err != nil {
		w.logger.Warn("agentplane worker: invalid control payload", "err", err)
		return
	}
	switch body.Type {
	case "cancel_session":
		sid, err := uuid.Parse(body.SessionID)
		if err != nil {
			w.logger.Warn("agentplane worker: control cancel — bad session_id",
				"raw", body.SessionID)
			return
		}
		hit := w.InterruptSession(sid)
		w.logger.Info("agentplane worker: control cancel_session",
			"session_id", sid, "hit", hit, "request_id", body.RequestID)
	default:
		w.logger.Info("agentplane worker: control — ignoring unknown type",
			"type", body.Type)
	}
}

// publishErrorFrame 给客户端推一帧 SDKResultError 让 ingress WS 关掉
// 连接。不阻塞主流程：失败只 log。
func (w *Worker) publishErrorFrame(ctx context.Context, sessionID uuid.UUID, runErr error) {
	frame := sdkbridge.ToSDKFrame(biumindkit.Error{
		Err: runErr, Recoverable: false,
	}, sessionID.String())
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = w.reg.PublishFrame(pubCtx, sessionID, raw)
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		next = max
	}
	return next
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
