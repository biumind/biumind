// Tests for SubscriptionsRepo + state machine. DB-backed; skip when
// DATABASE_URL not set.
//
// 12 tests, 覆盖 W2-4 dev-plan 验收标准:
//   状态机表 5 项:
//     trialing → active   (activate happy path)
//     active → past_due
//     past_due → active   (recovered)
//     active → canceled
//     canceled → expired
//   状态机非法转换 2 项:
//     expired → 任何状态  (终态)
//     trialing → past_due (状态机表无)
//   升降级 2 项:
//     升级 (sort_order 变大)
//     降级 (sort_order 变小)
//   读 + 唯一性 3 项:
//     GetActiveByUser
//     GetByStripeSubID
//     unique active per user (重复 Create → ErrAlreadyActive)

package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestSub(t *testing.T, r *SubscriptionsRepo, userID, planID uuid.UUID) *Subscription {
	t.Helper()
	now := time.Now().UTC()
	s, err := r.Create(context.Background(), CreateInput{
		UserID:               userID,
		PlanID:               planID,
		Status:               SubStatusTrialing,
		CurrentPeriodStart:   now,
		CurrentPeriodEnd:     now.Add(7 * 24 * time.Hour),
		BillingCycle:         "monthly",
		StripeCustomerID:     "cus_test_" + uuid.NewString()[:8],
		StripeSubscriptionID: "sub_test_" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE id=$1", s.ID)
	})
	return s
}

func planIDs(t *testing.T, p *PlansRepo) (free, pro, team uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for _, code := range []Plan{PlanFree, PlanPro, PlanTeam} {
		p, err := p.Get(ctx, code)
		if err != nil {
			t.Fatalf("get plan %q: %v", code, err)
		}
		switch code {
		case PlanFree:
			free = p.ID
		case PlanPro:
			pro = p.ID
		case PlanTeam:
			team = p.ID
		}
	}
	return
}

// ─── happy path 状态转换 ──────────────────────────────

func TestSubTransitionActivate(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()

	sub := newTestSub(t, r, uid, pro)
	// trialing → active
	updated, err := r.Transition(context.Background(), sub, SubStatusActive, "trial ended", "evt_test_1")
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if updated.Status != SubStatusActive {
		t.Fatalf("status = %s, want active", updated.Status)
	}
	// 审计事件应该写入了 'activated'
	events, err := r.ListEvents(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	hasActivated := false
	for _, e := range events {
		if e.EventType == "activated" {
			hasActivated = true
			break
		}
	}
	if !hasActivated {
		t.Fatalf("no 'activated' event, got: %+v", events)
	}
}

func TestSubTransitionPastDue(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")

	updated, err := r.Transition(context.Background(), sub, SubStatusPastDue, "invoice failed", "")
	if err != nil {
		t.Fatalf("transition past_due: %v", err)
	}
	if updated.Status != SubStatusPastDue {
		t.Fatalf("status = %s, want past_due", updated.Status)
	}
}

func TestSubTransitionRecovered(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")
	sub, _ = r.Transition(context.Background(), sub, SubStatusPastDue, "", "")

	updated, err := r.Transition(context.Background(), sub, SubStatusActive, "payment retry succeeded", "")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if updated.Status != SubStatusActive {
		t.Fatalf("recovered status = %s, want active", updated.Status)
	}
	events, _ := r.ListEvents(context.Background(), sub.ID)
	hasRecovered := false
	for _, e := range events {
		if e.EventType == "recovered" {
			hasRecovered = true
			break
		}
	}
	if !hasRecovered {
		t.Fatal("no 'recovered' event")
	}
}

func TestSubTransitionCanceled(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")

	updated, err := r.Transition(context.Background(), sub, SubStatusCanceled, "user canceled", "")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if updated.Status != SubStatusCanceled {
		t.Fatalf("status = %s, want canceled", updated.Status)
	}
	if updated.CanceledAt == nil {
		t.Fatal("canceled_at should be set")
	}
}

func TestSubTransitionExpired(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")
	sub, _ = r.Transition(context.Background(), sub, SubStatusCanceled, "", "")

	updated, err := r.Transition(context.Background(), sub, SubStatusExpired, "period end", "")
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if updated.Status != SubStatusExpired {
		t.Fatalf("status = %s, want expired", updated.Status)
	}
	if updated.ExpiredAt == nil {
		t.Fatal("expired_at should be set")
	}
}

// ─── 非法转换 ───────────────────────────────────────

func TestSubTransitionFromExpired(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")
	sub, _ = r.Transition(context.Background(), sub, SubStatusCanceled, "", "")
	sub, _ = r.Transition(context.Background(), sub, SubStatusExpired, "", "")

	for _, to := range []SubStatus{SubStatusActive, SubStatusTrialing, SubStatusPastDue, SubStatusCanceled} {
		_, err := r.Transition(context.Background(), sub, to, "", "")
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("expired → %s should error, got %v", to, err)
		}
	}
}

func TestSubTransitionTrialingToPastDue(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)

	_, err := r.Transition(context.Background(), sub, SubStatusPastDue, "", "")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("trialing → past_due should error, got %v", err)
	}
}

// ─── 升降级 ────────────────────────────────────────

func TestSubChangePlanUpgrade(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, team := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")

	// pro → team (sort_order 1 → 2, 升级)
	updated, err := r.ChangePlan(context.Background(), sub, team, plans, "")
	if err != nil {
		t.Fatalf("change plan: %v", err)
	}
	if updated.PlanID != team {
		t.Fatalf("plan_id = %v, want %v", updated.PlanID, team)
	}
	events, _ := r.ListEvents(context.Background(), sub.ID)
	hasUpgrade := false
	for _, e := range events {
		if e.EventType == "upgraded" {
			hasUpgrade = true
			break
		}
	}
	if !hasUpgrade {
		t.Fatalf("no 'upgraded' event, got: %+v", events)
	}
}

func TestSubChangePlanDowngrade(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	free, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)
	sub, _ = r.Transition(context.Background(), sub, SubStatusActive, "", "")

	// pro → free (sort_order 1 → 0, 降级)
	updated, err := r.ChangePlan(context.Background(), sub, free, plans, "")
	if err != nil {
		t.Fatalf("change plan: %v", err)
	}
	if updated.PlanID != free {
		t.Fatal("plan not changed to free")
	}
	events, _ := r.ListEvents(context.Background(), sub.ID)
	hasDown := false
	for _, e := range events {
		if e.EventType == "downgraded" {
			hasDown = true
			break
		}
	}
	if !hasDown {
		t.Fatal("no 'downgraded' event")
	}
}

// ─── 读路径 + 唯一性 ─────────────────────────────────

func TestSubGetActiveByUser(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()

	// 用户最初没订阅
	_, err := r.GetActiveByUser(context.Background(), uid)
	if !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("expected ErrSubscriptionNotFound, got %v", err)
	}

	sub := newTestSub(t, r, uid, pro)
	got, err := r.GetActiveByUser(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetActiveByUser: %v", err)
	}
	if got.ID != sub.ID {
		t.Fatal("returned wrong sub")
	}
}

func TestSubGetByStripeSubID(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	sub := newTestSub(t, r, uid, pro)

	got, err := r.GetByStripeSubID(context.Background(), sub.StripeSubscriptionID)
	if err != nil {
		t.Fatalf("by stripe id: %v", err)
	}
	if got.ID != sub.ID {
		t.Fatalf("wrong sub")
	}

	_, err = r.GetByStripeSubID(context.Background(), "sub_does_not_exist")
	if !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestSubUniqueActivePerUser(t *testing.T) {
	pool := plansDB(t)
	r := NewSubscriptionsRepo(pool)
	plans := NewPlansRepo(pool)
	_, pro, _ := planIDs(t, plans)
	uid := uuid.New()
	_ = newTestSub(t, r, uid, pro)

	// 第二次 Create 同 user 应被 partial unique index 挡住
	now := time.Now().UTC()
	_, err := r.Create(context.Background(), CreateInput{
		UserID:               uid,
		PlanID:               pro,
		Status:               SubStatusTrialing,
		CurrentPeriodStart:   now,
		CurrentPeriodEnd:     now.Add(7 * 24 * time.Hour),
		BillingCycle:         "monthly",
		StripeSubscriptionID: "sub_dup_" + uuid.NewString()[:8],
	})
	if !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("expected ErrAlreadyActive, got %v", err)
	}
}
