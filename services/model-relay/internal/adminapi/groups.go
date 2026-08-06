package adminapi

import (
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/model-groups
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.Groups.List(r.Context())
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
	})
}

type groupRequest struct {
	Code        string                  `json:"code"`
	Name        string                  `json:"name"`
	OwnerType   registry.GroupOwnerType `json:"owner_type"`
	OwnerID     string                  `json:"owner_id"`
	Description string                  `json:"description"`
}

// POST /v1/admin/model-groups
//
// MVP scope: create endpoint exists but admin UI doesn't expose it (Q3
// Phase 3 territory). The endpoint is there so a future admin can
// already POST org/user groups; until UI lands, only superadmin via
// curl uses it. Default-group is seeded by migration and not creatable
// here (the unique-code constraint guards re-creation).
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	got, err := s.Store.Groups.Insert(r.Context(), registry.ModelGroupInput{
		Code:        req.Code,
		Name:        req.Name,
		OwnerType:   req.OwnerType,
		OwnerID:     req.OwnerID,
		Description: req.Description,
	})
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, got)
}
