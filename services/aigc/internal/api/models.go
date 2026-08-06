package api

// models.go — 模型字典 / 供应商查询 endpoints (P2-6).
//
//   GET /v1/models?type=image|video|digital_human|hotparse   (公开)
//   GET /v1/providers                                         (admin only, 走 authz)
//
// /v1/models 公开是因为前端「创作」首页需要在用户未登录时也能展示模型选项
// (登录后才能提交)。这是 zhiying 的对齐: tb_ai_model 字典也是公开 GET.

import (
	"encoding/json"
	"net/http"

	"github.com/biumind/biumind/services/aigc/internal/authz"
	"github.com/biumind/biumind/services/aigc/internal/store"
)

// ─── /v1/models ──────────────────────────────────────

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	typ := firstQ(r.URL.Query(), "type")
	switch typ {
	case "", "image", "video", "digital_human", "hotparse":
		// ok
	default:
		writeErr(w, http.StatusBadRequest, "bad_type", "type must be image|video|digital_human|hotparse")
		return
	}

	// includeDisabled 仅对 admin 暴露; 这里固定 false (公开端点).
	models, err := s.Store.ListModels(r.Context(), typ, false)
	if writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models": projectModels(models),
	})
}

// P4.S3.6: handleListProviders 已下线 — provider 字典统一到 model-relay.

// ─── helpers ─────────────────────────────────────────

// decider 返回 authz Decider, nil 时退化为 AlwaysAllow (dev). main.go 应在
// AUTHZ_URL 空时 log warning.
func (s *Server) decider() authz.Decider {
	if s.Authz == nil {
		return authz.AlwaysAllow{}
	}
	return s.Authz
}

// projectModels 把 store.Model 切片投影成 client 能消费的 JSON.
// config / pricing_rule 直接透传 raw jsonb 给 Flutter 解析.
func projectModels(ms []*store.Model) []map[string]any {
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]any{
			"code":          m.Code,
			"type":          m.Type,
			"display_name":  m.DisplayName,
			"provider_code": m.ProviderCode,
			"price_credits": m.PriceCredits,
			"pricing_rule":  rawJSONOrEmpty(m.PricingRule),
			"config":        rawJSONOrEmpty(m.Config),
			"sort_order":    m.SortOrder,
		})
	}
	return out
}

// rawJSONOrEmpty 把 raw []byte 当 RawMessage 透传; 空时返 nil 让 client 看到 null.
func rawJSONOrEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}
