// pricing_rules.go — Phase 4 段 4 (F2.1) admin endpoints.
//
//   GET  /v1/admin/models/{id}/pricing-rules   返历史规则数组 (newest first)
//   POST /v1/admin/models/{id}/pricing-rules   append 新规则
//
// 与 services/model-relay/internal/registry/pricing.go PricingRulesRepo 配套.
// admin Vue 编辑 by_duration × by_resolution 多维乘数表时调用.

package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func (s *Server) handleListPricingRules(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	rules, err := s.Store.PricingRules.History(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rules, "total": len(rules),
	})
}

type pricingRuleRequest struct {
	RuleJSON json.RawMessage `json:"rule_jsonb"`
}

func (s *Server) handleAppendPricingRule(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req pricingRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.RuleJSON) == 0 || string(req.RuleJSON) == "null" {
		writeError(w, http.StatusBadRequest, "invalid_input", "rule_jsonb required")
		return
	}
	// 校验:确保 rule_jsonb 至少能被 unmarshal 成 object (防 admin 输入坏数据)
	var probe map[string]any
	if err := json.Unmarshal(req.RuleJSON, &probe); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"rule_jsonb must be a JSON object: "+err.Error())
		return
	}

	// 校验 model 存在
	if _, err := s.Store.Models.Get(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "model_not_found", "")
			return
		}
		translateRegistryError(w, err)
		return
	}

	out, err := s.Store.PricingRules.Append(r.Context(), registry.PricingRuleInput{
		ModelID:  id,
		RuleJSON: req.RuleJSON,
	})
	if err != nil {
		translateRegistryError(w, err)
		return
	}

	// 同时把 model.pricing_strategy 升级为 'parameter' (若当前非 parameter).
	// 这是 admin 显式加规则的语义 — 字典侧应跟上.
	_, _ = s.Store.Pool.Exec(r.Context(),
		`UPDATE model_relay.models
		 SET pricing_strategy = 'parameter', updated_at = now()
		 WHERE id = $1 AND pricing_strategy != 'parameter'`, id)

	writeJSON(w, http.StatusCreated, out)
}

// parseUUIDPath 是 adminapi 包内 helper, 在 helpers.go 已声明; 这里只是
// 让 IDE 跳转友好的占位.
var _ = uuid.Nil
