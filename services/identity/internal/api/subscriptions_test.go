// W5-4/5/7 endpoint 测试 + W5-9 webhook (合并到 webhook_w5_test.go).
//
// 涵盖:
//   POST /v1/subscriptions/checkout (6)
//   POST /v1/subscriptions/cancel   (4)
//   POST /v1/subscriptions/change_plan (already 6 cases via proration_test.go;
//        这里再补 endpoint 集成 1)
//   POST /v1/subscriptions/resume   (3)

package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/billing"
)

// genRSAKey — 测试用 2048 位 RSA, 返 (PKCS8 priv PEM, PKIX pub PEM).
// 跟 billing/wechat_test.go 的 genTestRSAKey 同款; 在 api 包里没法跨包调测试 helper.
func genRSAKey(t *testing.T) (string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(priv)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	pubBytes, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return privPEM, pubPEM
}

// ─── helpers ───────────────────────────────────────

func cleanupSubs(t *testing.T, s *Server, uid uuid.UUID) {
	t.Helper()
	pool := s.Subscriptions.Pool()
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM billing.subscriptions WHERE user_id=$1`, uid)
}

func cleanupOrders(t *testing.T, s *Server, uid uuid.UUID) {
	t.Helper()
	pool := s.Subscriptions.Pool()
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM billing.payment_orders WHERE user_id=$1`, uid)
}

func postJSON(mux *http.ServeMux, path, token string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// 注入 disabled 状态的 wechat / alipay client (Enabled=false).
// 真集成测试在 wechat_test.go / alipay_test.go 里, 这里只验 endpoint 路径.
func wireFakeProviders(s *Server) {
	cfgW := billing.WechatConfig{Enabled: false}
	if c, err := billing.NewWechatClient(cfgW, slog.Default()); err == nil {
		s.Wechat = c
	}
	cfgA := billing.AlipayConfig{Enabled: false}
	if c, err := billing.NewAlipayClient(cfgA, slog.Default()); err == nil {
		s.Alipay = c
	}
}

// ─── checkout ──────────────────────────────────────

// 1. checkout — 缺 plan_code → 400
func TestCheckout_MissingPlan(t *testing.T) {
	_, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"provider": "wechat_native",
	})
	if w.Code != 400 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

// 2. checkout — plan 不存在 → 400
func TestCheckout_PlanNotFound(t *testing.T) {
	_, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"plan_code": "diamond",
		"provider":  "wechat_native",
	})
	if w.Code != 400 {
		t.Fatalf("status = %d", w.Code)
	}
}

// 3. checkout — wechat client 未配置 → 503
func TestCheckout_WechatNotConfigured(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireFakeProviders(s) // wechat enabled=false
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"plan_code": "pro",
		"provider":  "wechat_native",
	})
	if w.Code != 503 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

// 4. checkout — alipay 未配置 → 503
func TestCheckout_AlipayNotConfigured(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireFakeProviders(s)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"plan_code": "pro",
		"provider":  "alipay_pc",
	})
	if w.Code != 503 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

// 5. checkout — provider unknown → 400
func TestCheckout_UnknownProvider(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireFakeProviders(s)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"plan_code": "pro",
		"provider":  "venmo",
	})
	if w.Code != 400 {
		t.Fatalf("status = %d", w.Code)
	}
}

// 6. checkout — alipay_pc 真起 (注入有效 alipay client + mock gateway).
func TestCheckout_AlipayPC_Happy(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupOrders(t, s, uid)

	// 注入真 alipay client (key 是测试生成).
	privPEM, pubPEM := genRSAKey(t)
	cfg := billing.AlipayConfig{
		Enabled:            true,
		AppID:              "2021_test",
		PrivateKeyPEM:      privPEM,
		AlipayPublicKeyPEM: pubPEM,
		NotifyURL:          "https://example.com/webhook/alipay",
	}
	c, err := billing.NewAlipayClient(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	s.Alipay = c
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/checkout", tok, map[string]any{
		"plan_code": "pro",
		"provider":  "alipay_pc",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp checkoutResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.HasPrefix(resp.RedirectURL, "https://openapi.alipay.com/gateway.do?") {
		t.Fatalf("redirect_url = %s", resp.RedirectURL)
	}
	if resp.OutTradeNo == "" || resp.AmountCents <= 0 {
		t.Fatalf("missing fields: %+v", resp)
	}
}

// genTestRSAKey 复用 wechat_test.go (同 package).

// ─── cancel ───────────────────────────────────────

// helper — 给 user 起一条 active sub.
func seedActiveSub(t *testing.T, s *Server, uid uuid.UUID, planCode billing.Plan) *billing.Subscription {
	t.Helper()
	plan, err := s.Plans.Get(context.Background(), planCode)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sub, err := s.Subscriptions.Create(context.Background(), billing.CreateInput{
		UserID:               uid,
		PlanID:               plan.ID,
		Status:               billing.SubStatusActive,
		CurrentPeriodStart:   now,
		CurrentPeriodEnd:     now.Add(30 * 24 * time.Hour),
		BillingCycle:         "monthly",
		StripeSubscriptionID: "sub_test_" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// 7. cancel — 默认 period_end (status canceled).
func TestCancel_PeriodEnd(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	_ = seedActiveSub(t, s, uid, billing.PlanPro)

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/cancel", tok, map[string]any{})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "canceled" {
		t.Fatalf("status = %v want canceled", resp["status"])
	}
}

// 8. cancel — immediate=true (status expired)
func TestCancel_Immediate(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	_ = seedActiveSub(t, s, uid, billing.PlanPro)

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/cancel?immediate=true", tok, map[string]any{})
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "expired" {
		t.Fatalf("status = %v want expired", resp["status"])
	}
}

// 9. cancel — 没 active sub → 404
func TestCancel_NoActive(t *testing.T) {
	_, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/cancel", tok, map[string]any{})
	if w.Code != 404 {
		t.Fatalf("status = %d", w.Code)
	}
}

// 10. cancel — body 缺省也允许 (空 body).
func TestCancel_EmptyBody(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	_ = seedActiveSub(t, s, uid, billing.PlanPro)

	tok := plansToken(t, signer, uid)
	req := httptest.NewRequest(http.MethodPost, "/v1/subscriptions/cancel", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

// ─── change_plan ──────────────────────────────────

// 11. change_plan — 升级 (pro → team) 立即生效 + proration immediate.
func TestChangePlan_Upgrade(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	_ = seedActiveSub(t, s, uid, billing.PlanPro)

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/change_plan", tok, map[string]any{
		"plan_code": "team",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp changePlanResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Effective != "immediate" {
		t.Fatalf("effective = %s want immediate", resp.Effective)
	}
	if resp.Proration == nil {
		t.Fatal("upgrade should return proration")
	}
}

// 12. change_plan — 降级 (team → pro) 走 period_end + scheduled_at.
func TestChangePlan_Downgrade(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	_ = seedActiveSub(t, s, uid, billing.PlanTeam)

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/change_plan", tok, map[string]any{
		"plan_code": "pro",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp changePlanResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Effective != "period_end" {
		t.Fatalf("effective = %s want period_end", resp.Effective)
	}
	if resp.ScheduledAt == nil {
		t.Fatal("downgrade should return scheduled_at")
	}
}

// ─── resume ───────────────────────────────────────

// 13. resume — canceled sub 在 period_end 前能恢复.
func TestResume_Happy(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	sub := seedActiveSub(t, s, uid, billing.PlanPro)
	// 把它 transition 到 canceled
	_, err := s.Subscriptions.Transition(context.Background(), sub, billing.SubStatusCanceled, "test", "")
	if err != nil {
		t.Fatal(err)
	}

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/resume", tok, map[string]any{})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "active" {
		t.Fatalf("status = %v want active", resp["status"])
	}
}

// 14. resume — period_end 已过 (expired) 不允许恢复.
func TestResume_PeriodEnded(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	defer cleanupSubs(t, s, uid)
	plan, _ := s.Plans.Get(context.Background(), billing.PlanPro)
	now := time.Now().UTC()
	pool := s.Subscriptions.Pool()
	// 直接 INSERT 一条已 canceled + period 在过去的 sub.
	subID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing.subscriptions
		    (id, user_id, plan_id, status, current_period_start, current_period_end,
		     canceled_at, billing_cycle)
		VALUES ($1, $2, $3, 'canceled', $4, $5, $6, 'monthly')
	`, subID, uid, plan.ID, now.AddDate(0, -2, 0), now.AddDate(0, -1, 0), now.AddDate(0, -1, 0))
	if err != nil {
		t.Fatal(err)
	}

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/resume", tok, map[string]any{})
	if w.Code != 404 {
		t.Fatalf("status = %d want 404 (no resumable)", w.Code)
	}
}

// 15. resume — 没 canceled sub → 404.
func TestResume_NoCanceled(t *testing.T) {
	_, mux, signer := newPlansTestServer(t)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/subscriptions/resume", tok, map[string]any{})
	if w.Code != 404 {
		t.Fatalf("status = %d", w.Code)
	}
}
