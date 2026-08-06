// plans.go — Phase 4 W2-5 会员体系 endpoints.
//
//   GET /v1/plans              — 公开, 列出 4 档套餐 + 当前用户高亮
//   GET /v1/subscriptions/me   — 鉴权, 当前用户的活跃订阅 + 周期
//
// /v1/plans 公开是因为客户端 (含未登录) 在「升级」页面也要看价格;
// 当前订阅信息只在 Authorization 头存在时才查 (best-effort 高亮).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/google/uuid"
)

// planView — GET /v1/plans 返回的 list 元素 + GET /v1/subscriptions/me
// 嵌套返回的 plan 视图. 字段集是 admin Vue / 客户端「升级」页面所需.
type planView struct {
	ID                 string         `json:"id"`
	Code               string         `json:"code"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	SortOrder          int            `json:"sort_order"`
	PriceCurrency      string         `json:"price_currency"`
	PriceMonthly       float64        `json:"price_monthly"`
	PriceYearly        float64        `json:"price_yearly"`
	MonthlyCredits     int64          `json:"monthly_credits"`
	Benefits           map[string]any `json:"benefits"`
	IsCurrent          bool           `json:"is_current,omitempty"` // 当前用户已订阅
	StripePriceMonthly string         `json:"stripe_price_monthly,omitempty"`
	StripePriceYearly  string         `json:"stripe_price_yearly,omitempty"`
}

func toPlanView(p *billing.PlanRow) planView {
	return planView{
		ID:                 p.ID.String(),
		Code:               string(p.Code),
		Name:               p.Name,
		Description:        p.Description,
		SortOrder:          p.SortOrder,
		PriceCurrency:      p.PriceCurrency,
		PriceMonthly:       p.PriceMonthly,
		PriceYearly:        p.PriceYearly,
		MonthlyCredits:     p.MonthlyCredits,
		Benefits:           limitsToMap(p.Benefits),
		StripePriceMonthly: p.StripePriceMonthly,
		StripePriceYearly:  p.StripePriceYearly,
	}
}

// limitsToMap 与 plans.go limitsToJSONBenefits 同款 snake_case key.
func limitsToMap(l billing.PlanLimits) map[string]any {
	return map[string]any{
		"hub_rpm":            l.HubRPM,
		"hub_tpm":            l.HubTPM,
		"sandbox_daily":      l.SandboxDaily,
		"sandbox_concurrent": l.SandboxConcurrent,
		"memory_quota":       l.MemoryQuota,
		"brain_projects":     l.BrainProjects,
	}
}

// handleListPlans — GET /v1/plans (公开).
//
// 当 Authorization 头存在且有效时, 把 user 当前订阅的 plan_id 标记为
// is_current=true. 鉴权失败 / 无 token 不报错 — 只是不高亮.
func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	if s.Plans == nil {
		writeErr(w, http.StatusServiceUnavailable, "plans_not_wired", "")
		return
	}
	plans, err := s.Plans.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// best-effort 高亮当前订阅
	currentPlanID := uuid.Nil
	if claims := claimsFromAuthHeader(r, s.Verifier); claims != nil && s.Subscriptions != nil {
		uid, err := uuid.Parse(claims.UserID)
		if err == nil {
			if sub, err := s.Subscriptions.GetActiveByUser(r.Context(), uid); err == nil {
				currentPlanID = sub.PlanID
			}
		}
	}

	out := make([]planView, 0, len(plans))
	for _, p := range plans {
		v := toPlanView(&p)
		if p.ID == currentPlanID {
			v.IsCurrent = true
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plans": out, "total": len(out),
	})
}

// claimsFromAuthHeader best-effort 解析 Authorization Bearer token,
// 失败任何步骤都返 nil (不报错). 用于"半公开"接口 — 有 token 就用,
// 无就 anonymous.
func claimsFromAuthHeader(r *http.Request, verifier *bauth.Verifier) *bauth.Claims {
	if verifier == nil {
		return nil
	}
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) <= 7 || authHeader[:7] != "Bearer " {
		return nil
	}
	claims, err := verifier.Verify(authHeader[7:])
	if err != nil {
		return nil
	}
	return claims
}

// ─── /v1/subscriptions/me ─────────────────────────────

// quotaUsageView — W4-8 单个 ref_type 的本月 quota 状态.
type quotaUsageView struct {
	Used        int64  `json:"used"`
	Monthly     int64  `json:"monthly"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

// subscriptionView — GET /v1/subscriptions/me 响应 shape.
// 嵌套包含 plan 视图; 客户端不用再二次查 /v1/plans.
type subscriptionView struct {
	ID                   string   `json:"id"`
	UserID               string   `json:"user_id"`
	Plan                 planView `json:"plan"`
	Status               string   `json:"status"`
	CurrentPeriodStart   string   `json:"current_period_start"`
	CurrentPeriodEnd     string   `json:"current_period_end"`
	TrialEndAt           *string  `json:"trial_end_at,omitempty"`
	CancelAt             *string  `json:"cancel_at,omitempty"`
	CanceledAt           *string  `json:"canceled_at,omitempty"`
	BillingCycle         string   `json:"billing_cycle"`
	StripeCustomerID     string   `json:"stripe_customer_id,omitempty"`
	StripeSubscriptionID string   `json:"stripe_subscription_id,omitempty"`
	IsActive             bool     `json:"is_active"`

	// 本月 quota usage — W4-8: 真接入 credits.GetQuotaStates.
	// key: ref_type (chat_message / aigc_task). 没订阅或 ref_type 无配额时
	// 该 key 缺省 (客户端按 0 处理).
	Quota map[string]quotaUsageView `json:"quota"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// handleMySubscription — GET /v1/subscriptions/me.
//
// 用户没有任何订阅时 (新注册 / 从未付费): 返 200 + 一个虚拟 free plan
// 视图, 让客户端 UI 不需要分支处理. status='free' 是一个伪状态, 表明
// 用户走 default Plan limits.
func (s *Server) handleMySubscription(w http.ResponseWriter, r *http.Request) {
	if s.Subscriptions == nil || s.Plans == nil {
		writeErr(w, http.StatusServiceUnavailable, "subscriptions_not_wired", "")
		return
	}

	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user_id", "")
		return
	}

	sub, err := s.Subscriptions.GetActiveByUser(r.Context(), uid)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		// 没有订阅 — 返虚拟 free plan
		freeP, err := s.Plans.Get(r.Context(), billing.PlanFree)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "free_plan_missing", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, subscriptionView{
			ID:           "",
			UserID:       uid.String(),
			Plan:         toPlanView(freeP),
			Status:       "free",
			IsActive:     true,
			BillingCycle: "lifetime",
			Quota:        s.fetchQuotaUsage(r.Context(), uid),
		})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	plan, err := s.Plans.GetByID(r.Context(), sub.PlanID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plan_missing", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, subscriptionView{
		ID:                   sub.ID.String(),
		UserID:               sub.UserID.String(),
		Plan:                 toPlanView(plan),
		Status:               string(sub.Status),
		CurrentPeriodStart:   sub.CurrentPeriodStart.UTC().Format("2006-01-02T15:04:05Z"),
		CurrentPeriodEnd:     sub.CurrentPeriodEnd.UTC().Format("2006-01-02T15:04:05Z"),
		TrialEndAt:           ptrTimeStr(sub.TrialEndAt),
		CancelAt:             ptrTimeStr(sub.CancelAt),
		CanceledAt:           ptrTimeStr(sub.CanceledAt),
		BillingCycle:         sub.BillingCycle,
		StripeCustomerID:     sub.StripeCustomerID,
		StripeSubscriptionID: sub.StripeSubscriptionID,
		IsActive:             sub.Status.IsActive(),
		Quota:                s.fetchQuotaUsage(r.Context(), uid),
		CreatedAt:            sub.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:            sub.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// fetchQuotaUsage — W4-8: 调 credits.GetQuotaStates 拿当月 quota 用量.
// Credits nil (单测无 DB) / 失败时返空 map, 不阻塞订阅查询.
func (s *Server) fetchQuotaUsage(ctx context.Context, uid uuid.UUID) map[string]quotaUsageView {
	if s.Credits == nil {
		return map[string]quotaUsageView{}
	}
	states, err := s.Credits.GetQuotaStates(ctx, uid)
	if err != nil {
		s.Logger.Warn("quota states failed", "user", uid.String(), "err", err)
		return map[string]quotaUsageView{}
	}
	out := make(map[string]quotaUsageView, len(states))
	for _, st := range states {
		out[string(st.RefType)] = quotaUsageView{
			Used:        st.UsedAmount,
			Monthly:     st.MonthlyAmount,
			PeriodStart: st.PeriodStart.UTC().Format("2006-01-02T15:04:05Z"),
			PeriodEnd:   st.PeriodEnd.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return out
}

// emptyUsage — 已废弃, 保留无引用避免 import 漂移. 删除是 W4 后续 cleanup.
//
//nolint:unused
func emptyUsage() map[string]int64 {
	return map[string]int64{
		"hub_rpm_used":       0,
		"hub_tpm_used":       0,
		"sandbox_daily_used": 0,
		"credits_used":       0,
	}
}

func ptrTimeStr(t interface{}) *string {
	// nil-aware: *time.Time 类型, nil → nil
	type tlike interface {
		IsZero() bool
		UTC() interface {
			Format(string) string
		}
	}
	if t == nil {
		return nil
	}
	// 通过 json marshal 兜底处理
	b, err := json.Marshal(t)
	if err != nil {
		return nil
	}
	s := string(b)
	if s == "null" {
		return nil
	}
	// json.Marshal(*time.Time) → "\"2026-...\"" (含引号), 去引号
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		return &s
	}
	return nil
}
