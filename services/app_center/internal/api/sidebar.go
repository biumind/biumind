// Sidebar HTTP surface.
//
//   GET    /v1/sidebar/layout?scope=desktop|mobile
//          → 200 { items, version, updated_at, updated_by_device }
//
//   PUT    /v1/sidebar/layout
//          body: { scope, items: [...], expected_version: int, device: "..." }
//          → 200 { items, version, ... }   (new version)
//          → 409 { error.code: "version_conflict" } when expected_version
//                doesn't match current
//
//   POST   /v1/sidebar/reset?scope=...
//          → 200 { items: [], version: 1 }
//
// All routes JWT-gated; the user_id comes from claims.sub. Scope
// resolution is fixed (the user sees their own layout, not someone
// else's) — there's no scope_id query param to pass.
//
// Realtime: every successful PUT / Reset writes
// events.SidebarLayoutChanged which the outbox poller pushes onto
// topic `sidebar:user:<uid>` so other devices auto-refresh.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/app_center/internal/sidebar"
	"github.com/google/uuid"
)

// MountSidebar wires the three endpoints. Required before the
// /v1/sidebar/* paths return anything other than 503.
func (s *Server) MountSidebar(mux *http.ServeMux) {
	if s.Pool == nil {
		return
	}
	if s.SidebarSvc == nil {
		s.SidebarSvc = sidebar.New(s.Pool)
	}
	mux.HandleFunc("GET /v1/sidebar/layout", s.requireAuth(s.handleGetSidebar))
	mux.HandleFunc("PUT /v1/sidebar/layout", s.requireAuth(s.handlePutSidebar))
	mux.HandleFunc("POST /v1/sidebar/reset", s.requireAuth(s.handleResetSidebar))
}

func (s *Server) handleGetSidebar(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_user", "")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "desktop"
	}
	layout, err := s.SidebarSvc.Get(r.Context(), userID, scope)
	if err != nil {
		mapSidebarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

type putSidebarReq struct {
	Scope           string         `json:"scope"`
	Items           []sidebar.Item `json:"items"`
	ExpectedVersion int            `json:"expected_version"`
	Device          string         `json:"device"`
}

func (s *Server) handlePutSidebar(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_user", "")
		return
	}
	var req putSidebarReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Scope == "" {
		req.Scope = "desktop"
	}
	if req.Items == nil {
		req.Items = []sidebar.Item{}
	}
	layout, err := s.SidebarSvc.Put(
		r.Context(), userID, req.Scope, req.Items, req.ExpectedVersion, req.Device,
	)
	if err != nil {
		mapSidebarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

func (s *Server) handleResetSidebar(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_user", "")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "desktop"
	}
	device := r.URL.Query().Get("device")
	layout, err := s.SidebarSvc.Reset(r.Context(), userID, scope, device)
	if err != nil {
		mapSidebarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

// ─── helpers ────────────────────────────────────────────────

func userIDFromCtx(r *http.Request) (uuid.UUID, bool) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func mapSidebarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sidebar.ErrVersionConflict):
		writeErr(w, http.StatusConflict, "version_conflict",
			"another device updated this layout — refetch and retry")
	case errors.Is(err, sidebar.ErrInvalidScope):
		writeErr(w, http.StatusBadRequest, "invalid_scope", err.Error())
	case errors.Is(err, sidebar.ErrTooManyItems):
		writeErr(w, http.StatusBadRequest, "too_many_items", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "sidebar_failed", err.Error())
	}
}
