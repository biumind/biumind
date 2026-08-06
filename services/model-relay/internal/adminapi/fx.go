package adminapi

import (
	"net/http"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/fx-rates → all rate rows + a "stalest" hint so the UI
// banner can read it directly.
func (s *Server) handleListFxRates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.FxRates.List(r.Context())
	if err != nil {
		translateRegistryError(w, err)
		return
	}

	// "Stalest" is computed in DB; if no manual rows exist we suppress
	// the hint instead of returning an error.
	stalest, age, err := s.Store.FxRates.StalestRate(r.Context())
	resp := map[string]any{"items": rows, "total": len(rows)}
	if err == nil {
		resp["stalest"] = stalest
		resp["stalest_age_seconds"] = int(age / time.Second)
	}
	writeJSON(w, http.StatusOK, resp)
}

type fxRateRequest struct {
	FromCurrency registry.Currency `json:"from_currency"`
	ToCurrency   registry.Currency `json:"to_currency"`
	Rate         float64           `json:"rate"`
	Source       string            `json:"source"`
}

// PUT /v1/admin/fx-rates → upsert one (from, to) pair.
func (s *Server) handleSetFxRate(w http.ResponseWriter, r *http.Request) {
	var req fxRateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Source == "" {
		req.Source = "manual"
	}
	got, err := s.Store.FxRates.Upsert(r.Context(), registry.FxRateUpsert{
		FromCurrency: req.FromCurrency,
		ToCurrency:   req.ToCurrency,
		Rate:         req.Rate,
		Source:       req.Source,
		UpdatedBy:    actorIDFromCtx(r),
	})
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}
