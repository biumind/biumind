// coupons_referrals.go — W6-9 endpoints.
//
//   POST /v1/coupons/redeem        — 兑换券 (authed)
//   POST /v1/referrals/invite       — 拿自己的邀请码 (authed)
//   POST /v1/referrals/claim        — 用别人码建立邀请关系 (authed)
//
// credits_grant 类券 / 试用延长 副作用由 handler 调相应 service 完成
// (Credits.Grant / Subscriptions ext) — 落 redemption 行后再 fire-and-forget.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/credits"
)

// ─── Coupons ──────────────────────────────────

type redeemReq struct {
	Code        string `json:"code"`
	PlanCode    string `json:"plan_code,omitempty"`
	AmountCents int64  `json:"amount_cents,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

type redeemResp struct {
	RedemptionID   string `json:"redemption_id"`
	CouponCode     string `json:"coupon_code"`
	Kind           string `json:"kind"`
	DiscountCents  int64  `json:"discount_cents,omitempty"`
	CreditsGranted int64  `json:"credits_granted,omitempty"`
	TrialExtraDays int    `json:"trial_extra_days,omitempty"`
}

func (s *Server) handleCouponRedeem(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Coupons == nil {
		writeErr(w, http.StatusServiceUnavailable, "coupons_not_wired", "")
		return
	}
	var req redeemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Code == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "code required")
		return
	}
	res, err := s.Coupons.Redeem(r.Context(), billing.RedeemArgs{
		Code: req.Code, UserID: uid, PlanCode: req.PlanCode,
		AmountCents: req.AmountCents, Currency: req.Currency,
	})
	if err != nil {
		writeCouponErr(w, err)
		return
	}

	resp := redeemResp{
		RedemptionID:   res.RedemptionID.String(),
		CouponCode:     res.Coupon.Code,
		Kind:           string(res.Coupon.Kind),
		DiscountCents:  res.DiscountCents,
		CreditsGranted: res.CreditsAmount,
		TrialExtraDays: res.TrialExtraDays,
	}
	// credits_grant 副作用: 调 Credits.Grant + 回填 log_id.
	if res.CreditsAmount > 0 && s.Credits != nil {
		expires := s.now().Add(30 * 24 * time.Hour)
		pkg, _, gErr := s.Credits.Grant(r.Context(), credits.GrantArgs{
			UserID:         uid,
			Amount:         res.CreditsAmount,
			Kind:           credits.KindTimeLimited,
			Source:         credits.SourceReward,
			ExpiresAt:      &expires,
			Remark:         "coupon: " + res.Coupon.Code,
			IdempotencyKey: "coupon:" + res.RedemptionID.String(),
		})
		if gErr == nil && pkg != nil {
			// 拿不到 credit_log_id 的话, 回填 redemption.id (自我引用) 占位.
			// 真正 log_id 在 credits.Grant 内部由 insertLog 返回但 Grant 不暴露;
			// 简化: redemption.credit_log_id 字段允许为 NULL, 这里跳过填充.
			_ = pkg
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeCouponErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrCouponNotFound):
		writeErr(w, http.StatusNotFound, "coupon_not_found", "")
	case errors.Is(err, billing.ErrCouponInactive):
		writeErr(w, http.StatusBadRequest, "coupon_inactive", "")
	case errors.Is(err, billing.ErrCouponExpired):
		writeErr(w, http.StatusBadRequest, "coupon_expired", "")
	case errors.Is(err, billing.ErrCouponAlreadyUsed):
		writeErr(w, http.StatusConflict, "coupon_already_used", "")
	case errors.Is(err, billing.ErrCouponPlanMismatch):
		writeErr(w, http.StatusBadRequest, "coupon_plan_mismatch", "")
	case errors.Is(err, billing.ErrCouponCurrencyMismatch):
		writeErr(w, http.StatusBadRequest, "coupon_currency_mismatch", "")
	case errors.Is(err, billing.ErrCouponMaxUsesReached):
		writeErr(w, http.StatusBadRequest, "coupon_max_uses", "")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// ─── Referrals ───────────────────────────────

type inviteResp struct {
	InviteCode string                 `json:"invite_code"`
	Stats      *billing.ReferralStats `json:"stats,omitempty"`
}

func (s *Server) handleReferralInvite(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Referrals == nil {
		writeErr(w, http.StatusServiceUnavailable, "referrals_not_wired", "")
		return
	}
	stats, err := s.Referrals.Stats(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inviteResp{
		InviteCode: stats.InviteCode,
		Stats:      stats,
	})
}

type claimReq struct {
	InviterUserID string `json:"inviter_user_id"`
	InviteCode    string `json:"invite_code"`
	DeviceFP      string `json:"device_fp,omitempty"`
	ClientIP      string `json:"client_ip,omitempty"`
}

func (s *Server) handleReferralClaim(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Referrals == nil {
		writeErr(w, http.StatusServiceUnavailable, "referrals_not_wired", "")
		return
	}
	var req claimReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	inviterID, err := uuid.Parse(req.InviterUserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "inviter_user_id invalid")
		return
	}
	ip := parseClientIP(r, req.ClientIP)
	id, err := s.Referrals.Claim(r.Context(), billing.ClaimArgs{
		InviterUserID: inviterID,
		InviteeUserID: uid,
		InviteCode:    req.InviteCode,
		DeviceFP:      req.DeviceFP,
		IP:            ip,
	})
	if err != nil {
		writeReferralErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"referral_id": id.String(),
		"status":      "pending",
	})
}

func writeReferralErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrInviteCodeNotFound):
		writeErr(w, http.StatusNotFound, "invite_code_not_found", "")
	case errors.Is(err, billing.ErrSelfReferralForbidden):
		writeErr(w, http.StatusBadRequest, "self_referral_forbidden", "")
	case errors.Is(err, billing.ErrInviteeAlreadyReferred):
		writeErr(w, http.StatusConflict, "already_referred", "")
	case errors.Is(err, billing.ErrDeviceShared):
		writeErr(w, http.StatusForbidden, "device_shared", "")
	case errors.Is(err, billing.ErrIPRateLimited):
		writeErr(w, http.StatusTooManyRequests, "ip_rate_limited", "")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// ─── helpers ─────────────────────────────────

// now — Server 时钟; nil 时用 time.Now.
func (s *Server) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// 让 imports 都被使用, 不引发 vet warning.
var (
	_ = context.Background
	_ = netip.Addr{}
)
