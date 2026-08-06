// W2-7 webhook 升级测试: 6 个新 event 路径验证 billing.subscriptions
// 状态机被正确驱动 + subscription_events 写入审计.
//
// 走真 DB (DATABASE_URL skip if unset). fakeStore 仍用于 SetUserPlan 旁路.

package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// w2WebhookEnv 构造一个完整 Server, Plans + Subscriptions 接 DB.
// fakeStore 仍捕获 SetUserPlan call.
type w2WebhookEnv struct {
	server     *Server
	store      *fakeStore
	plansRepo  *PlansRepo
	subsRepo   *SubscriptionsRepo
	pool       *pgxpool.Pool
	httpServer *httptest.Server
	secret     string
}

func newW2WebhookEnv(t *testing.T) *w2WebhookEnv {
	t.Helper()
	pool := plansDB(t)
	store := &fakeStore{}
	secret := "whsec_w2test_" + uuid.NewString()[:8]
	plansRepo := NewPlansRepo(pool)
	subsRepo := NewSubscriptionsRepo(pool)

	srv := New(secret, map[string]Plan{
		"price_pro":  PlanPro,
		"price_team": PlanTeam,
	}, store, slog.Default())
	srv.Plans = plansRepo
	srv.Subscriptions = subsRepo

	mux := http.NewServeMux()
	srv.Mount(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	return &w2WebhookEnv{
		server: srv, store: store,
		plansRepo: plansRepo, subsRepo: subsRepo,
		pool: pool, httpServer: httpSrv, secret: secret,
	}
}

// w2SignAndPost — 构造 valid Stripe-Signature header + POST.
func (e *w2WebhookEnv) post(t *testing.T, body []byte) int {
	t.Helper()
	ts := time.Now().UTC().Unix()
	mac := hmacOf(e.secret, fmt.Sprintf("%d.%s", ts, body))
	header := fmt.Sprintf("t=%d,v1=%s", ts, mac)

	req, _ := http.NewRequest(http.MethodPost,
		e.httpServer.URL+"/v1/billing/webhook",
		strings.NewReader(string(body)))
	req.Header.Set("Stripe-Signature", header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

func hmacOf(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// ─── 6 个 webhook event 路径测试 ─────────────────────

// 1. customer.subscription.created → billing.subscriptions row + 'created' event
func TestW2WebhookSubscriptionCreated(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2test_" + uuid.NewString()[:8]

	body := buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2test")

	if status := e.post(t, body); status != 200 {
		t.Fatalf("status = %d", status)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	// 验证 billing.subscriptions 行存在
	sub, err := e.subsRepo.GetByStripeSubID(context.Background(), stripeSubID)
	if err != nil {
		t.Fatalf("GetByStripeSubID: %v", err)
	}
	if sub.UserID != uid {
		t.Fatal("user_id mismatch")
	}
	if sub.Status != SubStatusActive {
		t.Fatalf("status = %s, want active (Stripe status was active)", sub.Status)
	}

	// 'created' event 应该在 subscription_events
	events, _ := e.subsRepo.ListEvents(context.Background(), sub.ID)
	if len(events) == 0 || events[len(events)-1].EventType != "created" {
		t.Fatalf("expected 'created' audit event, got: %+v", events)
	}
}

// 2. customer.subscription.updated, plan 变化 → upgraded event
func TestW2WebhookSubscriptionUpgrade(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2up_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	// 先 create with pro
	bodyCreated := buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2up")
	e.post(t, bodyCreated)

	// 然后 update to team
	bodyUpgrade := buildSubscriptionEvent("customer.subscription.updated", stripeSubID, "active",
		"price_team", uid.String(), "cus_w2up")
	if status := e.post(t, bodyUpgrade); status != 200 {
		t.Fatalf("status = %d", status)
	}

	// 验证 plan_id 是 team
	sub, _ := e.subsRepo.GetByStripeSubID(context.Background(), stripeSubID)
	teamPlan, _ := e.plansRepo.Get(context.Background(), PlanTeam)
	if sub.PlanID != teamPlan.ID {
		t.Fatalf("plan not changed to team")
	}

	// upgraded event 存在
	events, _ := e.subsRepo.ListEvents(context.Background(), sub.ID)
	hasUpgrade := false
	for _, ev := range events {
		if ev.EventType == "upgraded" {
			hasUpgrade = true
		}
	}
	if !hasUpgrade {
		t.Fatalf("no 'upgraded' event in: %+v", events)
	}
}

// 3. customer.subscription.deleted → canceled
func TestW2WebhookSubscriptionDeleted(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2del_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2del"))

	if status := e.post(t, buildSubscriptionEvent("customer.subscription.deleted", stripeSubID, "canceled",
		"price_pro", uid.String(), "cus_w2del")); status != 200 {
		t.Fatalf("status = %d", status)
	}

	sub, _ := e.subsRepo.GetByStripeSubID(context.Background(), stripeSubID)
	if sub.Status != SubStatusCanceled {
		t.Fatalf("status = %s, want canceled", sub.Status)
	}
	if sub.CanceledAt == nil {
		t.Fatal("canceled_at should be set")
	}
}

// 4. invoice.payment_failed → past_due
func TestW2WebhookInvoicePaymentFailed(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2fail_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2fail"))

	// invoice.payment_failed
	body := buildInvoiceEvent("invoice.payment_failed", stripeSubID, "subscription_cycle", false)
	if status := e.post(t, body); status != 200 {
		t.Fatalf("status = %d", status)
	}

	sub, _ := e.subsRepo.GetByStripeSubID(context.Background(), stripeSubID)
	if sub.Status != SubStatusPastDue {
		t.Fatalf("status = %s, want past_due", sub.Status)
	}
}

// 5. invoice.payment_succeeded recovers from past_due
func TestW2WebhookInvoicePaymentRecovered(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2rec_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	e.post(t, buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2rec"))
	e.post(t, buildInvoiceEvent("invoice.payment_failed", stripeSubID, "subscription_cycle", false))

	// 现在 past_due, 触发 recovery
	if status := e.post(t, buildInvoiceEvent("invoice.payment_succeeded", stripeSubID, "subscription_cycle", true)); status != 200 {
		t.Fatalf("status = %d", status)
	}

	sub, _ := e.subsRepo.GetByStripeSubID(context.Background(), stripeSubID)
	if sub.Status != SubStatusActive {
		t.Fatalf("status = %s, want active (recovered)", sub.Status)
	}
}

// 6. 重复 webhook (same stripe_event_id) 幂等
func TestW2WebhookIdempotency(t *testing.T) {
	e := newW2WebhookEnv(t)
	uid := uuid.New()
	stripeSubID := "sub_w2idem_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE stripe_subscription_id=$1", stripeSubID)
	})

	body := buildSubscriptionEvent("customer.subscription.created", stripeSubID, "active",
		"price_pro", uid.String(), "cus_w2idem")
	// 第一次
	e.post(t, body)
	// 第二次 (同 event id, Stripe 重投)
	if status := e.post(t, body); status != 200 {
		t.Fatalf("retry status = %d", status)
	}

	// 应该仍然只有一行 subscriptions
	subs, _ := e.subsRepo.ListByUser(context.Background(), uid)
	if len(subs) != 1 {
		t.Fatalf("expected 1 sub after duplicate webhook, got %d", len(subs))
	}
}

// ─── helpers ────────────────────────────────────────

func buildSubscriptionEvent(eventType, subID, status, priceID, userID, customer string) []byte {
	now := time.Now().UTC().Unix()
	canceled := int64(0)
	if status == "canceled" {
		canceled = now
	}
	body := map[string]any{
		"id":   "evt_" + uuid.NewString()[:12],
		"type": eventType,
		"data": map[string]any{
			"object": map[string]any{
				"id":                   subID,
				"customer":             customer,
				"status":               status,
				"current_period_start": now,
				"current_period_end":   now + 30*24*3600,
				"trial_end":            int64(0),
				"canceled_at":          canceled,
				"items": map[string]any{
					"data": []map[string]any{
						{"price": map[string]any{"id": priceID}},
					},
				},
				"metadata": map[string]any{
					"biumind_user_id": userID,
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func buildInvoiceEvent(eventType, subID, billingReason string, paid bool) []byte {
	body := map[string]any{
		"id":   "evt_" + uuid.NewString()[:12],
		"type": eventType,
		"data": map[string]any{
			"object": map[string]any{
				"id":             "in_" + uuid.NewString()[:12],
				"customer":       "cus_test",
				"subscription":   subID,
				"status":         "paid",
				"amount_paid":    1900,
				"currency":       "usd",
				"paid":           paid,
				"billing_reason": billingReason,
			},
		},
	}
	b, _ := json.Marshal(body)
	return b
}
