// W4-5 webhook hook 测试 — invoice.payment_succeeded 触发月度积分.
//
// 不依赖 cron 包 (避免循环导入); 用 fake hook 捕获调用即可.

package billing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRenewedHook 捕获 OnSubscriptionRenewed 调用.
type renewedCall struct {
	UserID    string
	PlanCode  string
	InvoiceID string
}

type fakeRenewedHook struct {
	mu    sync.Mutex
	calls []renewedCall
}

func (f *fakeRenewedHook) hook(ctx context.Context, userID, planCode string, now time.Time, invoiceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, renewedCall{userID, planCode, invoiceID})
}

// 1. subscription_create — 触发 hook.
func TestW4Webhook_SubscriptionCreateTriggersHook(t *testing.T) {
	e := newW2WebhookEnv(t)
	hook := &fakeRenewedHook{}
	e.server.OnSubscriptionRenewed = hook.hook

	uid := uuid.New()
	stripeSubID := "sub_w4create_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w4create"))

	// invoice.payment_succeeded with billing_reason=subscription_create
	if status := e.post(t, buildInvoiceEvent("invoice.payment_succeeded", stripeSubID, "subscription_create", true)); status != 200 {
		t.Fatalf("status = %d", status)
	}

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.calls) != 1 {
		t.Fatalf("hook fired %d times, want 1: %+v", len(hook.calls), hook.calls)
	}
	if hook.calls[0].UserID != uid.String() {
		t.Fatalf("hook user_id = %s, want %s", hook.calls[0].UserID, uid)
	}
	if hook.calls[0].PlanCode != "pro" {
		t.Fatalf("hook plan = %s", hook.calls[0].PlanCode)
	}
}

// 2. subscription_cycle — 触发 hook.
func TestW4Webhook_SubscriptionCycleTriggersHook(t *testing.T) {
	e := newW2WebhookEnv(t)
	hook := &fakeRenewedHook{}
	e.server.OnSubscriptionRenewed = hook.hook

	uid := uuid.New()
	stripeSubID := "sub_w4cycle_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_team", uid.String(), "cus_w4cycle"))

	if status := e.post(t, buildInvoiceEvent("invoice.payment_succeeded", stripeSubID, "subscription_cycle", true)); status != 200 {
		t.Fatalf("status = %d", status)
	}

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.calls) != 1 {
		t.Fatalf("cycle hook fired %d, want 1", len(hook.calls))
	}
	if hook.calls[0].PlanCode != "team" {
		t.Fatalf("cycle plan = %s", hook.calls[0].PlanCode)
	}
}

// 3. payment_failed — 不触发 hook.
func TestW4Webhook_PaymentFailedNoHook(t *testing.T) {
	e := newW2WebhookEnv(t)
	hook := &fakeRenewedHook{}
	e.server.OnSubscriptionRenewed = hook.hook

	uid := uuid.New()
	stripeSubID := "sub_w4failhook_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w4failhook"))

	e.post(t, buildInvoiceEvent("invoice.payment_failed", stripeSubID, "subscription_cycle", false))

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.calls) != 0 {
		t.Fatalf("hook should not fire for payment_failed: %+v", hook.calls)
	}
}

// 4. recovery (past_due → active via payment_succeeded) — 触发 hook.
func TestW4Webhook_RecoveryTriggersHook(t *testing.T) {
	e := newW2WebhookEnv(t)
	hook := &fakeRenewedHook{}
	e.server.OnSubscriptionRenewed = hook.hook

	uid := uuid.New()
	stripeSubID := "sub_w4rec_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w4rec"))
	e.post(t, buildInvoiceEvent("invoice.payment_failed", stripeSubID, "subscription_cycle", false))

	// recovery via subscription_cycle payment_succeeded
	e.post(t, buildInvoiceEvent("invoice.payment_succeeded", stripeSubID, "subscription_cycle", true))

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.calls) != 1 {
		t.Fatalf("recovery should fire hook once: %+v", hook.calls)
	}
}
