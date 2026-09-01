// usage_charge.go — /v1/internal/usage/charge：非 LLM 处理动作的计费代理
// （client-docproc W4，云端 wiki 解析按页扣费）。
//
// 为什么放在 model-relay：model_relay.pricing 是价格单一 SoT，而它的
// 消费者只有 model-relay 进程内（PriceLookuper）。brain 等服务不直连
// pricing 表、也不直连 identity credit-holds —— 统一经本端点计费，
// 与「业务服务过 model-relay」的架构取向一致（I6 精神延伸到计费面）。
//
// 语义：调用方在完成处理动作后带上**真实用量**（如 page_count），这里
// 查价 → Hold+Settle 即时结算（金额已知，不需要预估窗口）。dry_run=true
// 只报价不扣费（preflight 用）。pricing 缺行 → 对齐现有兜底：charged=0
// + pricing_missing:true（warn 不 402，与 lookuper 全 0 语义一致）。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/billing"
)

// UsageChargeBiller 是 charge 端点需要的最小计费面（*billing.Client 实现）。
type UsageChargeBiller interface {
	LookupPrice(ctx context.Context, refType, pricingKey string) (*billing.PricingEntry, error)
	Hold(ctx context.Context, args billing.HoldArgs) (*billing.Hold, error)
	Settle(ctx context.Context, holdID string, actualAmount int64, remark string) error
	Release(ctx context.Context, holdID string) error
}

type UsageChargeHandler struct {
	Billing UsageChargeBiller
	Logger  *slog.Logger
}

type usageChargeReq struct {
	UserID         string `json:"user_id"`
	Model          string `json:"model"`           // pricing 挂载的（pseudo-）model code，如 wiki-parse-text
	RefType        string `json:"ref_type"`        // parse_page / audio_transcription / ...
	Quantity       int64  `json:"quantity"`        // 真实用量（页数/秒数/…，按 ref_type 的 cost_basis 解释）
	IdempotencyKey string `json:"idempotency_key"` // 必填防重（如 parse:<source_id>）
	Remark         string `json:"remark,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

func (h *UsageChargeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var req usageChargeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.UserID == "" || req.Model == "" || req.RefType == "" ||
		req.Quantity <= 0 || req.IdempotencyKey == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_fields",
			"user_id, model, ref_type, quantity>0, idempotency_key required")
		return
	}
	if h.Billing == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "billing_not_configured", "")
		return
	}
	entry, err := h.Billing.LookupPrice(r.Context(), req.RefType, req.Model)
	if err != nil {
		if errors.Is(err, billing.ErrPricingNotFound) {
			// 对齐 modality_billing 缺价兜底：warn + 零扣（不 402），避免
			// 未配价格时处理管线被计费卡死；运维侧靠 pricing_missing 发现。
			logger.Warn("usage charge: pricing missing, zero charge",
				"ref_type", req.RefType, "model", req.Model)
			writeJSON(w, http.StatusOK, map[string]any{
				"charged_amount":  0,
				"pricing_missing": true,
			})
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "pricing_error", err.Error())
		return
	}
	amount := entry.CalculateRerank(req.Quantity) // per-unit × qty × markup ± clamp（per_search_unit/per_page 同构）
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{
			"charged_amount": amount,
			"dry_run":        true,
		})
		return
	}
	hold, err := h.Billing.Hold(r.Context(), billing.HoldArgs{
		UserID:         req.UserID,
		MaxAmount:      amount,
		RefType:        "wiki_parse_request",
		RefID:          req.IdempotencyKey,
		IdempotencyKey: req.IdempotencyKey,
		ModelCode:      req.Model,
	})
	if err != nil {
		if errors.Is(err, billing.ErrInsufficient) {
			writeJSONErr(w, http.StatusPaymentRequired, "insufficient_credits", "")
			return
		}
		writeJSONErr(w, http.StatusBadGateway, "billing_error", err.Error())
		return
	}
	if err := h.Billing.Settle(r.Context(), hold.ID, amount, req.Remark); err != nil {
		// settle 失败尽量释放，避免 hold 泄漏到 TTL。
		_ = h.Billing.Release(r.Context(), hold.ID)
		writeJSONErr(w, http.StatusBadGateway, "billing_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"charged_amount": amount,
		"hold_id":        hold.ID,
	})
}
