package internalapi

// credits.go — 服务间积分操作 endpoint, 供 services/aigc 等同集群服务调用.
//
//   POST /v1/internal/credits/consume    扣减
//   POST /v1/internal/credits/refund     退款（按原 log 反向回填）
//   POST /v1/internal/credits/grant      入账（充值/赠送/活动）
//   GET  /v1/internal/credits/{user_id}/balance   余额查询
//
// 鉴权: 共享 bearer token (HUB_INTERNAL_TOKEN), 同 plan 端点. 网络层 NetworkPolicy
// 限制只能集群内访问, token 是 defence-in-depth.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/biumind/biumind/services/identity/internal/credits"
	"github.com/google/uuid"
)

// MountCredits 把 credits 路由挂到给定 mux. 必须在 New 之后调用一次.
// svc=nil 时不挂任何路由（service-to-service credits 接口对外完全不可见）.
func (s *Server) MountCredits(mux *http.ServeMux, svc *credits.Service) {
	if svc == nil {
		return
	}
	s.Credits = svc
	mux.HandleFunc("POST /v1/internal/credits/consume",
		s.requireToken(s.handleCreditsConsume))
	mux.HandleFunc("POST /v1/internal/credits/refund",
		s.requireToken(s.handleCreditsRefund))
	mux.HandleFunc("POST /v1/internal/credits/grant",
		s.requireToken(s.handleCreditsGrant))
	mux.HandleFunc("GET /v1/internal/credits/{user_id}/balance",
		s.requireToken(s.handleCreditsBalance))
	// W1: 流式预扣 / 结算 / 释放 (chat / agent 用).
	// 路径用 'credit-holds' 子前缀避免与 /v1/internal/credits/{user_id}/balance
	// 在 net/http 1.22 严格模式下歧义.
	mux.HandleFunc("POST /v1/internal/credit-holds",
		s.requireToken(s.handleCreditsHold))
	mux.HandleFunc("POST /v1/internal/credit-holds/{hold_id}/settle",
		s.requireToken(s.handleCreditsSettle))
	mux.HandleFunc("POST /v1/internal/credit-holds/{hold_id}/release",
		s.requireToken(s.handleCreditsRelease))
	mux.HandleFunc("GET /v1/internal/credit-holds/{hold_id}",
		s.requireToken(s.handleCreditsGetHold))
	// pricing 查询端点已下线 (W4 SoT 整合): 价格数据从 billing.pricing_book
	// 迁到 model_relay.pricing,model-relay 直接本地查不再绕 identity.
	// pricing_book 表 + handler 在 0030 / 0031 migration 删表 + handler 删码.
}

// ─── handlers ─────────────────────────────────────────

type consumeReqBody struct {
	UserID         string `json:"user_id"`
	Amount         int64  `json:"amount"`
	RefType        string `json:"ref_type"`
	RefID          string `json:"ref_id,omitempty"`
	Remark         string `json:"remark,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// W3-7: 仅透传到 NATS 事件 (dashboard 模型分布 / 毛利率统计用), 不进 DB.
	ModelCode    string  `json:"model_code,omitempty"`
	ProviderCode string  `json:"provider_code,omitempty"`
	UpstreamUSD  float64 `json:"upstream_usd,omitempty"`
	UpstreamCNY  float64 `json:"upstream_cny,omitempty"`
}

func (s *Server) handleCreditsConsume(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	var body consumeReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	uid, err := uuid.Parse(body.UserID)
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	log, bal, err := s.Credits.Consume(r.Context(), credits.ConsumeArgs{
		UserID:         uid,
		Amount:         body.Amount,
		RefType:        credits.LogRefType(body.RefType),
		RefID:          body.RefID,
		Remark:         body.Remark,
		IdempotencyKey: body.IdempotencyKey,
		ModelCode:      body.ModelCode,
		ProviderCode:   body.ProviderCode,
		UpstreamUSD:    body.UpstreamUSD,
		UpstreamCNY:    body.UpstreamCNY,
	})
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"log": log, "balance": bal})
}

type refundReqBody struct {
	OriginalLogID  string `json:"original_log_id"`
	Amount         int64  `json:"amount"`
	Remark         string `json:"remark,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (s *Server) handleCreditsRefund(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	var body refundReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	origID, err := uuid.Parse(body.OriginalLogID)
	if err != nil {
		http.Error(w, "bad original_log_id", http.StatusBadRequest)
		return
	}
	log, bal, err := s.Credits.Refund(r.Context(), credits.RefundArgs{
		OriginalLogID:  origID,
		Amount:         body.Amount,
		Remark:         body.Remark,
		IdempotencyKey: body.IdempotencyKey,
	})
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"log": log, "balance": bal})
}

type grantReqBody struct {
	UserID         string `json:"user_id"`
	Amount         int64  `json:"amount"`
	Kind           string `json:"kind"`                    // permanent | time_limited
	Source         string `json:"source"`                  // recharge | plan_grant | reward | refund | admin
	ExpiresAtMS    int64  `json:"expires_at_ms,omitempty"` // unix ms; 0 = nil
	Remark         string `json:"remark,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (s *Server) handleCreditsGrant(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	var body grantReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	uid, err := uuid.Parse(body.UserID)
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	args := credits.GrantArgs{
		UserID:         uid,
		Amount:         body.Amount,
		Kind:           credits.PackageKind(body.Kind),
		Source:         credits.PackageSource(body.Source),
		Remark:         body.Remark,
		IdempotencyKey: body.IdempotencyKey,
	}
	if body.ExpiresAtMS > 0 {
		exp := time.UnixMilli(body.ExpiresAtMS)
		args.ExpiresAt = &exp
	}
	pkg, bal, err := s.Credits.Grant(r.Context(), args)
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"package": pkg, "balance": bal})
}

func (s *Server) handleCreditsBalance(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	bal, err := s.Credits.GetBalance(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONOK(w, map[string]any{"balance": bal})
}

// ─── Hold / Settle / Release ──────────────────────────

type holdReqBody struct {
	UserID         string `json:"user_id"`
	MaxAmount      int64  `json:"max_amount"`
	RefType        string `json:"ref_type"`
	RefID          string `json:"ref_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	TTLSeconds     int    `json:"ttl_seconds,omitempty"`

	// W3-7: 透传到 HoldEvent. Settle 暂不带 model_code (Settle 没接口字段)
	ModelCode    string `json:"model_code,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
}

func (s *Server) handleCreditsHold(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	var body holdReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	uid, err := uuid.Parse(body.UserID)
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	args := credits.HoldArgs{
		UserID:         uid,
		MaxAmount:      body.MaxAmount,
		RefType:        credits.LogRefType(body.RefType),
		RefID:          body.RefID,
		IdempotencyKey: body.IdempotencyKey,
		ModelCode:      body.ModelCode,
		ProviderCode:   body.ProviderCode,
	}
	if body.TTLSeconds > 0 {
		args.TTL = time.Duration(body.TTLSeconds) * time.Second
	}
	hold, bal, err := s.Credits.Hold(r.Context(), args)
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"hold": hold, "balance": bal})
}

type settleReqBody struct {
	ActualAmount int64  `json:"actual_amount"`
	Remark       string `json:"remark,omitempty"`
}

func (s *Server) handleCreditsSettle(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	holdID, err := uuid.Parse(r.PathValue("hold_id"))
	if err != nil {
		http.Error(w, "bad hold_id", http.StatusBadRequest)
		return
	}
	var body settleReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hold, log, bal, err := s.Credits.Settle(r.Context(), credits.SettleArgs{
		HoldID:       holdID,
		ActualAmount: body.ActualAmount,
		Remark:       body.Remark,
	})
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"hold": hold, "log": log, "balance": bal})
}

func (s *Server) handleCreditsRelease(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	holdID, err := uuid.Parse(r.PathValue("hold_id"))
	if err != nil {
		http.Error(w, "bad hold_id", http.StatusBadRequest)
		return
	}
	hold, bal, err := s.Credits.Release(r.Context(), holdID)
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"hold": hold, "balance": bal})
}

func (s *Server) handleCreditsGetHold(w http.ResponseWriter, r *http.Request) {
	if s.Credits == nil {
		http.Error(w, "credits not wired", http.StatusServiceUnavailable)
		return
	}
	holdID, err := uuid.Parse(r.PathValue("hold_id"))
	if err != nil {
		http.Error(w, "bad hold_id", http.StatusBadRequest)
		return
	}
	hold, err := s.Credits.GetHold(r.Context(), holdID)
	if writeCreditErr(w, err) {
		return
	}
	writeJSONOK(w, map[string]any{"hold": hold})
}

// ─── helpers ──────────────────────────────────────────

func writeCreditErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, credits.ErrInsufficientCredits):
		http.Error(w, err.Error(), http.StatusPaymentRequired)
	case errors.Is(err, credits.ErrInvalidAmount),
		errors.Is(err, credits.ErrInvalidKindExpiresAt),
		errors.Is(err, credits.ErrAmountExceedsOriginal),
		errors.Is(err, credits.ErrInvalidHoldRefType),
		errors.Is(err, credits.ErrSettleExceedsHold):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, credits.ErrLogNotFound),
		errors.Is(err, credits.ErrHoldNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, credits.ErrLogIsNotConsumption),
		errors.Is(err, credits.ErrAllPackagesExpired),
		errors.Is(err, credits.ErrHoldNotActive):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return true
}

func writeJSONOK(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
