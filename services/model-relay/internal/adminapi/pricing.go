package adminapi

import (
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/pricing/{model_id} → current effective row.
func (s *Server) handleGetPricing(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "model_id")
	if !ok {
		return
	}
	got, err := s.Store.Pricing.GetCurrent(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// No pricing yet — return an empty row instead of 404 so
			// the admin form can still render.
			writeJSON(w, http.StatusOK, map[string]any{
				"model_id": id,
				"currency": registry.CurrencyUSD,
			})
			return
		}
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// GET /v1/admin/pricing/{model_id}/history
func (s *Server) handleGetPricingHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "model_id")
	if !ok {
		return
	}
	rows, err := s.Store.Pricing.History(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rows, "total": len(rows),
	})
}

type pricingRequest struct {
	Currency          registry.Currency `json:"currency"`
	InputPerMTok      float64           `json:"input_per_mtok"`
	OutputPerMTok     float64           `json:"output_per_mtok"`
	CacheWritePerMTok float64           `json:"cache_write_per_mtok"`
	CacheReadPerMTok  float64           `json:"cache_read_per_mtok"`
	// P4 段 2 多模态字段. 指针语义与 registry.PricingInput 对齐:
	// 未传 → nil → 落库 NULL (不适用, 与 SQL 手录一致); 显式传 0 落库 0,
	// 计费端 (billing/local lookuper) 对 nil 与 <=0 同样返回
	// ErrPricingNotFound, 两种形态下游行为等价.
	CostPerImage       *float64 `json:"cost_per_image"`
	CostPerVideoSecond *float64 `json:"cost_per_video_second"`
	CostPerAudioSecond *float64 `json:"cost_per_audio_second"`
	CostPerCharacter   *float64 `json:"cost_per_character"`
	// 通用按单元价格: rerank 按 search_unit / wiki 解析按页复用此列.
	// 0 = 不适用 (与 DB DEFAULT 0 口径一致).
	CostPerSearchUnit float64 `json:"cost_per_search_unit"`
}

// POST /v1/admin/pricing/{model_id}
//
// Pricing is append-only: each POST inserts a new row with effective_at=now().
// Older rows remain for audit / retroactive billing.
func (s *Server) handleSetPricing(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "model_id")
	if !ok {
		return
	}
	var req pricingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	creator := actorIDFromCtx(r)
	got, err := s.Store.Pricing.Set(r.Context(), registry.PricingInput{
		ModelID:            id,
		Currency:           req.Currency,
		InputPerMTok:       req.InputPerMTok,
		OutputPerMTok:      req.OutputPerMTok,
		CacheWritePerMTok:  req.CacheWritePerMTok,
		CacheReadPerMTok:   req.CacheReadPerMTok,
		CostPerImage:       req.CostPerImage,
		CostPerVideoSecond: req.CostPerVideoSecond,
		CostPerAudioSecond: req.CostPerAudioSecond,
		CostPerCharacter:   req.CostPerCharacter,
		CostPerSearchUnit:  req.CostPerSearchUnit,
		CreatedBy:          creator,
	})
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

// actorIDFromCtx pulls the user id out of the JWT claims so the audit
// columns (pricing.created_by, fx_rates.updated_by) are populated.
// Returns nil pointer if claims missing — DB column is nullable.
func actorIDFromCtx(r *http.Request) *uuid.UUID {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok || claims.UserID == "" {
		return nil
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil
	}
	return &id
}
