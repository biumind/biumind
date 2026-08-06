package api

// credits.go — 用户侧积分相关公开 endpoint.
//
//   GET  /v1/identity/me/credits/balance         (Bearer)  当前用户余额
//   GET  /v1/identity/me/credits/logs            (Bearer)  流水分页
//   GET  /v1/identity/me/credits/packages        (Bearer)  当前活跃 packages
//   GET  /v1/credits/recharge-options                       充值套餐列表（公开）
//   POST /v1/identity/me/credits/recharge        (Bearer)  mock 充值（dev 占位, 直接入账）
//   POST /v1/credits/checkout                    (Bearer)  W7 真支付 — 选支付通道, 写 payment_orders + 调 wechat/alipay
//
// 真支付路径: /credits/checkout 创建 pending 订单 → 用户跳支付通道 → 支付通道
// webhook (W5-9) 收到 SUCCESS → 标记 succeeded + 触发 Grant. 见 webhooks.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/credits"
	"github.com/google/uuid"
)

// requireUserID 从 JWT claims 抽取 user_id, 失败时直接写 401 / 422 并返回 false.
func (s *Server) requireUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return uuid.Nil, false
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return uuid.Nil, false
	}
	return uid, true
}

// ─── GET /v1/identity/me/credits/balance ─────────────

func (s *Server) handleCreditsBalance(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	bal, err := s.Credits.GetBalance(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, balanceOut(bal))
}

// ─── GET /v1/identity/me/credits/logs ────────────────

func (s *Server) handleCreditsLogs(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	refType := credits.LogRefType(q.Get("ref_type")) // 空 = 不过滤
	limit, offset := paginationFromQuery(q)

	logs, err := s.Credits.ListLogs(r.Context(), uid, refType, nil, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, logOut(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": out})
}

// ─── GET /v1/identity/me/credits/packages ────────────

func (s *Server) handleCreditsPackages(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset := paginationFromQuery(r.URL.Query())
	pkgs, err := s.Credits.ListPackages(r.Context(), uid, "", false, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, packageOut(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": out})
}

// ─── GET /v1/credits/recharge-options ────────────────

func (s *Server) handleRechargeOptions(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	options, err := s.Store.ListRechargeOptions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": options})
}

// ─── POST /v1/identity/me/credits/recharge ───────────

type rechargeReq struct {
	OptionID       string `json:"option_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (s *Server) handleRecharge(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil || s.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	var req rechargeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	optID, err := uuid.Parse(req.OptionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_option_id", err.Error())
		return
	}

	// 拉套餐配置
	opt, err := s.Store.GetRechargeOption(r.Context(), optID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "option_not_found", err.Error())
		return
	}
	if !opt.Enabled {
		writeErr(w, http.StatusForbidden, "option_disabled", "")
		return
	}

	// v1 mock：直接 Grant，不走支付。v2 改为返回 payment_intent_url.
	args := credits.GrantArgs{
		UserID:         uid,
		Amount:         opt.CreditsAmount,
		Kind:           credits.PackageKind(opt.Kind),
		Source:         credits.SourceRecharge,
		Remark:         "mock recharge: " + opt.DisplayName,
		IdempotencyKey: req.IdempotencyKey,
	}
	if opt.Kind == "time_limited" {
		exp := time.Now().Add(time.Duration(opt.ValidDays) * 24 * time.Hour)
		args.ExpiresAt = &exp
	}
	pkg, bal, err := s.Credits.Grant(r.Context(), args)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"package":  packageOut(pkg),
		"balance":  balanceOut(bal),
	})
}

// ─── POST /v1/credits/checkout — W7 真支付 ────────

type creditsCheckoutReq struct {
	OptionID string `json:"option_id"`
	Provider string `json:"provider"` // wechat_native / wechat_jsapi / wechat_h5 / alipay_pc / alipay_wap / stripe
	OpenID   string `json:"openid,omitempty"`    // wechat_jsapi 必填
	ClientIP string `json:"client_ip,omitempty"` // wechat_h5 必填
}

func (s *Server) handleCreditsCheckout(w http.ResponseWriter, r *http.Request) {
	uid, ok := authedUser(w, r)
	if !ok {
		return
	}
	if s.Store == nil || s.Subscriptions == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_unavailable", "")
		return
	}
	var req creditsCheckoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.OptionID == "" || req.Provider == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "option_id and provider required")
		return
	}
	if !knownProvider(req.Provider) {
		writeErr(w, http.StatusBadRequest, "bad_request", "unsupported provider: "+req.Provider)
		return
	}
	optID, err := uuid.Parse(req.OptionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_option_id", err.Error())
		return
	}
	opt, err := s.Store.GetRechargeOption(r.Context(), optID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "option_not_found", err.Error())
		return
	}
	if !opt.Enabled {
		writeErr(w, http.StatusForbidden, "option_disabled", "")
		return
	}
	// price_micro_cny / 10000 = cents
	priceCents := opt.PriceMicroCNY / 10000
	if priceCents <= 0 {
		writeErr(w, http.StatusBadRequest, "option_zero_price", "")
		return
	}

	outTradeNo := genOutTradeNo(uid)
	resp := checkoutResp{
		Provider:    req.Provider,
		OutTradeNo:  outTradeNo,
		AmountCents: priceCents,
		Currency:    "CNY",
	}

	// 写 payment_orders pending + metadata.option_id (webhook 兑现时用).
	meta, _ := json.Marshal(map[string]any{
		"option_id":      opt.ID.String(),
		"kind":           opt.Kind,
		"credits_amount": opt.CreditsAmount,
		"valid_days":     opt.ValidDays,
		"display_name":   opt.DisplayName,
	})
	pool := s.Subscriptions.Pool()
	if _, err := pool.Exec(r.Context(), `
		INSERT INTO billing.payment_orders
		    (user_id, order_type, provider, amount, currency, status,
		     provider_order_id, metadata)
		VALUES ($1, 'topup', $2, $3 / 100.0, 'CNY', 'pending', $4, $5::jsonb)
	`, uid, normalizeProviderName(req.Provider), priceCents, outTradeNo, string(meta)); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	desc := fmt.Sprintf("BiuMind 充值 · %s", opt.DisplayName)
	switch req.Provider {
	case "wechat_native":
		if s.Wechat == nil || !s.Wechat.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "provider_not_configured", "wechat")
			return
		}
		codeURL, err := s.Wechat.CreateNativeOrder(r.Context(), billing.WechatOrderRequest{
			Description: desc, OutTradeNo: outTradeNo, TotalCents: priceCents, UserID: uid.String(),
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
			Description: desc, OutTradeNo: outTradeNo, TotalCents: priceCents,
			UserID: uid.String(), OpenID: req.OpenID,
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
		clientIP := req.ClientIP
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
			Description: desc, OutTradeNo: outTradeNo, TotalCents: priceCents,
			UserID: uid.String(), ClientIP: clientIP,
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
			OutTradeNo: outTradeNo, TotalAmount: float64(priceCents) / 100.0, Subject: desc,
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
		writeErr(w, http.StatusNotImplemented, "stripe_topup_pending", "Stripe topup integration deferred")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── webhook 兑现 topup ───────────────────────────

// fulfillTopupOrder — webhook 收到 SUCCESS 后调用. 查 payment_orders.metadata
// 里的 option_id, 调 Credits.Grant 真发积分. 幂等键用 order_id 自带.
//
// 走 payment_orders.fulfilled_at 标记防重复 (一笔订单只兑现一次).
func (s *Server) fulfillTopupOrder(ctx context.Context, outTradeNo string) error {
	if s.Credits == nil || s.Subscriptions == nil {
		return fmt.Errorf("topup fulfill: credits/subscriptions nil")
	}
	pool := s.Subscriptions.Pool()
	var (
		userID    uuid.UUID
		orderType string
		metaRaw   []byte
		fulfilled *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT user_id, order_type, metadata, paid_at
		FROM billing.payment_orders
		WHERE provider_order_id = $1 AND status = 'succeeded'
	`, outTradeNo).Scan(&userID, &orderType, &metaRaw, &fulfilled)
	if err != nil {
		return fmt.Errorf("query order: %w", err)
	}
	if orderType != "topup" {
		return nil // subscription 走 W5-7 路径, 不在这里兑现
	}

	var meta struct {
		OptionID      string `json:"option_id"`
		Kind          string `json:"kind"`
		CreditsAmount int64  `json:"credits_amount"`
		ValidDays     int    `json:"valid_days"`
		DisplayName   string `json:"display_name"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return fmt.Errorf("decode metadata: %w", err)
	}
	if meta.CreditsAmount <= 0 {
		return fmt.Errorf("invalid credits_amount in metadata")
	}

	args := credits.GrantArgs{
		UserID:         userID,
		Amount:         meta.CreditsAmount,
		Kind:           credits.PackageKind(meta.Kind),
		Source:         credits.SourceRecharge,
		Remark:         fmt.Sprintf("recharge: %s (order=%s)", meta.DisplayName, outTradeNo),
		IdempotencyKey: "topup:" + outTradeNo,
	}
	if meta.Kind == "time_limited" && meta.ValidDays > 0 {
		exp := time.Now().Add(time.Duration(meta.ValidDays) * 24 * time.Hour)
		args.ExpiresAt = &exp
	}
	_, _, err = s.Credits.Grant(ctx, args)
	if err != nil {
		return fmt.Errorf("grant: %w", err)
	}
	return nil
}

// ─── shared formatters ───────────────────────────────

func balanceOut(b *credits.Balance) map[string]any {
	out := map[string]any{
		"user_id":              b.UserID,
		"permanent_balance":    b.PermanentBalance,
		"time_limited_balance": b.TimeLimitedBalance,
		"total":                b.Total(),
		"updated_at":           b.UpdatedAt,
	}
	if b.TimeLimitedEarliestExpires != nil {
		out["time_limited_earliest_expires"] = *b.TimeLimitedEarliestExpires
	}
	return out
}

func packageOut(p *credits.Package) map[string]any {
	out := map[string]any{
		"id":             p.ID,
		"kind":           string(p.Kind),
		"source":         string(p.Source),
		"initial_amount": p.InitialAmount,
		"remaining":      p.Remaining,
		"created_at":     p.CreatedAt,
	}
	if p.ExpiresAt != nil {
		out["expires_at"] = *p.ExpiresAt
	}
	return out
}

func logOut(l *credits.Log) map[string]any {
	out := map[string]any{
		"id":             l.ID,
		"delta":          l.Delta,
		"balance_after":  l.BalanceAfter,
		"ref_type":       string(l.RefType),
		"ref_id":         l.RefID,
		"remark":         l.Remark,
		"created_at":     l.CreatedAt,
	}
	if l.RefundOfLogID != nil {
		out["refund_of_log_id"] = *l.RefundOfLogID
	}
	if len(l.ConsumeBreakdown) > 0 {
		out["consume_breakdown"] = l.ConsumeBreakdown
	}
	return out
}

func paginationFromQuery(q map[string][]string) (limit, offset int) {
	limit = 50
	if v := firstQueryParam(q, "limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := firstQueryParam(q, "offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

func firstQueryParam(q map[string][]string, key string) string {
	if v, ok := q[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}
