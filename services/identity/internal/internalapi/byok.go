package internalapi

// byok.go — 服务间 BYOK endpoint, 仅供 model-relay / aigc-worker 调用.
//
//   GET  /v1/internal/byok/{user_id}/{provider}              返明文 + config
//   POST /v1/internal/byok/{user_id}/{provider}/incr-failure
//   POST /v1/internal/byok/{user_id}/{provider}/touch-used
//
// 鉴权: 共享 bearer (HUB_INTERNAL_TOKEN). 这些端点会返回明文 API Key,
// 必须通过 NetworkPolicy 限制只有受信 Pod 能访问.
//
// client-side BYOK (is_client_side=true) 的记录被 store.GetDecrypted /
// MatchCustomByModel 的 WHERE 过滤 (is_client_side=false), relay 永远看不到
// 此类记录 —— 其 key 仅存客户端 keychain, 服务端不持有明文.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/identity/internal/byok"
	"github.com/google/uuid"
)

// MountBYOK 把 BYOK 路由挂到 mux. store=nil 时跳过.
func (s *Server) MountBYOK(mux *http.ServeMux, store *byok.Store) {
	if store == nil {
		return
	}
	s.BYOK = store
	mux.HandleFunc("GET /v1/internal/byok/{user_id}/{provider}",
		s.requireToken(s.handleBYOKGet))
	mux.HandleFunc("GET /v1/internal/byok/{user_id}/match",
		s.requireToken(s.handleBYOKMatch))
	mux.HandleFunc("POST /v1/internal/byok/{user_id}/{provider}/incr-failure",
		s.requireToken(s.handleBYOKIncrFailure))
	mux.HandleFunc("POST /v1/internal/byok/{user_id}/{provider}/touch-used",
		s.requireToken(s.handleBYOKTouchUsed))
}

func (s *Server) handleBYOKGet(w http.ResponseWriter, r *http.Request) {
	if s.BYOK == nil {
		http.Error(w, "byok not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	provider := r.PathValue("provider")
	plaintext, cfg, baseURL, protocol, err := s.BYOK.GetDecrypted(r.Context(), uid, provider)
	if errors.Is(err, byok.ErrKeyNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := map[string]any{
		"api_key": plaintext,
		"config":  json.RawMessage(cfg),
	}
	if baseURL != "" {
		out["base_url"] = baseURL
	}
	if protocol != "" {
		out["protocol"] = protocol
	}
	writeJSONOK(w, out)
}

// handleBYOKMatch — model-relay CredsResolver 在 catalog 失败时调:
// 按 model 匹配用户的 custom BYOK 记录. GET /v1/internal/byok/{user_id}/match?model=X
// 返 {api_key, base_url, protocol, config}; 无命中 404.
func (s *Server) handleBYOKMatch(w http.ResponseWriter, r *http.Request) {
	if s.BYOK == nil {
		http.Error(w, "byok not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		http.Error(w, "missing model", http.StatusBadRequest)
		return
	}
	plaintext, cfg, baseURL, protocol, err := s.BYOK.MatchCustomByModel(r.Context(), uid, model)
	if errors.Is(err, byok.ErrKeyNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := map[string]any{
		"api_key": plaintext,
		"config":  json.RawMessage(cfg),
	}
	if baseURL != "" {
		out["base_url"] = baseURL
	}
	if protocol != "" {
		out["protocol"] = protocol
	}
	writeJSONOK(w, out)
}

func (s *Server) handleBYOKIncrFailure(w http.ResponseWriter, r *http.Request) {
	if s.BYOK == nil {
		http.Error(w, "byok not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	provider := r.PathValue("provider")
	autoInvalid, err := s.BYOK.IncrementFailure(r.Context(), uid, provider)
	if errors.Is(err, byok.ErrKeyNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONOK(w, map[string]any{"auto_invalid": autoInvalid})
}

func (s *Server) handleBYOKTouchUsed(w http.ResponseWriter, r *http.Request) {
	if s.BYOK == nil {
		http.Error(w, "byok not wired", http.StatusServiceUnavailable)
		return
	}
	uid, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user_id", http.StatusBadRequest)
		return
	}
	provider := r.PathValue("provider")
	if err := s.BYOK.TouchUsed(r.Context(), uid, provider); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
