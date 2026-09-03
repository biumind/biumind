package internalapi

// settings.go — 服务间 settings endpoint, 供 ingest worker 按 owner 拉偏好.
//
//   GET /v1/internal/settings/{user_id}/ingest-model   → 200 {"model":"..."}；未设置 404
//
// 鉴权: 共享 bearer (IDENTITY_INTERNAL_TOKEN), 与 BYOK / credits 内部端点同机制.
// 404 = 用户未设置偏好, 与 BYOK 未配置语义一致 —— 消费方回落默认模型, 不当错误.

import (
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/identity/internal/settings"
	"github.com/google/uuid"
)

// MountSettings 把 settings 路由挂到 mux. store=nil 时跳过.
func (s *Server) MountSettings(mux *http.ServeMux, store *settings.Store) {
	if store == nil {
		return
	}
	s.Settings = store
	mux.HandleFunc("GET /v1/internal/settings/{user_id}/ingest-model",
		s.requireToken(s.handleIngestModelGet))
}

func (s *Server) handleIngestModelGet(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		http.Error(w, "settings not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	model, err := s.Settings.GetIngestModel(r.Context(), uid)
	if errors.Is(err, settings.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONOK(w, map[string]any{"model": model})
}
