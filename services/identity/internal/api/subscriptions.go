// subscriptions.go — W5-4/5/6/7 订阅生命周期 endpoints.
//
//   POST /v1/subscriptions/checkout      — 启动支付流程, 按通道返不同响应
//   POST /v1/subscriptions/cancel        — 取消订阅 (period_end / immediate)
//   POST /v1/subscriptions/change_plan   — 升级立即 + proration; 降级 period_end
//   POST /v1/subscriptions/resume        — period_end 前撤销取消
//
// 鉴权: 全部走 requireAuth, 取 claims.UserID 当 active subscription 主体.
//
// 不在 plans.go 里面: plans.go 只有 GET (公开 / 鉴权但只读). 这里都是 POST
// 写路径, 业务复杂度高, 单独文件方便测试.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/billing"
)

// ─── checkout ──────────────────────────────────────────

type checkoutReq struct {
	PlanCode     string `json:"plan_code"`
	BillingCycle string `json:"billing_cycle"` // monthly / yearly. 默认 monthly.
	Provider     string `json:"provider"`      // stripe / wechat_native / wechat_jsapi / wechat_h5 / alipay_pc / alipay_wap
	Trial        bool   `json:"trial,omitempty"`
	DeviceFP     string `json:"device_fp,omitempty"`
	OpenID       string `json:"openid,omitempty"`    // wechat_jsapi 必填
	ClientIP     string `json:"client_ip,omitempty"` // wechat_h5 必填; 兜底取 X-Forwarded-For
}

type checkoutResp struct {
	Provider    string `json:"provider"`
	OutTradeNo  string `json:"out_trade_no"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`

	// provider-specific (按 provider 各填一项):
	CodeURL     string `json:"code_url,omitempty"`     // wechat_native
	PrepayID    string `json:"prepay_id,omitempty"`    // wechat_jsapi
	H5URL       string `json:"h5_url,omitempty"`       // wechat_h5
	RedirectURL string `json:"redirect_url,omitempty"` // alipay_pc / alipay_wap / stripe (checkout session)
}

func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	var req checkoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.PlanCode == "" || req.Provider == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "plan_code and provider required")
		return
	}
	if req.BillingCycle == "" {
		req.BillingCycle = "monthly"
	}
	if s.Plans == nil {
		writeErr(w, http.StatusServiceUnavailable, "plans_not_wired", "")
		return
	}
	plan, err := s.Plans.Get(r.Context(), billing.Plan(req.PlanCode))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plan_not_found", req.PlanCode)
		return
	}

	// W5-8 trial 防刷
	if req.Trial && s.Trial != nil {
		ip := parseClientIP(r, req.ClientIP)
		got := s.Trial.Check(r.Context(), uid, req.DeviceFP, ip)
		if !got.Eligible {
			_ = s.Trial.Record(r.Context(), uid, req.DeviceFP, ip, false, got.Reason)
			writeErr(w, http.StatusForbidden, "trial_not_eligible", got.Reason)
			return
		}
	}

	// 价格 (cents). yearly 走 price_yearly, monthly 走 price_monthly.
	priceCents := int64(plan.PriceMonthly * 100)
	if req.BillingCycle == "yearly" {
		priceCents = int64(plan.PriceYearly * 100)
	}
	if priceCents <= 0 {
		writeErr(w, http.StatusBadRequest, "plan_not_payable",
			fmt.Sprintf("plan %s has 0 price for %s cycle", plan.Code, req.BillingCycle))
		return
	}

	// 提前校验 provider, 避免下面 INSERT 后 unknown 走入 default 分支返 400 但已写脏数据.
	if !knownProvider(req.Provider) {
		writeErr(w, http.StatusBadRequest, "bad_request", "unsupported provider: "+req.Provider)
		return
	}

	outTradeNo := genOutTradeNo(uid)
	resp := checkoutResp{
		Provider:    req.Provider,
		OutTradeNo:  outTradeNo,
		AmountCents: priceCents,
		Currency:    plan.PriceCurrency,
	}

	// 写 payment_orders pending 行 (provider_order_id=out_trade_no).
	if err := insertPendingOrder(r.Context(), s.Subscriptions.Pool(), uid, req.Provider, outTradeNo, priceCents, plan.PriceCurrency); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// 调对应 client.
	switch req.Provider {
	case "wechat_native":
		if s.Wechat == nil || !s.Wechat.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "provider_not_configured", "wechat")
			return
		}
		codeURL, err := s.Wechat.CreateNativeOrder(r.Context(), billing.WechatOrderRequest{
			Description: planDesc(plan, req.BillingCycle),
			OutTradeNo:  outTradeNo,
			TotalCents:  priceCents,
			UserID:      uid.String(),
		})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "wechat_error", err.Error())
			return
		}
		resp.CodeURL = codeURL
	case "wechat_jsapi":
		if s.Wechat == nil || !s.Wechat.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "provider_not_configured", "wechat")
			return
		}
		if req.OpenID == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "openid required for wechat_jsapi")
			return
		}
		prepay, err := s.Wechat.CreateJSAPIOrder(r.Context(), billing.WechatOrderRequest{
			Description: planDesc(plan, req.BillingCycle),
			OutTradeNo:  outTradeNo,
			TotalCents:  priceCents,
			UserID:      uid.String(),
			OpenID:      req.OpenID,
		})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "wechat_error", err.Error())
			return
		}
		resp.PrepayID = prepay
	case "wechat_h5":
		if s.Wechat == nil || !s.Wechat.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "provider_not_configured", "wechat")
			return
		}
		clientIP := strings.TrimSpace(req.ClientIP)
		if clientIP == "" {
			if ip := parseClientIP(r, ""); ip.IsValid() {
				clientIP = ip.String()
			}
		}
		if clientIP == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "client_ip required for wechat_h5")
			return
		}
		h5URL, err := s.Wechat.CreateH5Order(r.Context(), billing.WechatOrderRequest{
			Description: planDesc(plan, req.BillingCycle),
			OutTradeNo:  outTradeNo,
			TotalCents:  priceCents,
			UserID:      uid.String(),
			ClientIP:    clientIP,
		})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "wechat_error", err.Error())
			return
		}
		resp.H5URL = h5URL
	case "alipay_pc", "alipay_wap":
		if s.Alipay == nil || !s.Alipay.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "provider_not_configured", "alipay")
			return
		}
		args := billing.AlipayTradeArgs{
			OutTradeNo:  outTradeNo,
			TotalAmount: float64(priceCents) / 100.0,
			Subject:     planDesc(plan, req.BillingCycle),
		}
		var redirect string
		var err error
		if req.Provider == "alipay_pc" {
			redirect, err = s.Alipay.CreatePagePay(args)
		} else {
			redirect, err = s.Alipay.CreateWapPay(args)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "alipay_error", err.Error())
			return
		}
		resp.RedirectURL = redirect
	case "stripe":
		// Stripe checkout session 实际由前端 + Stripe SDK 走 — 这里只回 placeholder.
		// 真集成会调 Stripe checkout API 创建 session, 见后续 W6.
		writeErr(w, http.StatusNotImplemented, "stripe_checkout_pending",
			"Stripe checkout integration is part of W5 stripe-side wiring; use existing Stripe payment link for now")
		return
	default:
		writeErr(w, http.StatusBadRequest, "bad_request", "unsupported provider: "+req.Provider)
		return
	}

	// 试用记录: 把 trial 申请落 attempts 表 (succeeded=true 在 webhook 兑现后再补).
	if req.Trial && s.Trial != nil {
		_ = s.Trial.Record(r.Context(), uid, req.DeviceFP, parseClientIP(r, req.ClientIP), false, "pending")
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── cancel ────────────────────────────────────────────

type cancelReq struct {
	Immediate bool `json:"immediate,omitempty"`
}

func (s *Server) handleCancelSubscription(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Subscriptions == nil {
		writeErr(w, http.StatusServiceUnavailable, "subs_not_wired", "")
		return
	}
	var req cancelReq
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional; 兼容空 body
	if r.URL.Query().Get("immediate") == "true" {
		req.Immediate = true
	}

	sub, err := s.Subscriptions.GetActiveByUser(r.Context(), uid)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		writeErr(w, http.StatusNotFound, "no_active_subscription", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// 先 active/trialing → canceled (前提条件保证).
	updated, err := s.Subscriptions.Transition(r.Context(), sub, billing.SubStatusCanceled,
		fmt.Sprintf("user_cancel immediate=%v", req.Immediate), "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "transition_failed", err.Error())
		return
	}
	// immediate=true 再走一步 canceled → expired (退款交给后续 webhook / job 兑现).
	if req.Immediate {
		updated, err = s.Subscriptions.Transition(r.Context(), updated, billing.SubStatusExpired,
			"user_cancel_immediate", "")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "transition_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          updated.ID.String(),
		"status":      string(updated.Status),
		"immediate":   req.Immediate,
		"canceled_at": updated.CanceledAt,
	})
}

// ─── change_plan ──────────────────────────────────────

type changePlanReq struct {
	PlanCode string `json:"plan_code"`
}

type changePlanResp struct {
	OldPlan     string         `json:"old_plan"`
	NewPlan     string         `json:"new_plan"`
	Effective   string         `json:"effective"` // immediate / period_end
	Proration   *prorationView `json:"proration,omitempty"`
	ScheduledAt *time.Time     `json:"scheduled_at,omitempty"`
}

type prorationView struct {
	UnusedRefundCents     int64   `json:"unused_refund_cents"`
	NewProrateChargeCents int64   `json:"new_prorate_charge_cents"`
	NetChargeCents        int64   `json:"net_charge_cents"`
	RemainingRatio        float64 `json:"remaining_ratio"`
}

func (s *Server) handleChangePlan(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Subscriptions == nil || s.Plans == nil {
		writeErr(w, http.StatusServiceUnavailable, "subs_not_wired", "")
		return
	}
	var req changePlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.PlanCode == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "plan_code required")
		return
	}

	sub, err := s.Subscriptions.GetActiveByUser(r.Context(), uid)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		writeErr(w, http.StatusNotFound, "no_active_subscription", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	oldPlan, err := s.Plans.GetByID(r.Context(), sub.PlanID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "old_plan_missing", err.Error())
		return
	}
	newPlan, err := s.Plans.Get(r.Context(), billing.Plan(req.PlanCode))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plan_not_found", req.PlanCode)
		return
	}
	if oldPlan.ID == newPlan.ID {
		writeErr(w, http.StatusBadRequest, "same_plan", "")
		return
	}

	resp := changePlanResp{
		OldPlan: string(oldPlan.Code),
		NewPlan: string(newPlan.Code),
	}

	if billing.IsUpgrade(oldPlan.SortOrder, newPlan.SortOrder) {
		// 升级: 立即换 + 算 proration (净支付由前端再调 checkout 收款).
		now := time.Now().UTC()
		prorated, _ := billing.ComputeProration(billing.ProrationArgs{
			OldPriceCents: int64(oldPlan.PriceMonthly * 100),
			NewPriceCents: int64(newPlan.PriceMonthly * 100),
			PeriodStart:   sub.CurrentPeriodStart,
			PeriodEnd:     sub.CurrentPeriodEnd,
			Now:           now,
		})
		_, err := s.Subscriptions.ChangePlan(r.Context(), sub, newPlan.ID, s.Plans, "")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "change_plan_failed", err.Error())
			return
		}
		resp.Effective = "immediate"
		resp.Proration = &prorationView{
			UnusedRefundCents:     prorated.UnusedRefundCents,
			NewProrateChargeCents: prorated.NewProrateChargeCents,
			NetChargeCents:        prorated.NetChargeCents,
			RemainingRatio:        prorated.RemainingRatio,
		}
	} else {
		// 降级: 当前周期保留旧 plan, period_end 时切换. metadata 里记录待执行变更.
		end := sub.CurrentPeriodEnd
		if err := scheduleDowngrade(r.Context(), s.Subscriptions.Pool(), sub.ID, newPlan.ID, end); err != nil {
			writeErr(w, http.StatusInternalServerError, "schedule_failed", err.Error())
			return
		}
		resp.Effective = "period_end"
		resp.ScheduledAt = &end
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── resume ────────────────────────────────────────────

func (s *Server) handleResumeSubscription(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Subscriptions == nil {
		writeErr(w, http.StatusServiceUnavailable, "subs_not_wired", "")
		return
	}
	subs, err := s.Subscriptions.ListByUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 找最近一条 canceled 的 + period_end 还未到.
	now := time.Now().UTC()
	var target *billing.Subscription
	for i := range subs {
		if subs[i].Status == billing.SubStatusCanceled && subs[i].CurrentPeriodEnd.After(now) {
			target = &subs[i]
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusNotFound, "no_resumable_subscription",
			"no canceled subscription with active period to resume")
		return
	}
	updated, err := s.Subscriptions.Transition(r.Context(), target, billing.SubStatusActive,
		"user_resume", "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "resume_failed", err.Error())
		return
	}
	// canceled_at clear (Transition 不清, 这里手动)
	_, _ = s.Subscriptions.Pool().Exec(r.Context(),
		`UPDATE billing.subscriptions SET canceled_at=NULL WHERE id=$1`, updated.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     updated.ID.String(),
		"status": string(updated.Status),
	})
}

// ─── orders 历史 ────────────────────────────────────

type orderView struct {
	ID              string  `json:"id"`
	Provider        string  `json:"provider"`
	OrderType       string  `json:"order_type"`
	Amount          float64 `json:"amount"`
	Currency        string  `json:"currency"`
	Status          string  `json:"status"`
	ProviderOrderID string  `json:"provider_order_id"`
	FailureMessage  string  `json:"failure_message,omitempty"`
	RefundedAmount  float64 `json:"refunded_amount,omitempty"`
	CreatedAt       string  `json:"created_at"`
	PaidAt          string  `json:"paid_at,omitempty"`
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Subscriptions == nil {
		writeErr(w, http.StatusServiceUnavailable, "subs_not_wired", "")
		return
	}
	rows, err := s.Subscriptions.Pool().Query(r.Context(), `
		SELECT id, provider, order_type, amount, currency, status,
		       provider_order_id, COALESCE(failure_message, ''), refund_amount,
		       created_at, paid_at
		FROM billing.payment_orders
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer rows.Close()
	out := []orderView{}
	for rows.Next() {
		var o orderView
		var paidAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&o.ID, &o.Provider, &o.OrderType, &o.Amount, &o.Currency,
			&o.Status, &o.ProviderOrderID, &o.FailureMessage, &o.RefundedAmount,
			&createdAt, &paidAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan", err.Error())
			return
		}
		o.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		if paidAt != nil {
			o.PaidAt = paidAt.UTC().Format(time.RFC3339)
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// ─── helpers ───────────────────────────────────────────

// authedUser — 从 claims 取 uuid; 失败时已写过响应, 返 ok=false.
func authedUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return uuid.Nil, false
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user_id", "")
		return uuid.Nil, false
	}
	return uid, true
}

// parseClientIP — 优先用 body.client_ip, 否则取 X-Forwarded-For 第一个 / RemoteAddr.
func parseClientIP(r *http.Request, override string) netip.Addr {
	override = strings.TrimSpace(override)
	if override != "" {
		if addr, err := netip.ParseAddr(override); err == nil {
			return addr
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if addr, err := netip.ParseAddr(first); err == nil {
			return addr
		}
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	addr, _ := netip.ParseAddr(host)
	return addr
}

// genOutTradeNo — BIU + uid 前 8 + 时间戳, 32 字符内.
func genOutTradeNo(uid uuid.UUID) string {
	return fmt.Sprintf("BIU%s%d", strings.ReplaceAll(uid.String(), "-", "")[:8], time.Now().UnixMilli())
}

// planDesc — 商户 description 字段 (微信 ≤ 127 字).
func planDesc(p *billing.PlanRow, cycle string) string {
	return fmt.Sprintf("BiuMind %s %s", p.Name, cycle)
}

// insertPendingOrder — 写 payment_orders pending 行.
func insertPendingOrder(ctx interface {
	Done() <-chan struct{}
	Err() error
	Value(any) any
	Deadline() (time.Time, bool)
}, pool *pgxpool.Pool, userID uuid.UUID, providerStr, outTradeNo string, priceCents int64, currency string) error {
	provider := normalizeProviderName(providerStr)
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.payment_orders
		    (user_id, order_type, provider, amount, currency, status, provider_order_id)
		VALUES ($1, 'subscription', $2, $3 / 100.0, $4, 'pending', $5)
	`, userID, provider, priceCents, currency, outTradeNo)
	return err
}

// knownProvider — checkout req.provider 白名单.
func knownProvider(s string) bool {
	switch s {
	case "stripe", "wechat_native", "wechat_jsapi", "wechat_h5", "alipay_pc", "alipay_wap":
		return true
	}
	return false
}

// normalizeProviderName — checkout req 里的 provider (e.g. wechat_native)
// 映射到 payment_orders.provider CHECK 允许值 (wechat_pay / alipay / stripe).
func normalizeProviderName(s string) string {
	switch {
	case strings.HasPrefix(s, "wechat"):
		return "wechat_pay"
	case strings.HasPrefix(s, "alipay"):
		return "alipay"
	case s == "stripe":
		return "stripe"
	default:
		return s
	}
}

// scheduleDowngrade — 把降级目标写到 sub.metadata.scheduled_change.
// W4-4 cron / 单独 schedule processor 在 period_end 时兑现 (后续工程).
func scheduleDowngrade(ctx interface {
	Done() <-chan struct{}
	Err() error
	Value(any) any
	Deadline() (time.Time, bool)
}, pool *pgxpool.Pool, subID, scheduledPlanID uuid.UUID, scheduledAt time.Time) error {
	patch, _ := json.Marshal(map[string]any{
		"scheduled_change": map[string]any{
			"plan_id":      scheduledPlanID.String(),
			"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
		},
	})
	_, err := pool.Exec(ctx, `
		UPDATE billing.subscriptions
		SET metadata = metadata || $1::jsonb, updated_at = now()
		WHERE id = $2
	`, string(patch), subID)
	return err
}
