// models.go — GET /v1/internal/models/default-chat (Phase B relay half).
//
// brain's ChatRunner used to hardcode a fallback model code; now the
// platform default chat model is an admin-managed flag
// (models.is_default_chat, migration 00002) and brain pulls it here at
// resolve time. Same bearer middleware as /v1/internal/chat.
//
// Response:
//
//	200 {"code": "<models.code>"}
//	404 plain-text error when no default is set, or the default model
//	    has been deactivated (treated as "no default").
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
