// Endpoint tests for W2-5/W2-6:
//   GET /v1/plans              — 公开 + 带 token 高亮
//   GET /v1/subscriptions/me   — 鉴权 + 无订阅返虚拟 free / 有订阅返完整视图
//
// 用 httptest + HS256 in-process signer (跟 plans_test.go in billing
// 同款本地 DB), 不依赖真 RS256 setup.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/store"
)

const (
	plansTestSecret   = "plans-test-secret-32-chars-padded-aaaaa"
	plansTestIssuer   = "https://identity.biumind.local"
	plansTestAudience = "biumind-api"
)

func plansEndpointDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping fail: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newPlansTestServer(t *testing.T) (*Server, *http.ServeMux, *bauth.Signer) {
	t.Helper()
	pool := plansEndpointDB(t)
	signer := bauth.NewSigner(plansTestSecret, plansTestIssuer, plansTestAudience, 15*time.Minute)
	verifier := bauth.NewVerifier(plansTestSecret, plansTestIssuer, plansTestAudience)

	s := &Server{
		Store:         store.New(pool),
		Signer:        signer,
		Verifier:      verifier,
		Plans:         billing.NewPlansRepo(pool),
		Subscriptions: billing.NewSubscriptionsRepo(pool),
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux, signer
}

func plansToken(t *testing.T, signer *bauth.Signer, uid uuid.UUID) string {
	t.Helper()
	tok, err := signer.Sign(&bauth.Claims{
		UserID: uid.String(),
		Plan:   "team",
		Roles:  []string{"admin"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// ─── /v1/plans ───────────────────────────────────────

func TestListPlansPublic(t *testing.T) {
	_, mux, _ := newPlansTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/plans", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var out struct {
		Plans []map[string]any `json:"plans"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total < 4 {
		t.Fatalf("total = %d, want ≥4", out.Total)
	}
	codes := []string{}
	for _, p := range out.Plans {
		codes = append(codes, p["code"].(string))
		// is_current 应缺失或 false (无 token)
		if cur, ok := p["is_current"].(bool); ok && cur {
			t.Errorf("plan %s marked is_current without token", p["code"])
		}
	}
	want := map[string]bool{"free": true, "pro": true, "team": true, "enterprise": true}
	for _, c := range codes {
		if !want[c] {
			t.Errorf("unexpected plan code %q", c)
		}
	}
}

func TestListPlansHighlightWhenSubscribed(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	pool := s.Subscriptions.Pool() // 添加 helper

	uid := uuid.New()
	plans := s.Plans
	pro, err := plans.Get(context.Background(), billing.PlanPro)
	if err != nil {
		t.Fatal(err)
	}

	// 给 user 创建一个 active pro 订阅
	now := time.Now().UTC()
	sub, err := s.Subscriptions.Create(context.Background(), billing.CreateInput{
		UserID:               uid,
		PlanID:               pro.ID,
		Status:               billing.SubStatusActive,
		CurrentPeriodStart:   now,
		CurrentPeriodEnd:     now.Add(30 * 24 * time.Hour),
		BillingCycle:         "monthly",
		StripeSubscriptionID: "sub_plansendpoint_" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE id=$1", sub.ID)
	})

	tok := plansToken(t, signer, uid)
	req := httptest.NewRequest(http.MethodGet, "/v1/plans", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var out struct {
		Plans []map[string]any `json:"plans"`
	}
	json.NewDecoder(w.Body).Decode(&out)
	for _, p := range out.Plans {
		if p["code"] == "pro" {
			if cur, ok := p["is_current"].(bool); !ok || !cur {
				t.Fatalf("pro plan should be is_current=true: %v", p)
			}
		} else if cur, ok := p["is_current"].(bool); ok && cur {
			t.Errorf("plan %s should not be is_current", p["code"])
		}
	}
}

// ─── /v1/subscriptions/me ───────────────────────────

func TestMySubscriptionUnauthorized(t *testing.T) {
	_, mux, _ := newPlansTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/subscriptions/me", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMySubscriptionNoSubReturnsFree(t *testing.T) {
	_, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)

	req := httptest.NewRequest(http.MethodGet, "/v1/subscriptions/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out subscriptionView
	json.NewDecoder(w.Body).Decode(&out)
	if out.Status != "free" {
		t.Fatalf("status = %q, want 'free'", out.Status)
	}
	if !out.IsActive {
		t.Fatal("free user should have is_active=true")
	}
	if out.Plan.Code != "free" {
		t.Fatalf("plan code = %q, want free", out.Plan.Code)
	}
	if out.Quota == nil {
		t.Fatal("quota should be populated (W4-8)")
	}
}

func TestMySubscriptionWithActiveSub(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	pool := s.Subscriptions.Pool()
	uid := uuid.New()
	tok := plansToken(t, signer, uid)

	team, err := s.Plans.Get(context.Background(), billing.PlanTeam)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sub, err := s.Subscriptions.Create(context.Background(), billing.CreateInput{
		UserID:               uid,
		PlanID:               team.ID,
		Status:               billing.SubStatusActive,
		CurrentPeriodStart:   now,
		CurrentPeriodEnd:     now.Add(30 * 24 * time.Hour),
		BillingCycle:         "yearly",
		StripeCustomerID:     "cus_test_" + uuid.NewString()[:8],
		StripeSubscriptionID: "sub_test_subme_" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM billing.subscriptions WHERE id=$1", sub.ID)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/subscriptions/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out subscriptionView
	json.NewDecoder(w.Body).Decode(&out)
	if out.Status != "active" {
		t.Fatalf("status = %q, want active", out.Status)
	}
	if out.Plan.Code != "team" {
		t.Fatalf("plan code = %q, want team", out.Plan.Code)
	}
	if out.BillingCycle != "yearly" {
		t.Fatalf("billing_cycle = %q, want yearly", out.BillingCycle)
	}
	if !out.IsActive {
		t.Fatal("active sub should have is_active=true")
	}
}
