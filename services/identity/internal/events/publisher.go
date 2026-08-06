// Package events publishes billing-domain events onto NATS JetStream.
//
// W3-1 / W3-2 (BiuMind-Billing-Membership-Dev-Plan §4):
//   - Stream:  BIUMIND_BILLING_EVENTS
//   - Subject: biumind.<env>.billing.events.<kind>
//   - Retention: limits, MaxAge=72h, file storage, Replicas=1 (dev) / 3 (prod)
//
// 发布者只负责"事件发出去就走"; 落库 / Grafana 看板 / 对账由下游
// (workers/billing-sink → ClickHouse) 处理. 单 NATS 连接挂掉时,
// Publish 返错; caller 决定是否回滚 DB 事务 (我们的策略: NATS 失败
// 只警告, DB 已 commit, 让对账脚本 W3-8 兜底; 钱不能因为 NATS 连
// 不上就丢).
//
// 设计取舍:
//   - JSON 序列化, 不走 protobuf — sink 是 Python, JSON 直接消费;
//     未来要换 wire 时只改这里 + sink, 不影响调用方 (Publish 接受
//     强类型 Event*).
//   - Publisher 接口而非具体类型, 让 credits/billing 包好 mock; 真
//     实使用时由 main.go 从 bus.JetStream 包装.
//   - 不支持批量发布: 单条 Publish 即可, 计费写一笔事件一笔, 失败
//     重试由 caller 决定. 量级估计 < 1k QPS, 够了.
package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

// StreamName / SubjectPrefix 常量. main.go EnsureStream + Publisher.subject() 共用.
const (
	StreamName = "BIUMIND_BILLING_EVENTS"
)

// SubjectPrefix builds the per-kind subject. e.g. env="dev", kind="consume" →
// "biumind.dev.billing.events.consume".
func SubjectPrefix(env string) string {
	return bus.Subject(env, "billing", "events")
}

// EnsureStream 在 main.go 启动时调一次. 幂等. retention=limits, file storage,
// 72h MaxAge — 短期 buffer, sink 落库后 NATS 不再做长期存储.
func EnsureStream(ctx context.Context, js bus.JetStream, env string, replicas int) error {
	if js == nil {
		return errors.New("events: nil JetStream handle")
	}
	if replicas < 1 {
		replicas = 1
	}
	return js.EnsureStream(ctx, bus.StreamSpec{
		Name:      StreamName,
		Subjects:  []string{SubjectPrefix(env) + ".>"},
		Retention: bus.RetentionLimits,
		Storage:   bus.StorageFile,
		MaxAge:    72 * time.Hour,
		Replicas:  replicas,
	})
}

// ─── Event types ────────────────────────────────────────────────────

// Kind 是 6 类账单事件的枚举. SubjectPrefix(env) + "." + Kind 落 NATS subject.
type Kind string

const (
	KindConsume      Kind = "consume"      // 一次性扣减 (Consume)
	KindRefund       Kind = "refund"       // 退款 (Refund)
	KindHold         Kind = "hold"         // 预扣 (HoldCreate)
	KindSettle       Kind = "settle"       // 预扣结算 (Settle)
	KindRelease      Kind = "release"      // 预扣释放 (Release)
	KindSubscription Kind = "subscription" // 订阅升降级 / 续费 / 取消
)

// Common — 6 类事件的公共头. JSON 序列化时与具体 payload 合并到顶层.
//
// EventID + IdempotencyKey + UserID + OccurredAt 必填. Sink 用 EventID
// 做主键去重.
type Common struct {
	EventID        uuid.UUID `json:"event_id"`
	Kind           Kind      `json:"kind"`
	UserID         uuid.UUID `json:"user_id"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"` // 同一动作重发用
	OccurredAt     time.Time `json:"occurred_at"`
	Env            string    `json:"env"`
}

// ConsumeEvent — Consume 后立即发. Amount 始终正数, 表示消耗的积分量;
// LogID 引用 identity.credit_logs 行做对账锚点.
type ConsumeEvent struct {
	Common
	LogID        uuid.UUID `json:"log_id"`
	Amount       int64     `json:"amount"` // 永远 > 0, 单位 = 积分
	RefType      string    `json:"ref_type,omitempty"`
	RefID        string    `json:"ref_id,omitempty"`
	ModelCode    string    `json:"model_code,omitempty"` // 用于 model 维度 dashboard
	ProviderCode string    `json:"provider_code,omitempty"`
	UpstreamUSD  float64   `json:"upstream_usd,omitempty"` // 实际供应商成本 (毛利计算)
	UpstreamCNY  float64   `json:"upstream_cny,omitempty"`
}

// RefundEvent — 退款后发. RefundOfLogID 指向被退的原 Consume.
// Amount 也是正数 (实际退还的积分).
type RefundEvent struct {
	Common
	LogID         uuid.UUID `json:"log_id"`
	RefundOfLogID uuid.UUID `json:"refund_of_log_id"`
	Amount        int64     `json:"amount"` // 始终 > 0
}

// HoldEvent — HoldCreate 后立即发. Hold 不影响余额视图但锁定 credits.
type HoldEvent struct {
	Common
	HoldID       uuid.UUID `json:"hold_id"`
	Amount       int64     `json:"amount"`
	RefType      string    `json:"ref_type,omitempty"`
	RefID        string    `json:"ref_id,omitempty"`
	ModelCode    string    `json:"model_code,omitempty"`
	ProviderCode string    `json:"provider_code,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// SettleEvent — Settle 完成后发. Actual = 实际消耗 (≤ Hold.Amount).
// HoldDelta = Hold.Amount - Actual (= 自动 release 部分, 可能为 0).
type SettleEvent struct {
	Common
	HoldID    uuid.UUID `json:"hold_id"`
	LogID     uuid.UUID `json:"log_id"` // settle 写的 credit_log
	Actual    int64     `json:"actual"`
	HoldDelta int64     `json:"hold_delta"` // 多预扣的部分 (≥ 0)
}

// ReleaseEvent — Release 后发. 显式释放 (用户取消) 或过期清理.
// Reason: "user_cancel" | "expired" | "abnormal".
type ReleaseEvent struct {
	Common
	HoldID uuid.UUID `json:"hold_id"`
	Amount int64     `json:"amount"`
	Reason string    `json:"reason,omitempty"`
}

// SubscriptionEvent — 订阅 lifecycle. EventType: created/updated/deleted/
// trialing/past_due/canceled/expired/invoice.payment_succeeded/invoice.
// payment_failed.
type SubscriptionEvent struct {
	Common
	SubscriptionID uuid.UUID `json:"subscription_id"`
	EventType      string    `json:"event_type"`
	PlanCode       string    `json:"plan_code"`
	OldPlanCode    string    `json:"old_plan_code,omitempty"`
	AmountCents    int64     `json:"amount_cents,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	Source         string    `json:"source,omitempty"` // stripe / wechat / alipay / iap
}

// ─── Publisher interface ───────────────────────────────────────────

// Publisher — credits / billing / hold 模块依赖此接口. 测试用 Mock,
// 生产用 NATSPublisher.
type Publisher interface {
	PublishConsume(ctx context.Context, e ConsumeEvent) error
	PublishRefund(ctx context.Context, e RefundEvent) error
	PublishHold(ctx context.Context, e HoldEvent) error
	PublishSettle(ctx context.Context, e SettleEvent) error
	PublishRelease(ctx context.Context, e ReleaseEvent) error
	PublishSubscription(ctx context.Context, e SubscriptionEvent) error
}

// NATSPublisher — 真 publisher, 写到 JetStream.
//
// publishOK / publishFail 走 OTel 计数 — caller 在 metrics 包初始化时
// 注入. 这里只持有最小依赖 (bus + env + logger).
type NATSPublisher struct {
	js     bus.JetStream
	env    string
	logger *slog.Logger
}

// NewNATSPublisher — main.go 调一次.
func NewNATSPublisher(js bus.JetStream, env string, logger *slog.Logger) *NATSPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &NATSPublisher{js: js, env: env, logger: logger}
}

// publish 是 6 个 Publish* 共用的入口. event 是任意可 marshal 的 struct;
// kind 决定 subject. 失败时 log+return; caller 不要因此回滚 DB.
func (p *NATSPublisher) publish(ctx context.Context, kind Kind, event any) error {
	if p == nil || p.js == nil {
		return errors.New("events: NATSPublisher not initialized")
	}
	subj := SubjectPrefix(p.env) + "." + string(kind)
	if err := p.js.Publish(ctx, subj, event); err != nil {
		p.logger.Warn("billing event publish failed",
			"kind", kind, "subject", subj, "err", err.Error())
		return fmt.Errorf("events: publish %s: %w", kind, err)
	}
	return nil
}

func (p *NATSPublisher) PublishConsume(ctx context.Context, e ConsumeEvent) error {
	e.Common = ensureCommon(e.Common, KindConsume, p.env)
	return p.publish(ctx, KindConsume, e)
}
func (p *NATSPublisher) PublishRefund(ctx context.Context, e RefundEvent) error {
	e.Common = ensureCommon(e.Common, KindRefund, p.env)
	return p.publish(ctx, KindRefund, e)
}
func (p *NATSPublisher) PublishHold(ctx context.Context, e HoldEvent) error {
	e.Common = ensureCommon(e.Common, KindHold, p.env)
	return p.publish(ctx, KindHold, e)
}
func (p *NATSPublisher) PublishSettle(ctx context.Context, e SettleEvent) error {
	e.Common = ensureCommon(e.Common, KindSettle, p.env)
	return p.publish(ctx, KindSettle, e)
}
func (p *NATSPublisher) PublishRelease(ctx context.Context, e ReleaseEvent) error {
	e.Common = ensureCommon(e.Common, KindRelease, p.env)
	return p.publish(ctx, KindRelease, e)
}
func (p *NATSPublisher) PublishSubscription(ctx context.Context, e SubscriptionEvent) error {
	e.Common = ensureCommon(e.Common, KindSubscription, p.env)
	return p.publish(ctx, KindSubscription, e)
}

// ensureCommon — caller 不强制填 EventID/Kind/Env/OccurredAt; publisher 兜底.
func ensureCommon(c Common, k Kind, env string) Common {
	if c.EventID == uuid.Nil {
		c.EventID = uuid.New()
	}
	if c.Kind == "" {
		c.Kind = k
	}
	if c.Env == "" {
		c.Env = env
	}
	if c.OccurredAt.IsZero() {
		c.OccurredAt = time.Now().UTC()
	}
	return c
}

// ─── No-op publisher ────────────────────────────────────────────────

// NoopPublisher 满足 Publisher 接口但什么都不做. 单测 / 本地 dev 无 NATS
// 时使用. credits 包默认用 Noop, main.go 接 NATS 后再注入真 publisher.
type NoopPublisher struct{}

func (NoopPublisher) PublishConsume(context.Context, ConsumeEvent) error           { return nil }
func (NoopPublisher) PublishRefund(context.Context, RefundEvent) error             { return nil }
func (NoopPublisher) PublishHold(context.Context, HoldEvent) error                 { return nil }
func (NoopPublisher) PublishSettle(context.Context, SettleEvent) error             { return nil }
func (NoopPublisher) PublishRelease(context.Context, ReleaseEvent) error           { return nil }
func (NoopPublisher) PublishSubscription(context.Context, SubscriptionEvent) error { return nil }
