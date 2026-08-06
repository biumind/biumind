package credits

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/services/identity/internal/events"
)

// countingPublisher — 测试用 publisher, 计每类事件触发次数. 验证
// W3-3 集成: Consume / Refund / Hold / Settle / Release / ReapExpired
// 都正确发出对应事件.
type countingPublisher struct {
	mu                                                sync.Mutex
	consumes, refunds, holds, settles, releases, subs int
	lastConsume                                       events.ConsumeEvent
	lastRelease                                       events.ReleaseEvent
}

func (c *countingPublisher) PublishConsume(_ context.Context, e events.ConsumeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consumes++
	c.lastConsume = e
	return nil
}
func (c *countingPublisher) PublishRefund(_ context.Context, _ events.RefundEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refunds++
	return nil
}
func (c *countingPublisher) PublishHold(_ context.Context, _ events.HoldEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holds++
	return nil
}
func (c *countingPublisher) PublishSettle(_ context.Context, _ events.SettleEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settles++
	return nil
}
func (c *countingPublisher) PublishRelease(_ context.Context, e events.ReleaseEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases++
	c.lastRelease = e
	return nil
}
func (c *countingPublisher) PublishSubscription(_ context.Context, _ events.SubscriptionEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs++
	return nil
}

// TestPublisherIntegration_ConsumeRefund 验证 Consume + Refund 各发出一条事件.
func TestPublisherIntegration_ConsumeRefund(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	pub := &countingPublisher{}
	svc.SetPublisher(pub)

	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent,
		Source: SourceRecharge, Remark: "test",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	log, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 200,
		RefType: RefChatMessage, RefID: "m-1",
		IdempotencyKey: "consume-1",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if pub.consumes != 1 {
		t.Errorf("consumes=%d want 1", pub.consumes)
	}
	if pub.lastConsume.Amount != 200 || pub.lastConsume.LogID != log.ID {
		t.Errorf("consume payload: %+v", pub.lastConsume)
	}

	if _, _, err := svc.Refund(ctx, RefundArgs{
		OriginalLogID: log.ID, Amount: 50,
		IdempotencyKey: "refund-1",
	}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if pub.refunds != 1 {
		t.Errorf("refunds=%d want 1", pub.refunds)
	}
}

// TestPublisherIntegration_HoldSettleRelease 验证 Hold + Settle + Release.
func TestPublisherIntegration_HoldSettleRelease(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	pub := &countingPublisher{}
	svc.SetPublisher(pub)

	uid := newUser()
	resetUser(t, pool, uid)
	resetHolds(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent,
		Source: SourceRecharge, Remark: "test",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// Hold 1: settle path
	h1, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 300,
		RefType: RefChatMessage, RefID: "m-h1",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, _, _, err := svc.Settle(ctx, SettleArgs{
		HoldID: h1.ID, ActualAmount: 200,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// Hold 2: release path
	h2, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100,
		RefType: RefChatMessage, RefID: "m-h2",
	})
	if err != nil {
		t.Fatalf("hold2: %v", err)
	}
	if _, _, err := svc.Release(ctx, h2.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	if pub.holds != 2 {
		t.Errorf("holds=%d want 2", pub.holds)
	}
	if pub.settles != 1 {
		t.Errorf("settles=%d want 1", pub.settles)
	}
	if pub.releases != 1 {
		t.Errorf("releases=%d want 1", pub.releases)
	}
	if pub.lastRelease.Reason != "user_cancel" {
		t.Errorf("release reason=%q want user_cancel", pub.lastRelease.Reason)
	}
}

// TestPublisherIntegration_ReapEmitsExpired — ReapExpired 释放 expired
// hold 时应发 release 事件 reason=expired.
func TestPublisherIntegration_ReapEmitsExpired(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	pub := &countingPublisher{}
	svc.SetPublisher(pub)

	uid := newUser()
	resetUser(t, pool, uid)
	resetHolds(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent,
		Source: SourceRecharge, Remark: "test",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// 创建 hold 然后强制将 expires_at 改成过去, 模拟过期.
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100,
		RefType: RefChatMessage, RefID: "m-reap",
		TTL: 1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE identity.credit_holds SET expires_at = now() - interval '1 minute' WHERE id = $1`,
		h.ID); err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	pub.holds = 0 // 重置, 只数 reap 触发的 release
	pub.releases = 0
	if n, err := svc.ReapExpired(ctx, 10); err != nil {
		t.Fatalf("reap: %v", err)
	} else if n != 1 {
		t.Errorf("reap processed=%d want 1", n)
	}
	if pub.releases != 1 {
		t.Errorf("releases=%d want 1", pub.releases)
	}
	if pub.lastRelease.Reason != "expired" {
		t.Errorf("reap reason=%q want expired", pub.lastRelease.Reason)
	}
}

// 验证 SetPublisher(nil) 不 panic, 仍走 NoopPublisher.
func TestSetPublisherNil(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	svc.SetPublisher(nil)

	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	_, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge,
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
}
