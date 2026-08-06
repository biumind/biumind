package adminapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// listModels 默认分页. mode=all 时全表 3000+ 条, 不分页 admin Vue 表会卡死.
const (
	defaultModelsPageSize = 50
	maxModelsPageSize     = 200
)

// GET /v1/admin/models?status=active&family=claude&min_plan=pro&q=sonnet&mode=chat&include_pricing=true
//
// mode 接受单值或逗号分隔多值 (P4 follow-up F1):
//   ?mode=image_generation
//   ?mode=image_generation,video_generation,digital_human,hotparse
//
// include_pricing=true (F2.1) 一次 SQL 批量拉每个 model 最新 pricing,
// 给 admin Vue 列表显示价格用. 默认 false 减少返回 payload 大小.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, pageSize := parsePaging(q, defaultModelsPageSize, maxModelsPageSize)
	f := registry.ModelFilter{
		Status:  registry.EntityStatus(q.Get("status")),
		Family:  q.Get("family"),
		MinPlan: registry.Plan(q.Get("min_plan")),
		Search:  q.Get("q"),
		Mode:    q.Get("mode"),
		Limit:   pageSize,
		Offset:  (page - 1) * pageSize,
	}
	items, err := s.Store.Models.List(r.Context(), f)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	// 全量 total 走 Count, 分页 UI 才能算总页数.
	total, err := s.Store.Models.Count(r.Context(), f)
	if err != nil {
		translateRegistryError(w, err)
		return
	}

	resp := map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}

	if q.Get("include_pricing") == "true" && len(items) > 0 {
		ids := make([]uuid.UUID, 0, len(items))
		for _, m := range items {
			ids = append(ids, m.ID)
		}
		pricings, err := s.Store.Pricing.BatchLatest(r.Context(), ids)
		if err != nil {
			// 不阻塞 list, 只 warn (admin UI 显示 0 价格而非全报错)
			s.Logger.Warn("listModels include_pricing batch failed", "err", err)
		} else {
			// 转 model_id -> Pricing 投影成 map[code]Pricing 便前端 join
			byCode := make(map[string]registry.Pricing, len(pricings))
			for _, m := range items {
				if p, ok := pricings[m.ID]; ok {
					byCode[m.Code] = p
				}
			}
			resp["pricings"] = byCode
		}
	}

	// include_channel_stats=true: 一次 SQL 批量统计当前页 model 的渠道计数,
	// 替代前端原本的 listChannels() 全表拉 + 客户端 group by.
	if q.Get("include_channel_stats") == "true" && len(items) > 0 {
		ids := make([]uuid.UUID, 0, len(items))
		for _, m := range items {
			ids = append(ids, m.ID)
		}
		stats, err := s.Store.Channels.StatsByModelIDs(r.Context(), ids)
		if err != nil {
			s.Logger.Warn("listModels include_channel_stats batch failed", "err", err)
		} else {
			byCode := make(map[string]registry.ChannelStats, len(items))
			for _, m := range items {
				// 没数据的 model 也输出零值, 前端少一次 ?? 兜底
				byCode[m.Code] = stats[m.ID]
			}
			resp["channel_stats"] = byCode
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// parsePaging 解析 page (>=1) 和 page_size (1..max), 给默认值兜底.
func parsePaging(q url.Values, defSize, maxSize int) (page, pageSize int) {
	page = 1
	pageSize = defSize
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			pageSize = n
		}
	}
	if pageSize > maxSize {
		pageSize = maxSize
	}
	return page, pageSize
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	got, err := s.Store.Models.Get(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	groups, err := s.Store.Groups.ListGroupsForModel(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	// Surface the model + group bindings together — admin detail panel
	// shows both side by side.
	writeJSON(w, http.StatusOK, map[string]any{
		"model":  got,
		"groups": groups,
	})
}

type modelRequest struct {
	Code            string                   `json:"code"`
	DisplayName     string                   `json:"display_name"`
	Family          string                   `json:"family"`
	ContextWindow   int                      `json:"context_window"`
	MaxOutput       int                      `json:"max_output"`
	Capabilities    registry.Capabilities    `json:"capabilities"`
	MinPlan         registry.Plan            `json:"min_plan"`
	Status          registry.EntityStatus    `json:"status"`
	SortOrder       int                      `json:"sort_order"`
	RoutingStrategy registry.RoutingStrategy `json:"routing_strategy"`
	ManualOverride  bool                     `json:"manual_override"`
	Mode            string                   `json:"mode"`
}

func (req modelRequest) toInput() registry.ModelInput {
	return registry.ModelInput{
		Code: req.Code, DisplayName: req.DisplayName, Family: req.Family,
		ContextWindow: req.ContextWindow, MaxOutput: req.MaxOutput,
		Capabilities: req.Capabilities,
		MinPlan:      req.MinPlan, Status: req.Status, SortOrder: req.SortOrder,
		RoutingStrategy: req.RoutingStrategy, ManualOverride: req.ManualOverride,
		Mode: req.Mode,
	}
}

func (s *Server) handleCreateModel(w http.ResponseWriter, r *http.Request) {
	var req modelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// New models are manual_override=true by default — admin-created
	// models should NOT be overwritten by sync-upstream. The request
	// can still set false if the admin wants the next sync to
	// re-baseline this row.
	if !req.ManualOverride {
		req.ManualOverride = true
	}
	got, err := s.Store.Models.Insert(r.Context(), req.toInput())
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	// Auto-bind to default group so visibility filter doesn't reject
	// the brand-new model on next request.
	if err := s.Store.Groups.SetModelBindings(r.Context(), got.ID,
		[]uuid.UUID{registry.DefaultGroupID}); err != nil {
		s.Logger.Warn("admin: auto-bind default group failed",
			"model_id", got.ID, "err", err.Error())
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req modelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	got, err := s.Store.Models.Update(r.Context(), id, req.toInput())
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	// channels FK is CASCADE — deleting a model cleans up its channels.
	// pricing FK is also CASCADE so historical pricing rows go with it.
	// We don't go further (e.g. usage_log) because that's an audit log.
	if err := s.Store.Models.Delete(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrConflict) {
			writeError(w, http.StatusConflict, "model_in_use", err.Error())
			return
		}
		translateRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/admin/models/{id}/bind-groups
type bindGroupsRequest struct {
	GroupIDs []string `json:"group_ids"`
}

func (s *Server) handleBindModelGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req bindGroupsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	gids := make([]uuid.UUID, 0, len(req.GroupIDs))
	for _, raw := range req.GroupIDs {
		g, err := uuid.Parse(raw)
		if err != nil {
			writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
				"group_id not a UUID: "+raw, "group_ids")
			return
		}
		gids = append(gids, g)
	}
	if err := s.Store.Groups.SetModelBindings(r.Context(), id, gids); err != nil {
		translateRegistryError(w, err)
		return
	}
	groups, _ := s.Store.Groups.ListGroupsForModel(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"model_id": id,
		"groups":   groups,
	})
}
