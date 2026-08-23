// Repo Apps HTTP handlers (M1.4, tech plan §2.2).
//
//	POST /v1/apps/repo/analyze              {repo_url} → analysis draft
//	POST /v1/apps/repo/installs             {repo_url, ref_type, config} → Installation
//	GET  /v1/apps/installs/{id}/runtime     → {mode:"local", status, url:null}
//	GET  /v1/apps/installs/{id}/builds      → {builds:[...]} (newest first, max 20)
//	POST /v1/apps/installs/{id}/redeploy    → {build_id} (queued row; execution is M2)
//
// All responses use explicit snake_case json tags — the Installation
// dual-key fallback debt (installer.go:141-156) must not spread here.
//
// Error mapping is sentinel-based via mapRepoError (errors.Is), in the
// mapInstallError tradition; no message-substring matching.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/app_center/internal/installs"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
)

// ─── Analyze ────────────────────────────────────────────────────────

type repoAnalyzeReq struct {
	RepoURL string `json:"repo_url"`
}

// handleRepoAnalyze is the one repo endpoint that works without a DB —
// it only needs the GitHub client, so stateless mode stays useful for
// the confirm page.
func (s *Server) handleRepoAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.RepoAnalyzer == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "repo analyze not wired")
		return
	}
	var req repoAnalyzeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.RepoURL == "" {
		writeErr(w, http.StatusBadRequest, "missing_repo_url", "")
		return
	}
	res, err := repoanalyze.Analyze(r.Context(), s.RepoAnalyzer, req.RepoURL)
	if err != nil {
		mapRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ─── Install ────────────────────────────────────────────────────────

type repoInstallReq struct {
	RepoURL string         `json:"repo_url"`
	RefType string         `json:"ref_type"` // "release" | "branch"
	Config  map[string]any `json:"config,omitempty"`
}

func (s *Server) handleRepoInstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil || s.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "App Center running in stateless mode (no DATABASE_URL)")
		return
	}
	if s.RepoAnalyzer == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "repo analyze not wired")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}

	var req repoInstallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.RepoURL == "" {
		writeErr(w, http.StatusBadRequest, "missing_repo_url", "")
		return
	}
	if req.RefType == "" {
		req.RefType = "release"
	}
	if req.RefType != "release" && req.RefType != "branch" {
		writeErr(w, http.StatusBadRequest, "invalid_ref_type", "ref_type must be release or branch")
		return
	}

	// Re-run the analysis server-side — the client-supplied draft is a
	// display artefact, never an install input.
	res, err := repoanalyze.Analyze(r.Context(), s.RepoAnalyzer, req.RepoURL)
	if err != nil {
		mapRepoError(w, err)
		return
	}

	row, err := s.Installer.CreateRepoApp(r.Context(), installs.RepoAppRequest{
		Analysis:    res,
		RefType:     req.RefType,
		Config:      req.Config,
		UserID:      claims.UserID,
		CallerOrgID: claims.OrgID,
	})
	if err != nil {
		mapRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// ─── Runtime / builds / redeploy ────────────────────────────────────

type repoRuntimeResp struct {
	Mode   string  `json:"mode"`
	Status string  `json:"status"`
	URL    *string `json:"url"` // always null in M1 — the client resolves via the local CLI
}

func (s *Server) handleRepoRuntime(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil || s.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "App Center running in stateless mode (no DATABASE_URL)")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	id := r.PathValue("id")
	if _, err := s.Installer.OwnedRepoInstall(r.Context(), id, claims.UserID); err != nil {
		mapRepoError(w, err)
		return
	}
	buildStatus, err := s.Installer.LatestBuildStatus(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "build_status_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repoRuntimeResp{
		Mode:   "local",
		Status: installs.RuntimeStatusFor(buildStatus),
		URL:    nil,
	})
}

const repoBuildsLimit = 20

func (s *Server) handleRepoBuilds(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil || s.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "App Center running in stateless mode (no DATABASE_URL)")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	id := r.PathValue("id")
	if _, err := s.Installer.OwnedRepoInstall(r.Context(), id, claims.UserID); err != nil {
		mapRepoError(w, err)
		return
	}
	builds, err := s.Installer.ListBuilds(r.Context(), id, repoBuildsLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_builds_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"builds": builds})
}

func (s *Server) handleRepoRedeploy(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil || s.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "repo_disabled", "App Center running in stateless mode (no DATABASE_URL)")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	id := r.PathValue("id")
	row, err := s.Installer.OwnedRepoInstall(r.Context(), id, claims.UserID)
	if err != nil {
		mapRepoError(w, err)
		return
	}
	buildID, err := s.Installer.QueueRedeploy(r.Context(), row.ID, row.Identifier, claims.UserID)
	if err != nil {
		mapRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"build_id": buildID})
}

// ─── Error mapping ──────────────────────────────────────────────────

// mapRepoError centralises the repo-endpoint sentinel → HTTP mapping.
// install-path sentinels fall through to mapInstallError.
func mapRepoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repoanalyze.ErrInvalidRepoURL):
		writeErr(w, http.StatusBadRequest, "invalid_repo_url", err.Error())
	case errors.Is(err, repoanalyze.ErrRepoNotFound):
		writeErr(w, http.StatusNotFound, "repo_not_found", err.Error())
	case errors.Is(err, repoanalyze.ErrUpstreamFailed):
		writeErr(w, http.StatusBadGateway, "github_upstream_failed", err.Error())
	case errors.Is(err, repoanalyze.ErrUpstreamShape):
		writeErr(w, http.StatusBadGateway, "github_unexpected_response", err.Error())
	case errors.Is(err, installs.ErrSecretConfigField):
		writeErr(w, http.StatusBadRequest, "secret_field_rejected", err.Error())
	case errors.Is(err, installs.ErrNoRepoRef):
		writeErr(w, http.StatusConflict, "no_ref", err.Error())
	case errors.Is(err, installs.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	default:
		mapInstallError(w, err)
	}
}
