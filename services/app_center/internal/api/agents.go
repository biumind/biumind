// Per-agent grant HTTP handlers (M3.3).
//
//	GET    /v1/apps/installs/{id}/agents
//	POST   /v1/apps/installs/{id}/agents          body: {"agent_id": "..."}
//	DELETE /v1/apps/installs/{id}/agents/{agentID}
//
// Authz checks (app:grant_agent / app:revoke_agent) are performed
// inside the Installer methods; the API layer is a thin shim that
// authenticates the caller, parses path/body, and maps installs
// errors to HTTP status codes.

package api

import (
	"encoding/json"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

func (s *Server) handleListAgentGrants(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	installID := r.PathValue("id")
	grants, err := s.Installer.ListAgentGrants(r.Context(), installID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_grants_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"install_id": installID,
		"grants":     grants,
	})
}

type grantAgentReq struct {
	AgentID string `json:"agent_id"`
}

func (s *Server) handleGrantAgent(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	installID := r.PathValue("id")

	var body grantAgentReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	agentID, err := uuid.Parse(body.AgentID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_agent_id", err.Error())
		return
	}

	g, err := s.Installer.GrantAgent(r.Context(), installID, agentID,
		claims.UserID, claims.OrgID, claims.Roles)
	if err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleRevokeAgent(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	installID := r.PathValue("id")
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_agent_id", err.Error())
		return
	}
	if err := s.Installer.RevokeAgent(r.Context(), installID, agentID,
		claims.UserID, claims.OrgID, claims.Roles); err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
