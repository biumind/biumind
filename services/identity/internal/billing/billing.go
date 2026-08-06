// Package billing handles the Stripe webhook + plan→quota mapping.
//
// We use Stripe directly (not Lemon Squeezy / Paddle) because the
// webhook signature spec is simple, the Customer Portal is fully
// hosted, and the Go-side dependency footprint is just stdlib HMAC.
//
// Architecture:
//
//   1. Stripe webhook hits POST /v1/billing/webhook with X-Stripe-Signature.
//   2. We verify the signature against STRIPE_WEBHOOK_SECRET (HMAC-SHA256
//      over the raw body + timestamp).
//   3. On `customer.subscription.{created,updated,deleted}` we look up
//      the price ID, map it to a Plan, and persist the new plan against
//      the user (Store.SetUserPlan).
//   4. model-relay reads the plan via the existing virtual-key resolver path
//      and applies plan-specific quota ceilings.
//
// What this package does NOT do (out of scope for the MVP):
//
//   - Creating subscriptions / checkout sessions (front-end uses
//     Stripe.js directly).
//   - Refunds, proration, dunning — Stripe handles all of it.
//   - Invoice rendering — Stripe Customer Portal already does this.
//
// What it DOES expose: the webhook + a "create portal session" helper
// so the Flutter client can deep-link the user into Stripe's hosted
// account-management UI.

package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Plan is the canonical pricing tier name. Stripe price IDs map to
// these via PlanResolver. Adding a new plan is one PlanLimits entry +
// one PlanResolver mapping.
type Plan string

const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
	PlanTeam Plan = "team"
)

// PlanLimits — the quota ceilings each plan unlocks. model-relay reads these
// when sizing the per-user limiter on RPM / TPM. Sandbox uses the
// daily-create cap likewise.
type PlanLimits struct {
	HubRPM            int64
	HubTPM            int64
	SandboxDaily      int64
	SandboxConcurrent int
	MemoryQuota       int   // max stored memories per project
	BrainProjects     int   // max projects per user
}

// DefaultLimits is the baseline mapping we ship with. Override via
// SetLimits if a customer needs a custom plan (rare).
var DefaultLimits = map[Plan]PlanLimits{
	PlanFree: {
		HubRPM: 60, HubTPM: 50_000,
		SandboxDaily: 10, SandboxConcurrent: 1,
		MemoryQuota: 100, BrainProjects: 3,
	},
	PlanPro: {
		HubRPM: 600, HubTPM: 500_000,
		SandboxDaily: 100, SandboxConcurrent: 5,
		MemoryQuota: 5_000, BrainProjects: 50,
	},
	PlanTeam: {
		HubRPM: 6_000, HubTPM: 5_000_000,
		SandboxDaily: 1_000, SandboxConcurrent: 20,
		MemoryQuota: 100_000, BrainProjects: 1_000,
	},
}

// PlanStore is the contract billing needs against the user database.
// Implementing this lets us keep billing decoupled from
// services/identity/internal/store.
type PlanStore interface {
	// SetUserPlan persists the plan + Stripe customer/subscription
	// IDs against a user. Idempotent — webhook redelivery must not
	// produce duplicate state.
	SetUserPlan(ctx Context, userID, customerID, subscriptionID string, plan Plan) error
}

// Context is a thin alias so this file stays free of `context.Context`
// imports clutter in tests where stub stores don't need it.
type Context = interface{}

// Server is the HTTP handler bundle.
type Server struct {
	WebhookSecret string
	// PriceToPlan maps Stripe price IDs (price_xxx) to a Plan.
	// Configure via env: STRIPE_PRICE_PRO=price_xxx, STRIPE_PRICE_TEAM=...
	PriceToPlan map[string]Plan
	Store       PlanStore
	Logger      *slog.Logger
	// Now is overridable for tests.
	Now func() time.Time

	// W2-7 webhook 升级注入. 非 nil 时 webhook 不仅 SetUserPlan, 同步
	// 写 billing.subscriptions + subscription_events. nil 时退化为
	// 仅 SetUserPlan (W1 行为, 向下兼容).
	Plans         *PlansRepo
	Subscriptions *SubscriptionsRepo

	// W4-5: invoice.payment_succeeded 触发即时月度积分发放 + quota 重置
	// 的钩子. main.go 注入实现 (cron.GrantUserMonthly + cron.ResetUserQuota).
	// nil 时不触发 (兼容 W2 行为). 钩子失败只 log, 不影响 webhook 200.
	// invoiceID 作为额外审计字段; 实际幂等键由 cron 包内部用 (uid, period).
	OnSubscriptionRenewed func(ctx context.Context, userID, planCode string, now time.Time, invoiceID string)
}

func New(secret string, priceMap map[string]Plan, store PlanStore, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		WebhookSecret: secret, PriceToPlan: priceMap,
		Store: store, Logger: logger,
		Now: time.Now,
	}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/billing/webhook", s.handleWebhook)
}

// ─── Webhook ────────────────────────────────────────────

// stripeEvent — only the fields we need; keeps us decoupled from
// the upstream stripe-go SDK (which would balloon module size).
type stripeEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

type stripeSubscription struct {
	ID         string `json:"id"`
	Customer   string `json:"customer"`
	Status     string `json:"status"`
	Items      struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
	Metadata map[string]string `json:"metadata"`

	// W2-7: 周期与试用期 — Stripe Unix timestamp (秒)
	CurrentPeriodStart int64 `json:"current_period_start"`
	CurrentPeriodEnd   int64 `json:"current_period_end"`
	TrialEnd           int64 `json:"trial_end"`            // 0 = no trial
	CancelAt           int64 `json:"cancel_at"`            // 用户预约取消时点
	CanceledAt         int64 `json:"canceled_at"`          // 实际取消时点
}

// stripeInvoice — invoice.payment_{succeeded,failed} event 的 data.object.
type stripeInvoice struct {
	ID             string `json:"id"`
	Customer       string `json:"customer"`
	Subscription   string `json:"subscription"`            // sub_xxx
	Status         string `json:"status"`                  // paid | open | uncollectible | void
	AmountPaid     int64  `json:"amount_paid"`             // cents
	Currency       string `json:"currency"`
	Paid           bool   `json:"paid"`
	BillingReason  string `json:"billing_reason"`          // subscription_create | subscription_cycle | ...
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sigHeader := r.Header.Get("Stripe-Signature")
	if err := VerifyWebhookSignature(s.WebhookSecret, body, sigHeader, s.Now()); err != nil {
		s.Logger.Warn("stripe webhook signature reject",
			"err", err, "remote", r.RemoteAddr)
		http.Error(w, "signature: "+err.Error(), http.StatusBadRequest)
		return
	}

	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "json: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch ev.Type {
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		if err := s.applySubscription(r.Context(), ev); err != nil {
			s.Logger.Warn("stripe subscription apply",
				"err", err, "event", ev.Type, "id", ev.ID)
		}
	case "invoice.payment_succeeded",
		"invoice.payment_failed":
		// W2-7: 扣款成功/失败 → past_due / recovered 状态转换 + 写 audit.
		if err := s.applyInvoice(r.Context(), ev); err != nil {
			s.Logger.Warn("stripe invoice apply",
				"err", err, "event", ev.Type, "id", ev.ID)
		}
	default:
		// Unrecognised event types are not an error — Stripe sends
		// many event kinds and we ignore the ones we don't care
		// about. Log at debug to avoid log noise.
		s.Logger.Debug("stripe unhandled event", "type", ev.Type)
	}
	w.WriteHeader(http.StatusOK)
}

// applySubscription decodes the embedded subscription object,
// resolves price → plan, and writes through to the store + (W2-7) the
// billing.subscriptions table state machine.
//
// 旧路径 (Plans/Subscriptions nil): 仅 SetUserPlan 维护 users.plan denorm.
// 新路径: 同步写 billing.subscriptions + subscription_events, 然后
//         SetUserPlan 维护 denorm.
func (s *Server) applySubscription(ctx Context, ev stripeEvent) error {
	var sub stripeSubscription
	if err := json.Unmarshal(ev.Data.Object, &sub); err != nil {
		return fmt.Errorf("decode subscription: %w", err)
	}
	if len(sub.Items.Data) == 0 {
		return errors.New("subscription has no items")
	}
	priceID := sub.Items.Data[0].Price.ID
	plan, ok := s.PriceToPlan[priceID]
	if !ok {
		// Unknown price — fall back to free so the user isn't locked
		// out of the platform. Operator gets a warn.
		s.Logger.Warn("stripe unknown price id, defaulting to free",
			"price_id", priceID)
		plan = PlanFree
	}
	// `customer.subscription.deleted` → drop to free.
	if ev.Type == "customer.subscription.deleted" ||
		(ev.Type == "customer.subscription.updated" &&
			(sub.Status == "canceled" || sub.Status == "unpaid")) {
		plan = PlanFree
	}
	userID := sub.Metadata["biumind_user_id"]
	if userID == "" {
		return errors.New("subscription metadata missing biumind_user_id")
	}

	// W2-7: 同步写 billing.subscriptions (best-effort, 失败不阻塞 SetUserPlan).
	if s.Plans != nil && s.Subscriptions != nil {
		if err := s.syncSubscriptionRow(ctx, ev, &sub, plan); err != nil {
			s.Logger.Warn("billing.subscriptions sync failed (denorm continues)",
				"err", err, "stripe_sub_id", sub.ID, "event_type", ev.Type)
		}
	}

	return s.Store.SetUserPlan(ctx, userID, sub.Customer, sub.ID, plan)
}

// syncSubscriptionRow 维护 billing.subscriptions 行与 Stripe 事件一致.
// 五条路径:
//   created  → SubscriptionsRepo.Create (status=trialing/active 看 Stripe status)
//   updated, plan 变 → ChangePlan + 写 upgraded/downgraded event
//   updated, status='canceled' → Transition canceled (用户取消)
//   updated, status='past_due' → Transition past_due
//   updated, status='active' from past_due → Transition active (recovered)
//   deleted  → Transition canceled / expired (按 Stripe 来)
func (s *Server) syncSubscriptionRow(rawCtx Context, ev stripeEvent, sub *stripeSubscription, plan Plan) error {
	ctx, ok := rawCtx.(context.Context)
	if !ok {
		ctx = context.Background()
	}

	// 1. 看 billing.subscriptions 是否已有此 stripe_subscription_id 行.
	existing, err := s.Subscriptions.GetByStripeSubID(ctx, sub.ID)
	notFound := errors.Is(err, ErrSubscriptionNotFound)
	if err != nil && !notFound {
		return fmt.Errorf("lookup by stripe id: %w", err)
	}

	// 2. 解 plan code → plans.id
	planRow, err := s.Plans.Get(ctx, plan)
	if err != nil {
		return fmt.Errorf("plan code %q not in plans table: %w", plan, err)
	}

	stripePeriodStart := time.Unix(sub.CurrentPeriodStart, 0).UTC()
	stripePeriodEnd := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
	if sub.CurrentPeriodStart == 0 {
		stripePeriodStart = time.Now().UTC()
		stripePeriodEnd = stripePeriodStart.Add(30 * 24 * time.Hour)
	}
	var trialEnd *time.Time
	if sub.TrialEnd > 0 {
		t := time.Unix(sub.TrialEnd, 0).UTC()
		trialEnd = &t
	}

	// 3. 状态机处理
	switch {
	case notFound && ev.Type == "customer.subscription.created":
		// 新建. trialing 还是 active 看 Stripe status.
		initStatus := SubStatusTrialing
		if sub.Status == "active" {
			initStatus = SubStatusActive
		}
		userID, err := uuid.Parse(sub.Metadata["biumind_user_id"])
		if err != nil {
			return fmt.Errorf("bad user_id: %w", err)
		}
		_, err = s.Subscriptions.Create(ctx, CreateInput{
			UserID:               userID,
			PlanID:               planRow.ID,
			Status:               initStatus,
			CurrentPeriodStart:   stripePeriodStart,
			CurrentPeriodEnd:     stripePeriodEnd,
			TrialEndAt:           trialEnd,
			BillingCycle:         "monthly",
			StripeCustomerID:     sub.Customer,
			StripeSubscriptionID: sub.ID,
			Metadata:             json.RawMessage(`{"source":"stripe_webhook","event_id":"` + ev.ID + `"}`),
		})
		return err

	case notFound && ev.Type != "customer.subscription.created":
		// 没有现有行但是 update/delete 事件 — backfill 一行 (一致性兜底).
		// 真出现在 webhook 重排或 backfill 脚本未跑场景.
		userID, err := uuid.Parse(sub.Metadata["biumind_user_id"])
		if err != nil {
			return fmt.Errorf("bad user_id: %w", err)
		}
		init := SubStatusActive
		if sub.Status == "canceled" {
			init = SubStatusCanceled
		}
		_, err = s.Subscriptions.Create(ctx, CreateInput{
			UserID: userID, PlanID: planRow.ID, Status: init,
			CurrentPeriodStart: stripePeriodStart, CurrentPeriodEnd: stripePeriodEnd,
			TrialEndAt:         trialEnd,
			BillingCycle:       "monthly",
			StripeCustomerID:   sub.Customer,
			StripeSubscriptionID: sub.ID,
			Metadata: json.RawMessage(`{"source":"stripe_webhook_backfill","event_id":"` + ev.ID + `"}`),
		})
		return err

	default:
		// 已有行, 处理 update/delete:
		if ev.Type == "customer.subscription.deleted" || sub.Status == "canceled" {
			if existing.Status == SubStatusCanceled || existing.Status == SubStatusExpired {
				return nil // idempotent
			}
			_, err := s.Subscriptions.Transition(ctx, existing, SubStatusCanceled, "stripe canceled", ev.ID)
			return err
		}
		// past_due (扣款失败 retry 中)
		if sub.Status == "past_due" || sub.Status == "unpaid" {
			if existing.Status == SubStatusPastDue {
				return nil
			}
			_, err := s.Subscriptions.Transition(ctx, existing, SubStatusPastDue, "stripe past_due", ev.ID)
			return err
		}
		// active 恢复 (from past_due / trialing)
		if sub.Status == "active" && existing.Status != SubStatusActive {
			_, err := s.Subscriptions.Transition(ctx, existing, SubStatusActive, "stripe active", ev.ID)
			return err
		}
		// plan 变化 → upgrade/downgrade
		if existing.PlanID != planRow.ID && existing.Status == SubStatusActive {
			_, err := s.Subscriptions.ChangePlan(ctx, existing, planRow.ID, s.Plans, ev.ID)
			return err
		}
	}
	return nil
}

// applyInvoice — invoice.payment_{succeeded,failed} 事件处理.
func (s *Server) applyInvoice(rawCtx Context, ev stripeEvent) error {
	if s.Subscriptions == nil {
		return nil // W1 回退路径, 无 billing.subscriptions
	}
	var inv stripeInvoice
	if err := json.Unmarshal(ev.Data.Object, &inv); err != nil {
		return fmt.Errorf("decode invoice: %w", err)
	}
	if inv.Subscription == "" {
		// 单次充值或 setup, 不触发 subscription 状态变更
		return nil
	}
	ctx, ok := rawCtx.(context.Context)
	if !ok {
		ctx = context.Background()
	}
	existing, err := s.Subscriptions.GetByStripeSubID(ctx, inv.Subscription)
	if errors.Is(err, ErrSubscriptionNotFound) {
		// 没有对应订阅行 — webhook 乱序到达, 跳过 (subscription.created
		// 后会建); 不视为错误.
		return nil
	}
	if err != nil {
		return err
	}

	switch ev.Type {
	case "invoice.payment_failed":
		if existing.Status == SubStatusPastDue {
			return nil // idempotent
		}
		_, err := s.Subscriptions.Transition(ctx, existing, SubStatusPastDue, "invoice payment_failed", ev.ID)
		return err
	case "invoice.payment_succeeded":
		// 从 past_due 恢复
		if existing.Status == SubStatusPastDue {
			_, err := s.Subscriptions.Transition(ctx, existing, SubStatusActive, "invoice payment_succeeded", ev.ID)
			if err != nil {
				return err
			}
		} else {
			// 续费成功 — 记一条 'renewed' event (不变状态)
			if err := s.Subscriptions.appendEvent(ctx, eventInput{
				SubscriptionID: existing.ID,
				UserID:         existing.UserID,
				EventType:      "payment_succeeded",
				ToStatus:       string(existing.Status),
				StripeEventID:  ev.ID,
				Metadata:       json.RawMessage(`{"billing_reason":"` + inv.BillingReason + `","amount_paid":` + jsonInt(inv.AmountPaid) + `}`),
			}); err != nil {
				return err
			}
		}
		// W4-5: 仅 subscription_create / subscription_cycle 触发月度发放
		// (其他如 manual / subscription_threshold 不算续费).
		if s.OnSubscriptionRenewed != nil &&
			(inv.BillingReason == "subscription_create" || inv.BillingReason == "subscription_cycle") {
			plan, err := s.Plans.GetByID(ctx, existing.PlanID)
			if err == nil && plan.MonthlyCredits > 0 {
				s.OnSubscriptionRenewed(ctx, existing.UserID.String(), string(plan.Code), s.Now(), inv.ID)
			} else if err != nil {
				s.Logger.Warn("W4-5: plan lookup failed", "plan_id", existing.PlanID, "err", err)
			}
		}
		return nil
	}
	return nil
}

func jsonInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

// ─── Signature verification ─────────────────────────────

// VerifyWebhookSignature implements the Stripe-Signature spec:
//
//	t=<timestamp>,v1=<hex(hmacSHA256(secret, "<t>.<body>"))>
//
// We reject signatures older than 5 minutes to defeat replay attacks.
// Multiple v1 entries in the header (during key rotation) are
// accepted as long as ANY matches.
//
// Exposed so tests can use it without spinning the full Server.
func VerifyWebhookSignature(secret string, body []byte, header string, now time.Time) error {
	if secret == "" {
		return errors.New("webhook secret not configured")
	}
	if header == "" {
		return errors.New("missing Stripe-Signature header")
	}

	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return fmt.Errorf("bad timestamp: %w", err)
			}
			ts = n
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return errors.New("malformed signature header")
	}
	if abs(now.Unix()-ts) > 300 {
		return errors.New("timestamp outside tolerance window")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, body)
	want := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range sigs {
		if hmac.Equal([]byte(sig), []byte(want)) {
			return nil
		}
	}
	return errors.New("no matching v1 signature")
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ─── Helpers exposed for upstream callers ──────────────

// LimitsFor returns the canonical PlanLimits for a plan, falling back
// to free if the plan is unrecognised.
func LimitsFor(plan Plan) PlanLimits {
	if l, ok := DefaultLimits[plan]; ok {
		return l
	}
	return DefaultLimits[PlanFree]
}

// ─── Customer portal ────────────────────────────────────

// PortalLinker is the small surface of the Stripe API we need to
// create a portal session. Production wires this to a real Stripe
// HTTP client; tests inject a stub.
type PortalLinker interface {
	// PortalSessionURL takes the Stripe customer id + return URL
	// (where the user lands after closing the portal) and returns
	// the deep-link URL for the hosted account-management page.
	PortalSessionURL(customerID, returnURL string) (string, error)
}

// CustomerLookup resolves a BiuMind user id to its Stripe customer
// id (set by the webhook on subscription create). Returns "" when
// the user has never had a paid plan.
type CustomerLookup func(userID string) (customerID string, err error)

// HandlePortalSession is a ready-made HTTP handler. The auth layer
// must run before it so r.Context() carries claims.
//
//	POST /v1/billing/portal_session  body: {"return_url":"https://app/account"}
//	→ {"url":"https://billing.stripe.com/p/session/..."}
func HandlePortalSession(linker PortalLinker, lookup CustomerLookup,
	getUserID func(r *http.Request) string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := getUserID(r)
		if uid == "" {
			http.Error(w, "no user in context", http.StatusUnauthorized)
			return
		}
		var req struct {
			ReturnURL string `json:"return_url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req) // optional body
		if req.ReturnURL == "" {
			req.ReturnURL = "https://app.biumind.com/account"
		}
		cust, err := lookup(uid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cust == "" {
			http.Error(w, "no Stripe customer for this user — subscribe first",
				http.StatusBadRequest)
			return
		}
		url, err := linker.PortalSessionURL(cust, req.ReturnURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": url})
	}
}
