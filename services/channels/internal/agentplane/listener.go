// Listener 订阅 brain session `.out` subject，把 SDK Protocol 帧聚合后通过
// driver.Send 推回原 channel（Telegram / 微信 / 邮件 / ...）。
//
// 帧聚合策略（SDK Protocol → 单条用户消息）：
//
//	streamlined_text 帧 → 累积到 builder
//	tool_progress / tool_use_summary → 不发给用户（IM 用户不关心 internal tool）
//	result(success) 帧 → builder 累积内容 + driver.Send 一次成形发送
//	result(error)   帧 → 推一条 "abort: <msg>" 让用户知道 task 失败
//
// 多 turn 任务在 v1 不支持（chat WS / agent 模式才有）。task mode 是 single
// shot，session 关闭后 listener 也退出。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/services/channels/internal/driver"
	"github.com/biumind/biumind/services/channels/internal/envelope"
	"github.com/google/uuid"
)

// Listener 订阅 brain session 的 .out subject 然后通过 driver.Send 把结果
// 推回原 channel。每次 channels.Inbound 创了 task session 调
// SubscribeAndReply 起一条 listener，session 完成后自动结束。
type Listener struct {
	JS      bus.JetStream
	Drivers map[string]driver.Driver
	Logger  *slog.Logger

	// PerSessionTimeout —— 防 listener 在客户端断 session 后泄漏。生产
	// task mode 大部分 ≤ 60s 完成；30 分钟 hard cap 兜底。
	PerSessionTimeout time.Duration
}

// NewListener 构造 Listener。drivers 是 Router.drivers 同款 map（按名字
// 找 driver）；listener 不直接调 router，避免循环。
func NewListener(js bus.JetStream, drivers map[string]driver.Driver, logger *slog.Logger) *Listener {
	if logger == nil {
		logger = slog.Default()
	}
	return &Listener{
		JS:                js,
		Drivers:           drivers,
		Logger:            logger,
		PerSessionTimeout: 30 * time.Minute,
	}
}

// ReplyContext —— SubscribeAndReply 收到 result 帧后构造回复 envelope 用。
// 由 router.Inbound 提供（拿原 inbound envelope 的 channel + sender 反推）。
type ReplyContext struct {
	Channel        string          // 用哪个 driver 推送
	ConversationID string          // 落到 reply envelope.ConversationID
	ReplyTo        string          // 上游 message_id 让 driver 做 thread reply
	Recipient      envelope.Sender // 给谁
}

// SubscribeAndReply 起一个独立 goroutine 订阅 session.out subject。session
// result 帧到达时构造回复 envelope 并通过对应 driver.Send 推回。
//
// 返回的 stop 函数让调用方提前结束（一般不用，listener 自己 result 帧后
// drain 退出）。
func (l *Listener) SubscribeAndReply(ctx context.Context, sessionID uuid.UUID, reply ReplyContext) (stop func(), err error) {
	if l.JS == nil {
		return nil, fmt.Errorf("agentplane listener: nil JetStream")
	}
	d, ok := l.Drivers[reply.Channel]
	if !ok {
		return nil, fmt.Errorf("agentplane listener: no driver registered for channel %q", reply.Channel)
	}

	subject := "biu.session." + sessionID.String() + ".out"
	durable := "channels-listener-" + sessionID.String()[:8]

	// listener 自己的 ctx —— 跟父 ctx + PerSessionTimeout 取交集。
	listenerCtx, cancel := context.WithTimeout(ctx, l.PerSessionTimeout)

	state := &replyState{
		sessionID: sessionID,
		reply:     reply,
		driver:    d,
		logger:    l.Logger,
		cancel:    cancel,
	}

	sub, subErr := l.JS.Subscribe(listenerCtx, bus.ConsumerSpec{
		Stream:        "BIU_SESSIONS",
		Durable:       durable,
		FilterSubject: subject,
		AckWait:       30 * time.Second,
		MaxDeliver:    3,
	}, func(_ context.Context, m *bus.Message) error {
		state.handleFrame(listenerCtx, m.Body)
		return nil
	})
	if subErr != nil {
		cancel()
		return nil, fmt.Errorf("agentplane listener: subscribe %s: %w", subject, subErr)
	}

	state.sub = sub
	go func() {
		<-listenerCtx.Done()
		_ = sub.Drain()
	}()
	return func() {
		cancel()
	}, nil
}

// replyState —— per-session aggregator。同一 session 多个文本帧累积，
// 等 result 帧 fire driver.Send 一次成形发送。
type replyState struct {
	sessionID uuid.UUID
	reply     ReplyContext
	driver    driver.Driver
	logger    *slog.Logger
	sub       bus.Subscription
	cancel    context.CancelFunc

	mu      sync.Mutex
	builder strings.Builder
	sent    bool // 防 result 帧 redelivery 双发
}

// frameProbe 是给 json.Unmarshal 看 type / subtype 用的最小子集。具体
// 字段（text / errors）在分支里再 unmarshal 一遍。
type frameProbe struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Text    string `json:"text,omitempty"`
}

func (s *replyState) handleFrame(ctx context.Context, raw []byte) {
	var probe frameProbe
	if err := json.Unmarshal(raw, &probe); err != nil {
		s.logger.Warn("agentplane listener: bad frame",
			"session_id", s.sessionID, "err", err)
		return
	}
	switch probe.Type {
	case "streamlined_text":
		s.mu.Lock()
		s.builder.WriteString(probe.Text)
		s.mu.Unlock()
	case "result":
		s.mu.Lock()
		if s.sent {
			s.mu.Unlock()
			return
		}
		s.sent = true
		text := s.builder.String()
		s.mu.Unlock()

		if probe.Subtype == "success" {
			s.dispatchReply(ctx, text)
		} else {
			s.dispatchReply(ctx, "[task failed: "+probe.Subtype+"]")
		}
		// session 结束 —— cancel ctx 让 sub Drain，listener goroutine 退出
		if s.cancel != nil {
			s.cancel()
		}
	default:
		// tool_progress / tool_use_summary / 等中间帧 —— IM 用户不需要看
	}
}

// dispatchReply 通过 driver 发回。失败只 log，不重试 ——
// driver 错误一般是 channel 配置 / 平台限流，重试也好不到哪。
func (s *replyState) dispatchReply(ctx context.Context, text string) {
	if text == "" {
		s.logger.Debug("agentplane listener: empty reply, skipping send",
			"session_id", s.sessionID)
		return
	}
	out := envelope.Envelope{
		Channel:        s.reply.Channel,
		ConversationID: s.reply.ConversationID,
		Sender:         s.reply.Recipient,
		Text:           text,
		ReplyTo:        s.reply.ReplyTo,
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.driver.Send(sendCtx, out); err != nil {
		s.logger.Warn("agentplane listener: driver.Send failed",
			"session_id", s.sessionID, "channel", s.reply.Channel,
			"err", err)
		return
	}
	s.logger.Debug("agentplane listener: reply sent",
		"session_id", s.sessionID, "channel", s.reply.Channel,
		"len", len(text))
}
