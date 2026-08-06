// Package router decides what happens to inbound envelopes.
//
// Today: forwards to the configured Runtime URL (POST /v1/agents/run) so an
// agent can handle the message; on success, sends the agent's reply
// through the originating driver (Telegram bot, Discord channel, etc).
//
// MVP simplification: forwarding is best-effort, fire-and-forget, with a
// 10s timeout. Production replaces this with NATS publish so Runtime can
// scale workers independently. The function signature is stable enough to
// swap implementations without touching the API layer.
package router

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/services/channels/internal/driver"
	"github.com/biumind/biumind/services/channels/internal/envelope"
	"github.com/biumind/biumind/services/channels/internal/memorybridge"
)

type Router struct {
	// Drivers keyed by channel name.
	drivers map[string]driver.Driver

	// Env tags subjects / log fields. Defaults to "dev". Kept for
	// observability / future bus pub paths even after S11-4 dropped
	// channels.inbound.<channel>.
	Env string

	Logger *slog.Logger

	// DedupTTL — webhook replay protection window. The same
	// (channel, message_id) pair seen twice within this window is
	// dropped instead of forwarded. Default 5 minutes.
	DedupTTL time.Duration

	// recent — small ring of recent inbound envelopes for debug + tests
	// (Send() uses the conversation_id to find the channel without
	// requiring callers to specify it explicitly).
	mu     sync.Mutex
	recent []envelope.Envelope
	max    int

	// seen — message_id → expiry for the dedup window.
	seen map[string]time.Time

	// Memory — when non-nil, the router queries Brain.Memory before
	// forwarding so the agent receives related memories alongside
	// the message. Nil disables memory enrichment entirely.
	Memory *memorybridge.Bridge

	// AgentPlane —— 唯一 inbound 派发路径（S11-4 删了 JS/HTTP 老路径）。
	// nil 时 Inbound 仅 record + log（dev 友好）；生产部署必须配齐让
	// brain agent_plane 可达。
	AgentPlane *AgentPlaneIntegration
}

// AgentPlaneIntegration 把 channels/internal/agentplane 的 Trigger
// （HTTP client to brain）+ Listener（NATS subscriber）粘到 Router 上。
// nil 时 Inbound 走老路径。
//
// 接口故意保留 channels/internal/agentplane 子包不直接 import 到
// channels/internal/router —— router 包只依赖一个 OnInbound 函数指针，
// 测试用 fake 实现就够。
type AgentPlaneIntegration struct {
	// CreateTaskSession 调 brain POST /v1/agent/sessions。返回 session_id +
	// 错误（IsNoRuntime() true 时 router 走 fallback）。
	CreateTaskSession func(ctx context.Context, req AgentPlaneCreateReq) (sessionID string, err error)
	// SubscribeAndReply 订阅 session.out 帧并 driver.Send 回客户端。
	// 失败只 log，不阻塞 Inbound 主流程。
	SubscribeAndReply func(ctx context.Context, sessionID string, reply AgentPlaneReply) error
}

// AgentPlaneCreateReq —— 跟 channels/internal/agentplane.CreateTaskSessionReq
// 字段对齐；router 用结构体字段，调用方用 channels/internal/agentplane 反包。
type AgentPlaneCreateReq struct {
	Prompt       string
	Model        string
	SystemPrompt string
	PoolTag      string
	ThreadID     string
}

// AgentPlaneReply 给 listener 让它知道把 result 帧推到哪。
type AgentPlaneReply struct {
	Channel        string
	ConversationID string
	ReplyTo        string
	Recipient      envelope.Sender
}

// New 构造一条空 Router；caller 自己注入 AgentPlane / Memory / drivers。
// 老 runtime URL / hub bearer 在 S11-4 删了（channels 直接走 brain
// agent_plane，没本地 runtime URL 了）。
func New(logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		drivers:  map[string]driver.Driver{},
		Env:      "dev",
		Logger:   logger,
		DedupTTL: 5 * time.Minute,
		max:      500,
		seen:     map[string]time.Time{},
	}
}

// WithEnv 设置 Env 标签（subject prefix / log field 用）。
func (r *Router) WithEnv(env string) *Router {
	if env != "" {
		r.Env = env
	}
	return r
}

func (r *Router) Register(d driver.Driver) {
	r.drivers[d.Name()] = d
}

func (r *Router) Driver(name string) (driver.Driver, bool) {
	d, ok := r.drivers[name]
	return d, ok
}

// Routes — list of registered driver names (deterministic order helps
// tests + status pages).
func (r *Router) Routes() []string {
	out := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		out = append(out, name)
	}
	return out
}

// Inbound is called by the API layer with the envelopes a driver parsed
// out of a webhook. The router records them and forwards to Runtime.
//
// Forwarding errors are LOGGED, not returned — the platform doesn't care
// whether downstream worked, it just needs a 200 on the webhook.
func (r *Router) Inbound(ctx context.Context, envs []envelope.Envelope) {
	for i := range envs {
		if r.isDuplicate(envs[i]) {
			r.Logger.Debug("dedup drop", "channel", envs[i].Channel,
				"msg_id", envs[i].MessageID)
			continue
		}
		r.remember(envs[i])

		// Memory enrichment — best-effort. Failures must NOT block
		// forwarding (the message itself is the priority).
		var memCtx []memorybridge.Recalled
		var memMode string
		if r.Memory != nil {
			hits, mode, err := r.Memory.Recall(ctx, envs[i].Text)
			if err != nil {
				r.Logger.Warn("memory recall failed; forwarding without context",
					"err", err)
			} else {
				memCtx = hits
				memMode = mode
			}
		}

		// AgentPlane 是唯一路径（S11-4 后老 JS / HTTP forward 删了）。
		// AgentPlane 未注入时 envelope 只 record + log，方便 dev 部署
		// 不带 brain 也不爆。
		if r.AgentPlane == nil || r.AgentPlane.CreateTaskSession == nil {
			r.Logger.Info("inbound (AgentPlane not configured; envelope only recorded)",
				"channel", envs[i].Channel,
				"sender", envs[i].Sender.PlatformID,
				"text", envs[i].Text,
				"memory_hits", len(memCtx))
			continue
		}
		if !r.dispatchViaAgentPlane(ctx, envs[i], memCtx, memMode) {
			r.Logger.Warn("agentplane dispatch failed; envelope dropped",
				"channel", envs[i].Channel, "msg_id", envs[i].MessageID)
		}
	}
}

// isDuplicate returns true when (channel, message_id) was seen within
// the dedup window. Side effect: marks the pair as seen, sweeps expired
// entries lazily so the map can't grow unbounded.
func (r *Router) isDuplicate(e envelope.Envelope) bool {
	if e.MessageID == "" {
		return false
	}
	key := e.Channel + ":" + e.MessageID
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	// Lazy sweep — keeps the map small without a goroutine.
	if len(r.seen) > 1024 {
		for k, exp := range r.seen {
			if exp.Before(now) {
				delete(r.seen, k)
			}
		}
	}
	if exp, hit := r.seen[key]; hit && exp.After(now) {
		return true
	}
	r.seen[key] = now.Add(r.DedupTTL)
	return false
}

// Recent returns up to `n` most recent recorded envelopes (newest first).
// Used by tests and the /v1/channels/recent debug endpoint.
func (r *Router) Recent(n int) []envelope.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.recent) {
		n = len(r.recent)
	}
	out := make([]envelope.Envelope, n)
	for i := 0; i < n; i++ {
		out[i] = r.recent[len(r.recent)-1-i]
	}
	return out
}

func (r *Router) remember(e envelope.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recent = append(r.recent, e)
	if len(r.recent) > r.max {
		// Drop the oldest half to amortise.
		r.recent = r.recent[len(r.recent)-r.max/2:]
	}
}

// dispatchViaAgentPlane 把 envelope 转成 brain Agent Plane task session +
// 起 listener 收 .out 帧。返回 true = 成功调度（即使 session 还在跑），
// false = 走降级（老 JS / HTTP 路径）。
//
// 失败分流：
//   - 503 no_runtime_available → false（让 caller 降级）
//   - 其它错误 → log warn + false（兜底走老路径）
//   - 成功创建 session 但 listener 起不来 → log warn 仍 true
//     （session 已经创建，user 收不到回复也比双发好）
func (r *Router) dispatchViaAgentPlane(
	ctx context.Context, e envelope.Envelope,
	_ []memorybridge.Recalled, _ string,
) bool {
	systemPrompt := composeChannelSystem(e)
	req := AgentPlaneCreateReq{
		Prompt:       e.Text,
		SystemPrompt: systemPrompt,
		ThreadID:     e.ConversationID,
	}
	sessionID, err := r.AgentPlane.CreateTaskSession(ctx, req)
	if err != nil {
		// no_runtime → 降级；其它错也降级（老路径作兜底，比丢消息好）
		r.Logger.Warn("agentplane: CreateTaskSession failed; falling back",
			"err", err, "channel", e.Channel, "msg_id", e.MessageID)
		return false
	}

	// 起 listener；listener.Subscribe 失败不影响"session 已创建"事实，
	// 但用户收不到回复 —— log warn 让运维知道
	if r.AgentPlane.SubscribeAndReply != nil {
		reply := AgentPlaneReply{
			Channel:        e.Channel,
			ConversationID: e.ConversationID,
			ReplyTo:        e.MessageID,
			Recipient:      e.Sender,
		}
		if err := r.AgentPlane.SubscribeAndReply(ctx, sessionID, reply); err != nil {
			r.Logger.Warn("agentplane: SubscribeAndReply failed; user won't get reply",
				"err", err, "session_id", sessionID,
				"channel", e.Channel, "msg_id", e.MessageID)
		}
	}
	r.Logger.Debug("agentplane: dispatched via task session",
		"session_id", sessionID, "channel", e.Channel,
		"msg_id", e.MessageID)
	return true
}

// composeChannelSystem 跟 runtime channelsbus.composeSystem 同款，给 LLM
// 一段"你正在通过 X channel 回复 Y"的上下文。沿用于平台无关——LLM 知道
// 自己在 IM 场景应该用更短的回复风格。
func composeChannelSystem(e envelope.Envelope) string {
	var sb []string
	channel := e.Channel
	if channel == "" {
		channel = "an external channel"
	}
	sb = append(sb, "You are responding to a message that arrived via "+channel+".")
	if name := strings.TrimSpace(e.Sender.DisplayName); name != "" {
		line := "Sender: " + name
		if e.Sender.PlatformID != "" {
			line += " (" + e.Sender.PlatformID + ")"
		}
		sb = append(sb, line)
	}
	sb = append(sb, "Keep replies concise and conversational; the user is on a chat platform.")
	return strings.Join(sb, "\n")
}
