// models.go — GET /v1/internal/models/{default-chat,preferred-chat}.
//
// brain's ChatRunner used to hardcode a fallback model code; now the
// platform default chat model is an admin-managed flag
// (models.is_default_chat, migration 00002) and brain pulls it here at
// resolve time. preferred-chat is the next rung of that fallback chain:
// when no admin default exists it auto-picks the best usable chat model
// (registry.Cache.PreferredChatModel). Same bearer middleware as
// /v1/internal/chat.
//
// Response:
//
//	200 {"code": "<models.code>"}
//	404 plain-text error when no default is set / no usable chat model
//	    exists (a deactivated model counts as absent).
//	503 when the registry cache is not wired.
//
// The lookup rides the registry Cache (LISTEN/NOTIFY + TTL), so flag
// changes propagate without a restart.

package internalapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// handleDefaultChatModel returns the admin-designated default chat
// model code. Shape kept minimal — brain only needs the code to put in
// the `model` request field.
func (s *Server) handleDefaultChatModel(w http.ResponseWriter, r *http.Request) {
	if s.Cache == nil {
		http.Error(w, "registry cache not wired", http.StatusServiceUnavailable)
		return
	}
	m, err := s.Cache.DefaultChatModel(r.Context())
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			http.Error(w, "no default chat model", http.StatusNotFound)
			return
		}
		http.Error(w, "default chat lookup failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": m.Code})
}

// handlePreferredChatModel returns the auto-picked best usable chat
// model code (mode=chat, status=active, sort_order ASC then code ASC).
// Used as the rung below the admin default in brain/runtime fallback
// chains — it never overrides is_default_chat.
func (s *Server) handlePreferredChatModel(w http.ResponseWriter, r *http.Request) {
	if s.Cache == nil {
		http.Error(w, "registry cache not wired", http.StatusServiceUnavailable)
		return
	}
	m, err := s.Cache.PreferredChatModel(r.Context())
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			http.Error(w, "no usable chat model", http.StatusNotFound)
			return
		}
		http.Error(w, "preferred chat lookup failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": m.Code})
}
