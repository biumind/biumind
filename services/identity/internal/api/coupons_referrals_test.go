// W6-9 endpoint 测试 — 6 cases.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/billing"
)

// ─── helpers ───────────────────────────────────

func wireCouponsReferrals(s *Server) {
	s.Coupons = billing.NewCouponRepo(s.Subscriptions.Pool())
	s.Referrals = billing.NewReferralRepo(s.Subscriptions.Pool())
}

func seedTestCoupon(t *testing.T, s *Server, code, kind string, value int64) {
	t.Helper()
	pool := s.Subscriptions.Pool()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing.coupons (code, kind, value, status, once_per_user)
		VALUES ($1, $2, $3, 'active', true)
		ON CONFLICT (code) DO NOTHING
	`, code, kind, value)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing.coupons WHERE code=$1`, code)
	})
}

func cleanRedemptions(t *testing.T, s *Server, uid uuid.UUID) {
	t.Helper()
	_, _ = s.Subscriptions.Pool().Exec(context.Background(),
		`DELETE FROM billing.coupon_redemptions WHERE user_id=$1`, uid)
}

func cleanReferralsByUser(t *testing.T, s *Server, uid uuid.UUID) {
	t.Helper()
	_, _ = s.Subscriptions.Pool().Exec(context.Background(),
		`DELETE FROM billing.referrals WHERE inviter_user_id=$1 OR invitee_user_id=$1`, uid)
}

// ─── coupons (3) ─────────────────────────────

// 1. redeem happy.
func TestCouponRedeem_Happy(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	uid := uuid.New()
	defer cleanRedemptions(t, s, uid)

	code := "EP_HAPPY_" + uuid.NewString()[:6]
	seedTestCoupon(t, s, code, "credits_grant", 100)

	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/coupons/redeem", tok, map[string]any{"code": code})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp redeemResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.CreditsGranted != 100 || resp.Kind != "credits_grant" {
		t.Fatalf("resp = %+v", resp)
	}
}

// 2. redeem unknown code → 404.
func TestCouponRedeem_NotFound(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	uid := uuid.New()
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/coupons/redeem", tok, map[string]any{
		"code": "NOSUCH_" + uuid.NewString()[:6],
	})
	if w.Code != 404 {
		t.Fatalf("status = %d", w.Code)
	}
}

// 3. redeem same code twice → 409.
func TestCouponRedeem_Duplicate(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	uid := uuid.New()
	defer cleanRedemptions(t, s, uid)

	code := "EP_DUP_" + uuid.NewString()[:6]
	seedTestCoupon(t, s, code, "credits_grant", 50)

	tok := plansToken(t, signer, uid)
	if w := postJSON(mux, "/v1/coupons/redeem", tok, map[string]any{"code": code}); w.Code != 200 {
		t.Fatalf("first: %d", w.Code)
	}
	w := postJSON(mux, "/v1/coupons/redeem", tok, map[string]any{"code": code})
	if w.Code != 409 {
		t.Fatalf("second status = %d want 409", w.Code)
	}
}

// ─── referrals (3) ─────────────────────────────

// 4. /invite 返自己邀请码 + stats.
func TestReferralInvite(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	uid := uuid.New()
	defer cleanReferralsByUser(t, s, uid)
	tok := plansToken(t, signer, uid)
	req := httptest.NewRequest(http.MethodPost, "/v1/referrals/invite", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp inviteResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.InviteCode) != 8 {
		t.Fatalf("code = %q", resp.InviteCode)
	}
}

// 5. /claim happy.
func TestReferralClaim_Happy(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	inviter := uuid.New()
	invitee := uuid.New()
	defer cleanReferralsByUser(t, s, inviter)
	defer cleanReferralsByUser(t, s, invitee)

	code := s.Referrals.GenerateInviteCode(inviter)
	tok := plansToken(t, signer, invitee)
	w := postJSON(mux, "/v1/referrals/claim", tok, map[string]any{
		"inviter_user_id": inviter.String(),
		"invite_code":     code,
		"device_fp":       "dev-x",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

// 6. /claim self → 400.
func TestReferralClaim_SelfForbidden(t *testing.T) {
	s, mux, signer := newPlansTestServer(t)
	wireCouponsReferrals(s)
	uid := uuid.New()
	defer cleanReferralsByUser(t, s, uid)
	code := s.Referrals.GenerateInviteCode(uid)
	tok := plansToken(t, signer, uid)
	w := postJSON(mux, "/v1/referrals/claim", tok, map[string]any{
		"inviter_user_id": uid.String(),
		"invite_code":     code,
	})
	if w.Code != 400 {
		t.Fatalf("status = %d", w.Code)
	}
}
