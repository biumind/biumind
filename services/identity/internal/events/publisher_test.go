// publisher_test.go — W3-2 6 单测. 不依赖真实 NATS, 通过实现 bus.JetStream
// 接口的 fake 捕获 subject + JSON body 做断言.

package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	natsjs "github.com/nats-io/nats.go/jetstream"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

// captureJS — 实现 bus.JetStream, 把 Publish 的 (subject, body) 留下.
type captureJS struct {
	subject string
	body    []byte
}

func (c *captureJS) EnsureStream(context.Context, bus.StreamSpec) error { return nil }
func (c *captureJS) Publish(_ context.Context, subj string, payload any, _ ...bus.Header) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.subject = subj
	c.body = body
	return nil
}
func (c *captureJS) Subscribe(context.Context, bus.ConsumerSpec, bus.JSHandler) (bus.Subscription, error) {
	return nil, nil
}
func (c *captureJS) RawJetStream() natsjs.JetStream { return nil }

func newPub() (*NATSPublisher, *captureJS) {
	c := &captureJS{}
	return &NATSPublisher{js: c, env: "test"}, c
}

func (c *captureJS) into(t *testing.T, out any) {
	t.Helper()
	if err := json.Unmarshal(c.body, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// ─── tests ─────────────────────────────────────────────────────────

func TestPublishConsume(t *testing.T) {
	p, c := newPub()
	user := uuid.New()
	logID := uuid.New()
	if err := p.PublishConsume(context.Background(), ConsumeEvent{
		Common:    Common{UserID: user, IdempotencyKey: "task:abc"},
		LogID:     logID,
		Amount:    100,
		ModelCode: "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("PublishConsume: %v", err)
	}
	if c.subject != "biumind.test.billing.events.consume" {
		t.Errorf("subject=%q", c.subject)
	}
	var got ConsumeEvent
	c.into(t, &got)
	if got.EventID == uuid.Nil {
		t.Errorf("EventID not auto-filled")
	}
	if got.Kind != KindConsume {
		t.Errorf("Kind=%q want consume", got.Kind)
	}
	if got.Env != "test" {
		t.Errorf("Env=%q", got.Env)
	}
	if got.OccurredAt.IsZero() {
		t.Errorf("OccurredAt zero")
	}
	if got.UserID != user || got.LogID != logID || got.Amount != 100 {
		t.Errorf("payload mismatch: %+v", got)
	}
	if got.ModelCode != "claude-sonnet-4-6" {
		t.Errorf("ModelCode lost: %q", got.ModelCode)
	}
}

func TestPublishRefund(t *testing.T) {
	p, c := newPub()
	orig := uuid.New()
	if err := p.PublishRefund(context.Background(), RefundEvent{
		Common:        Common{UserID: uuid.New(), EventID: uuid.New()},
		LogID:         uuid.New(),
		RefundOfLogID: orig,
		Amount:        50,
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if c.subject != "biumind.test.billing.events.refund" {
		t.Errorf("subject=%q", c.subject)
	}
	var got RefundEvent
	c.into(t, &got)
	if got.RefundOfLogID != orig || got.Amount != 50 || got.Kind != KindRefund {
		t.Errorf("bad: %+v", got)
	}
}

func TestPublishHold(t *testing.T) {
	p, c := newPub()
	exp := time.Now().Add(5 * time.Minute)
	if err := p.PublishHold(context.Background(), HoldEvent{
		Common:    Common{UserID: uuid.New()},
		HoldID:    uuid.New(),
		Amount:    200,
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if c.subject != "biumind.test.billing.events.hold" {
		t.Errorf("subject=%q", c.subject)
	}
	var got HoldEvent
	c.into(t, &got)
	if got.Amount != 200 || got.Kind != KindHold {
		t.Errorf("bad: %+v", got)
	}
	// JSON 时间 round-trip 误差 < 1s 算 OK.
	d := got.ExpiresAt.Sub(exp)
	if d < 0 {
		d = -d
	}
	if d > time.Second {
		t.Errorf("ExpiresAt drift: got=%v want=%v", got.ExpiresAt, exp)
	}
}

func TestPublishSettle(t *testing.T) {
	p, c := newPub()
	if err := p.PublishSettle(context.Background(), SettleEvent{
		Common:    Common{UserID: uuid.New()},
		HoldID:    uuid.New(),
		LogID:     uuid.New(),
		Actual:    150,
		HoldDelta: 50,
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if c.subject != "biumind.test.billing.events.settle" {
		t.Errorf("subject=%q", c.subject)
	}
	var got SettleEvent
	c.into(t, &got)
	if got.Actual != 150 || got.HoldDelta != 50 || got.Kind != KindSettle {
		t.Errorf("bad: %+v", got)
	}
}

func TestPublishRelease(t *testing.T) {
	p, c := newPub()
	if err := p.PublishRelease(context.Background(), ReleaseEvent{
		Common: Common{UserID: uuid.New()},
		HoldID: uuid.New(),
		Amount: 100,
		Reason: "user_cancel",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if c.subject != "biumind.test.billing.events.release" {
		t.Errorf("subject=%q", c.subject)
	}
	var got ReleaseEvent
	c.into(t, &got)
	if got.Reason != "user_cancel" || got.Amount != 100 || got.Kind != KindRelease {
		t.Errorf("bad: %+v", got)
	}
}

func TestPublishSubscription(t *testing.T) {
	p, c := newPub()
	subID := uuid.New()
	if err := p.PublishSubscription(context.Background(), SubscriptionEvent{
		Common:         Common{UserID: uuid.New()},
		SubscriptionID: subID,
		EventType:      "invoice.payment_succeeded",
		PlanCode:       "pro",
		OldPlanCode:    "free",
		AmountCents:    1999,
		Currency:       "USD",
		Source:         "stripe",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if c.subject != "biumind.test.billing.events.subscription" {
		t.Errorf("subject=%q", c.subject)
	}
	var got SubscriptionEvent
	c.into(t, &got)
	if got.SubscriptionID != subID || got.PlanCode != "pro" ||
		got.OldPlanCode != "free" || got.AmountCents != 1999 ||
		got.Source != "stripe" || got.Kind != KindSubscription {
		t.Errorf("bad: %+v", got)
	}
}

func TestNoopPublisher(t *testing.T) {
	var p Publisher = NoopPublisher{}
	ctx := context.Background()
	checks := []func() error{
		func() error { return p.PublishConsume(ctx, ConsumeEvent{}) },
		func() error { return p.PublishRefund(ctx, RefundEvent{}) },
		func() error { return p.PublishHold(ctx, HoldEvent{}) },
		func() error { return p.PublishSettle(ctx, SettleEvent{}) },
		func() error { return p.PublishRelease(ctx, ReleaseEvent{}) },
		func() error { return p.PublishSubscription(ctx, SubscriptionEvent{}) },
	}
	for i, fn := range checks {
		if err := fn(); err != nil {
			t.Errorf("noop %d returned err: %v", i, err)
		}
	}
}

func TestSubjectPrefix(t *testing.T) {
	if got := SubjectPrefix("dev"); got != "biumind.dev.billing.events" {
		t.Errorf("SubjectPrefix(dev)=%q", got)
	}
	if got := SubjectPrefix(""); got != "biumind.billing.events" {
		t.Errorf("SubjectPrefix(empty)=%q", got)
	}
}
