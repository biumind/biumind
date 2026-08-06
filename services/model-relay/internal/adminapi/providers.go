package adminapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/providers?status=active&protocol=openai_compat&q=open
func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	f := registry.ProviderFilter{
		Status:   registry.EntityStatus(r.URL.Query().Get("status")),
		Protocol: registry.ProviderProtocol(r.URL.Query().Get("protocol")),
		Search:   r.URL.Query().Get("q"),
	}
	items, err := s.Store.Providers.List(r.Context(), f)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
	})
}

func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	got, err := s.Store.Providers.Get(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

type providerRequest struct {
	Code        string                     `json:"code"`
	Name        string                     `json:"name"`
	Protocol    registry.ProviderProtocol  `json:"protocol"`
	Icon        string                     `json:"icon"`
	Description string                     `json:"description"`
	Status      registry.EntityStatus      `json:"status"`
}

func (req providerRequest) toInput() registry.ProviderInput {
	return registry.ProviderInput{
		Code: req.Code, Name: req.Name, Protocol: req.Protocol,
		Icon: req.Icon, Description: req.Description, Status: req.Status,
	}
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	got, err := s.Store.Providers.Insert(r.Context(), req.toInput())
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req providerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	got, err := s.Store.Providers.Update(r.Context(), id, req.toInput())
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if err := s.Store.Providers.Delete(r.Context(), id); err != nil {
		translateRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ──────────────────────────────────────────────────────

// parseUUIDPath extracts a UUID from a path variable. Writes an error
// and returns ok=false on bad input.
func parseUUIDPath(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		writeErrorField(w, http.StatusBadRequest, "missing_path_param",
			"path parameter required", name)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
			"path parameter must be a UUID: "+err.Error(), name)
		return uuid.Nil, false
	}
	return id, true
}
