// Agent-plane readiness reconciler —— 根治启动竞态：启动时刻 NATS /
// JetStream 未就绪时，旧代码让 WS ingress 与 worker poll 路由终身不挂载
// （请求落默认 mux 404 → daemon 无限重注册 / 客户端无限转圈，实测踩过）。
//
// 现在路由无条件挂载、惰性消费 readiness；本组件在后台驱动状态机：
//
//	disconnected → connected → streams_ready
//
// 外加终态 disabled（NATS_URL 为空 —— 无需 reconcile，/readyz 视作就绪，
// WS / worker poll 恒定 503 no_jetstream，语义正确）。
//
// 任一 Ensure 失败保持重试，退避 Tick → MaxBackoff 指数增长。broker 后来
// 断线会翻回 disconnected；nats.go 的 JetStream 句柄能跨重连存活，重连后
// 重新 Ensure（幂等）即回 streams_ready —— 全程不重启进程自愈。
package agentplane

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

// ReadinessState 是 agent-plane NATS/JetStream readiness 状态机。
type ReadinessState string

const (
	// ReadinessDisabled —— NATS_URL 为空；无事可 reconcile。/readyz 视
	// 作就绪（disabled 不算失败），JS 消费者恒定 503。
	ReadinessDisabled ReadinessState = "disabled"
	// ReadinessDisconnected —— 配了 NATS 但 bus 未连上（broker 挂了或
	// 还在握手）。
	ReadinessDisconnected ReadinessState = "disconnected"
	// ReadinessConnected —— bus 已连上，流尚未全部 ensured。
	ReadinessConnected ReadinessState = "connected"
	// ReadinessStreamsReady —— work / control / session 流全部 ensured；
	// JetStream + Queue 消费者放行。
	ReadinessStreamsReady ReadinessState = "streams_ready"
)

// ReadinessSnapshot 是 /readyz 的 payload。
type ReadinessSnapshot struct {
	NATS    string `json:"nats"`              // connected | disconnected | disabled
	Streams string `json:"jetstream_streams"` // ready | pending | disabled
	Queue   bool   `json:"queue"`
	Ingress bool   `json:"ingress"`
	// LastError 记录最近一次 reconcile 失败原因（pending 时排查用）。
	LastError string `json:"last_error,omitempty"`
}

// Readiness 在后台 reconcile NATS 连接 + agent-plane JetStream 流，并
// 提供线程安全的当前句柄查询。
type Readiness struct {
	bus    bus.Bus
	queue  *Queue
	logger *slog.Logger

	// Tick / MaxBackoff 是重试节奏上下限。导出以便测试在 Run 前调小。
	Tick       time.Duration
	MaxBackoff time.Duration

	mu      sync.RWMutex
	state   ReadinessState
	js      bus.JetStream // 仅 streams_ready 时非 nil
	lastErr string
}

// NewReadiness 构造 reconciler。natsConfigured=false（NATS_URL 空）时直
// 接落 disabled 终态，Run 立即返回。queue 必须是 NewQueue(nil) 建出来的
// 同一个实例 —— reconciler 就绪后经 queue.SetJS 灌入句柄。
func NewReadiness(b bus.Bus, natsConfigured bool, queue *Queue, logger *slog.Logger) *Readiness {
	if logger == nil {
		logger = slog.Default()
	}
	state := ReadinessDisconnected
	if !natsConfigured {
		state = ReadinessDisabled
	}
	return &Readiness{
		bus: b, queue: queue, logger: logger,
		Tick: 2 * time.Second, MaxBackoff: 30 * time.Second,
		state: state,
	}
}

// State 返回当前状态机状态。
func (r *Readiness) State() ReadinessState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Ready 是 /readyz 的整体判定：disabled 视作就绪（无事可等），否则必须
// streams_ready。
func (r *Readiness) Ready() bool {
	s := r.State()
	return s == ReadinessDisabled || s == ReadinessStreamsReady
}

// NATSConnected 报告 broker 实时连通性（connected 或 streams_ready）。
// 给仍在 boot 时一次性 wire 的订阅者（graph extractor / wiki ingest
// subscriber）做门槛 —— 它们跟 readiness 消费同一份快照，不另起判断。
func (r *Readiness) NATSConnected() bool {
	s := r.State()
	return s == ReadinessConnected || s == ReadinessStreamsReady
}

// JetStream 返回当前 durable 句柄 —— 仅 streams_ready 时非 nil。
func (r *Readiness) JetStream() bus.JetStream {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.js
}

// Queue 返回可用的 work queue —— 未就绪时 nil。handler 必须把 nil 当
// 503 no_jetstream 处理。
func (r *Readiness) Queue() *Queue {
	if r.JetStream() == nil {
		return nil
	}
	return r.queue
}

// Snapshot 取 /readyz 用的状态快照。
func (r *Readiness) Snapshot() ReadinessSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	switch r.state {
	case ReadinessDisabled:
		return ReadinessSnapshot{NATS: "disabled", Streams: "disabled"}
	case ReadinessStreamsReady:
		return ReadinessSnapshot{NATS: "connected", Streams: "ready", Queue: true, Ingress: true}
	default: // disconnected / connected —— 流 pending
		nats := "disconnected"
		if r.state == ReadinessConnected {
			nats = "connected"
		}
		return ReadinessSnapshot{NATS: nats, Streams: "pending", LastError: r.lastErr}
	}
}

// HandleReadyz 服务 `GET /readyz`：全就绪（或 disabled）200 + 快照，否
// 则 503 + 快照。main.go 把它挂在 /healthz 旁边。
func (r *Readiness) HandleReadyz(w http.ResponseWriter, _ *http.Request) {
	snap := r.Snapshot()
	status := http.StatusOK
	if !r.Ready() {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(snap)
}

// Run 驱动 reconcile 循环直到 ctx cancel。disabled 时立即返回（无事可做）。
func (r *Readiness) Run(ctx context.Context) {
	if r.State() == ReadinessDisabled {
		return
	}
	backoff := r.Tick
	for {
		if r.reconcile(ctx) {
			backoff = r.Tick
		} else {
			backoff = min(backoff*2, r.MaxBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// reconcile 推一次状态机。返回 true 表示稳态（按 base tick 节奏走），
// false 表示需要带退避重试。
func (r *Readiness) reconcile(ctx context.Context) bool {
	if !r.bus.Connected() {
		r.transition(ReadinessDisconnected, nil, "")
		return false
	}
	if r.State() == ReadinessDisconnected {
		r.transition(ReadinessConnected, nil, "")
	}
	if r.State() == ReadinessStreamsReady {
		return true
	}
	js, err := r.bus.JetStream()
	if err != nil {
		r.setErr("jetstream init: " + err.Error())
		return false
	}
	// 先把句柄灌进 queue —— Ensure* 方法的流 spec 长在 Queue 上,要先有
	// js 才能跑。消费方仍经 Readiness.Queue() 门控,提前灌入不会放行。
	r.queue.SetJS(js)
	ensures := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"work", r.queue.EnsureWorkStream},
		{"control", r.queue.EnsureControlStream},
		{"session", r.queue.EnsureSessionStream},
	}
	for _, e := range ensures {
		ensureCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := e.fn(ensureCtx)
		cancel()
		if err != nil {
			r.setErr("ensure " + e.name + " stream: " + err.Error())
			return false
		}
	}
	r.transition(ReadinessStreamsReady, js, "")
	return true
}

// transition 切状态并打 INFO 日志（仅真切换时）。
func (r *Readiness) transition(to ReadinessState, js bus.JetStream, errMsg string) {
	r.mu.Lock()
	if r.state == to {
		r.js = js
		r.lastErr = errMsg
		r.mu.Unlock()
		return
	}
	prev := r.state
	r.state = to
	r.js = js
	r.lastErr = errMsg
	r.mu.Unlock()
	r.logger.Info("agentplane readiness: state transition",
		"from", prev, "to", to)
}

// setErr 记录一次 reconcile 失败（保持当前状态,由 Run 退避重试）。
func (r *Readiness) setErr(msg string) {
	r.mu.Lock()
	r.lastErr = msg
	r.mu.Unlock()
	r.logger.Warn("agentplane readiness: reconcile failed, will retry", "err", msg)
}
