// Install lifecycle HTTP handlers.
//
// These mount alongside the v1.0 catalogue handlers (api.go) on the
// same Server type. They all require Server.Installer to be wired —
// when the service runs in stateless mode (no DATABASE_URL) the
// installs endpoints return 503, surfacing the misconfiguration to
// the caller without crashing the v1.0 invoke path.
//
// Auth model:
//   * Caller must present a valid JWT (the requireAuth middleware in
//     api.go enforces this before any handler here runs).
//   * Scope resolution follows the JWT claims: scope=user uses sub;
//     scope=org uses org_id (and the user must have it set).
//   * Authorization (can this user install / uninstall / toggle this
//     resource?) is delegated to the Installer, which calls Authz
//     with the manifest-derived attributes.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/app_center/internal/installs"
)

// ─── List ─────────────────────────────────────────────────────────

// handleListInstalls returns this caller's installations.
//
//	GET /v1/apps/installs?scope=user|org
//
// scope defaults to "user". For scope=org, the caller's org_id (from
// JWT) is used as the scope_id; users without an org_id get 400.
func (s *Server) handleListInstalls(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "App Center running in stateless mode (no DATABASE_URL)")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "user"
	}
	scopeID := claims.UserID
	if scope == "org" {
		if claims.OrgID == "" {
			writeErr(w, http.StatusBadRequest, "no_org", "user has no org_id")
			return
		}
		scopeID = claims.OrgID
	}

	rows, err := s.Installer.List(r.Context(), scope, scopeID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installations": rows,
		"scope":         scope,
		"scope_id":      scopeID,
	})
}

// ─── Get ──────────────────────────────────────────────────────────

func (s *Server) handleGetInstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	row, err := s.Installer.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if err == installs.ErrNotFound {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ─── Install ──────────────────────────────────────────────────────

type installReq struct {
	Identifier         string         `json:"identifier"`
	Scope              string         `json:"scope"`
	GrantedPermissions []string       `json:"granted_permissions"`
	Config             map[string]any `json:"config,omitempty"`
	// Forced is admin-only — non-admin requests setting forced=true are
	// rejected by Authz.
	Forced bool `json:"forced,omitempty"`
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}

	var req installReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Identifier == "" {
		writeErr(w, http.StatusBadRequest, "missing_identifier", "")
		return
	}
	if req.Scope == "" {
		req.Scope = "user"
	}

	scopeID := claims.UserID
	if req.Scope == "org" {
		if claims.OrgID == "" {
			writeErr(w, http.StatusBadRequest, "no_org", "user has no org_id")
			return
		}
		scopeID = claims.OrgID
	}

	row, err := s.Installer.Install(r.Context(), installs.InstallRequest{
		Identifier:         req.Identifier,
		Scope:              req.Scope,
		ScopeID:            scopeID,
		GrantedPermissions: req.GrantedPermissions,
		Config:             req.Config,
		Forced:             req.Forced,
		CallerUserID:       claims.UserID,
		CallerOrgID:        claims.OrgID,
		CallerRoles:        claims.Roles,
	})
	if err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// ─── Create user_webview App ──────────────────────────────────────
//
// One-shot create-and-install for "Add WebView Application" UI. The
// caller (always a user, never an admin) supplies a URL + name + icon;
// the server synthesises a manifest, persists into the catalogue, and
// installs in one HTTP round-trip.

type userWebViewReq struct {
	Title        string `json:"title"`
	URL          string `json:"url"`
	IconFileHash string `json:"icon_file_hash,omitempty"`
}

func (s *Server) handleCreateUserWebView(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	var req userWebViewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	row, err := s.Installer.CreateUserWebView(r.Context(), installs.UserWebViewRequest{
		Title:        req.Title,
		URL:          req.URL,
		IconFileHash: req.IconFileHash,
		UserID:       claims.UserID,
		OrgID:        claims.OrgID,
	})
	if err != nil {
		// Synthesis errors map to 400 (user input); install errors flow
		// through the regular mapper.
		if isUserInputErr(err) {
			writeErr(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func isUserInputErr(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"url required", "invalid url", "scheme must be",
		"url has no host", "must be FQDN", "title required",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// ─── Uninstall ────────────────────────────────────────────────────

func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	id := r.PathValue("id")
	if err := s.Installer.Uninstall(r.Context(), id, claims.UserID, claims.OrgID, claims.Roles); err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── Toggle ───────────────────────────────────────────────────────

type toggleReq struct {
	// Pointer so {} body = no-op vs explicit `{"enabled": false}`.
	Enabled *bool `json:"enabled,omitempty"`
}

func (s *Server) handleToggleInstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	id := r.PathValue("id")
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Enabled == nil {
		// PATCH with no fields = explicit no-op. Return current row.
		row, err := s.Installer.Get(r.Context(), id)
		if err != nil {
			mapInstallError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, row)
		return
	}
	row, err := s.Installer.Toggle(r.Context(), id, *req.Enabled,
		claims.UserID, claims.OrgID, claims.Roles)
	if err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ─── Error mapping ────────────────────────────────────────────────

// mapInstallError translates installs sentinel errors into the right
// HTTP status code so the client doesn't have to parse error strings.
func mapInstallError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, installs.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, installs.ErrUnknownApp):
		writeErr(w, http.StatusNotFound, "unknown_app", err.Error())
	case errors.Is(err, installs.ErrAlreadyInstalled):
		writeErr(w, http.StatusConflict, "already_installed", err.Error())
	case errors.Is(err, installs.ErrPermissionDenied):
		writeErr(w, http.StatusForbidden, "permission_denied", err.Error())
	case errors.Is(err, installs.ErrPermissionsExceed):
		writeErr(w, http.StatusBadRequest, "permissions_exceed", err.Error())
	case errors.Is(err, installs.ErrManifestInvalid):
		writeErr(w, http.StatusBadRequest, "manifest_invalid", err.Error())
	case errors.Is(err, installs.ErrForcedUninstall):
		writeErr(w, http.StatusForbidden, "forced_uninstall", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "install_failed", err.Error())
	}
}
