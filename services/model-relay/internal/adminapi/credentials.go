package adminapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/health"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/credentials?provider_id=...&status=active&q=label
func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := registry.CredentialFilter{
		Status: registry.EntityStatus(q.Get("status")),
		Search: q.Get("q"),
	}
	if pid := q.Get("provider_id"); pid != "" {
		id, err := uuid.Parse(pid)
		if err != nil {
			writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
				"provider_id must be a UUID", "provider_id")
			return
		}
		f.ProviderID = id
	}
	items, err := s.Store.Credentials.ListSafe(r.Context(), f)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
	})
}

// GET /v1/admin/credentials/{id} — returns scrubbed view (no plaintext).
func (s *Server) handleGetCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	cred, err := s.Store.Credentials.Get(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, registry.NewCredentialSafe(cred))
}

// credentialCreateRequest is what admin posts to create a credential.
// Plaintext is the upstream API key — encrypted before storage. The
// JSON tag is "plaintext" not "key" because the latter is ambiguous.
type credentialCreateRequest struct {
	ProviderID     string            `json:"provider_id"`
	Label          string            `json:"label"`
	Plaintext      string            `json:"plaintext"`
	BaseURL        string            `json:"base_url"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         string            `json:"status"`
}

func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	var req credentialCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	pid, err := uuid.Parse(req.ProviderID)
	if err != nil {
		writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
			"provider_id required", "provider_id")
		return
	}
	if strings.TrimSpace(req.Plaintext) == "" {
		writeErrorField(w, http.StatusBadRequest, "missing_field",
			"plaintext required", "plaintext")
		return
	}
	status := registry.EntityStatus(req.Status)
	if status == "" {
		status = registry.StatusActive
	}
	safe, err := s.Vault.Save(r.Context(), registry.SaveInput{
		ProviderID:     pid,
		Label:          req.Label,
		Plaintext:      req.Plaintext,
		BaseURL:        req.BaseURL,
		HeaderOverride: req.HeaderOverride,
		Status:         status,
	})
	if err != nil {
		// Vault.Save can return ErrConflict (duplicate label per provider,
		// caught at SQL layer) or generic insert errors.
		if errors.Is(err, registry.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, safe)
}

// credentialUpdateRequest covers two paths:
//   - Metadata-only: label / base_url / header / status set, plaintext empty.
//   - Rotation:     plaintext non-empty → re-encrypt with new key.
type credentialUpdateRequest struct {
	Label          string            `json:"label"`
	BaseURL        string            `json:"base_url"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         string            `json:"status"`
	Plaintext      string            `json:"plaintext,omitempty"` // optional rotation
}

func (s *Server) handleUpdateCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req credentialUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Plaintext != "" {
		// Rotation path — Vault.Rotate preserves metadata + replaces ciphertext.
		safe, err := s.Vault.Rotate(r.Context(), id, req.Plaintext)
		if err != nil {
			translateRegistryError(w, err)
			return
		}
		// Then fold in any metadata changes the request also wanted.
		safe2, err := s.Vault.UpdateMetadata(r.Context(), id,
			req.Label, req.BaseURL, req.HeaderOverride,
			registry.EntityStatus(orDefault(req.Status, string(registry.StatusActive))),
		)
		if err != nil {
			translateRegistryError(w, err)
			return
		}
		_ = safe // first call's result superseded by second
		writeJSON(w, http.StatusOK, safe2)
		return
	}

	// Metadata-only update.
	status := registry.EntityStatus(req.Status)
	if status == "" {
		status = registry.StatusActive
	}
	safe, err := s.Vault.UpdateMetadata(r.Context(), id,
		req.Label, req.BaseURL, req.HeaderOverride, status,
	)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, safe)
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	// Block delete if any channel still references this credential.
	// FK is RESTRICT at the SQL layer so a delete would fail anyway,
	// but checking up front gives a friendlier error.
	count, err := s.Store.Credentials.CountChannels(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "credential_in_use",
			"credential is referenced by channels; remove channels first")
		return
	}
	if err := s.Store.Credentials.Delete(r.Context(), id); err != nil {
		translateRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/admin/credentials/{id}/test
type testCredentialRequest struct {
	TestModel string `json:"test_model"` // optional override
}

func (s *Server) handleTestCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req testCredentialRequest
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	res := s.Probe.RunCredential(r.Context(), id, req.TestModel)

	// Stamp the result back onto the credential row so admin list shows
	// "last tested 5s ago, ok / 401". Failures don't auto-flip to invalid
	// here — only the supervisor's request-path RecordFailure does that.
	if err := s.Store.Credentials.PatchTestResult(
		r.Context(), id,
		probeErrorMessage(res), res.OK,
	); err != nil {
		s.Logger.Warn("admin: stamp test result failed",
			"credential_id", id, "err", err.Error())
	}
	writeJSON(w, http.StatusOK, res)
}

// ─── small helpers ────────────────────────────────────────────────

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// probeErrorMessage compresses a ProbeResult into a short string for
// the credentials.last_test_error column. Empty on success.
func probeErrorMessage(res *health.ProbeResult) string {
	if res == nil || res.OK {
		return ""
	}
	if res.Error == "" {
		return res.ErrorCode
	}
	return res.ErrorCode + ": " + res.Error
}
