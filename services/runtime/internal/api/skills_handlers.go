// Skills HTTP handlers — REST surface backed by
// services/runtime/internal/skills.Registry. The wire format
// mirrors the proto messages in packages/proto/biumind/runtime/v1/
// skills.proto so a future Connect-Go server (or a Dart client
// using JSON over HTTP) can read this without translation.
//
// Routes (mounted in api.go when Server.Skills is non-nil):
//
//	GET    /v1/skills                       list scoped to caller's org
//	GET    /v1/skills/{id}                  get one
//	POST   /v1/skills                       install (URL/Zip later — inline now)
//	PATCH  /v1/skills/{id}                  sparse update
//	DELETE /v1/skills/{id}                  delete
//	POST   /v1/skills/{id}/toggle           per-agent enable + pin
//
// Propose / Approve / Reject / ShareSkillOrg are reserved for PS3.1
// (the self-authoring workflow lands with the approval UI). URL +
// Zip install paths are reserved for PS2.3.
//
// Auth: every route is gated by api.Server.requireAuth, which
// populates bauth.Claims in ctx. handlers extract OrgID via
// claims.OrgID and refuse the request if it's missing/invalid —
// the registry's UNIQUE (org_id, identifier) constraint depends
// on every write carrying a real org.

package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/biumind/biumind/services/runtime/internal/skills/installer"
	"github.com/google/uuid"
)

// ─── List ───────────────────────────────────────────────────

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	in := skillsreg.ListInput{
		OrgID:  orgID,
		Source: skillsreg.Source(r.URL.Query().Get("source")),
		Status: skillsreg.Status(r.URL.Query().Get("status")),
	}
	if owner := r.URL.Query().Get("owner_id"); owner != "" {
		uid, err := uuid.Parse(owner)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_owner_id", err.Error())
			return
		}
		in.OwnerID = &uid
	}
	rows, err := s.Skills.List(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, sk := range rows {
		out = append(out, skillToJSON(sk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// ─── Get ────────────────────────────────────────────────────

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	sk, err := s.Skills.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if sk.OrgID != orgID {
		// Cross-org reads — surface 404 rather than 403 so we don't
		// leak existence to outsiders.
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, skillToJSON(sk))
}

// ─── Install (URL / Zip / Inline) ───────────────────────────

// installSkillReq is a soft oneof — exactly one of `url`,
// `zip_b64`, or the inline body fields is expected. Server picks by
// priority (URL > Zip > inline) so a misconfigured caller gets
// deterministic behaviour. Marshalling-as-JSON-with-empty-fields
// remains harmless: empty url + empty zip + empty identifier =
// validation failure.
type installSkillReq struct {
	// URL fetch — pulls a single SKILL.md over HTTPS.
	URL string `json:"url"`

	// Zip blob, base64-encoded. Smaller than multipart for typical
	// .biuskill packages (≤ ~1MB), and JSON-only round-trip means
	// the existing transport stack has no extra moving parts.
	ZipB64 string `json:"zip_b64"`

	// SignatureB64 — optional base64-encoded ed25519 signature over
	// the raw zip bytes (P2-#10). Required when the server is in
	// strict mode (trust store non-empty). Verified against the
	// configured trust store before installer.FromZip is called.
	SignatureB64 string `json:"signature_b64"`

	// Inline frontmatter+body. Mutually exclusive with URL/Zip.
	Identifier  string                            `json:"identifier"`
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	Body        string                            `json:"body"`
	Manifest    skillsreg.Manifest                `json:"manifest"`
	Paths       []string                          `json:"paths"`
	Permissions []string                          `json:"permissions"`
	Resources   map[string]skillsreg.ResourceMeta `json:"resources"`

	// Per-agent enablement coupling — non-empty target_agent_id
	// pins/enables in the same request so a UI install→toggle
	// flow doesn't need two calls.
	TargetAgentID string `json:"target_agent_id"`
	Pin           bool   `json:"pin"`
}

func (s *Server) handleInstallSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	var req installSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	uid, err := uuid.Parse(bauth.MustClaims(r.Context()).UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}

	in, source, err := buildCreateInput(r, req, orgID, uid, s.SkillsTrustStore)
	if err != nil {
		// Map installer-specific errors to friendly status codes.
		switch {
		case errors.Is(err, installer.ErrTooLarge):
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
		case errors.Is(err, installer.ErrSignatureRequired):
			writeErr(w, http.StatusForbidden, "signature_required", err.Error())
		case errors.Is(err, installer.ErrUntrusted):
			writeErr(w, http.StatusForbidden, "untrusted_publisher", err.Error())
		default:
			writeErr(w, http.StatusBadRequest, "install_parse", err.Error())
		}
		return
	}

	sk, err := s.Skills.Create(r.Context(), in)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	_ = source // reserved for future telemetry / event payload

	if req.TargetAgentID != "" {
		agentID, err := uuid.Parse(req.TargetAgentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_agent_id", err.Error())
			return
		}
		if _, err := s.Skills.Toggle(r.Context(), agentID, sk.ID, true, req.Pin); err != nil {
			// Skill was already created at this point; surface the
			// toggle failure but don't try to roll back — the user
			// can retry the toggle endpoint with the returned id.
			writeErr(w, http.StatusInternalServerError, "toggle_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, skillToJSON(sk))
}

// buildCreateInput resolves the installSkillReq oneof into a
// skillsreg.CreateInput. Priority: URL > Zip > inline. Returns the
// chosen source label so the handler can record provenance.
//
// trust may be nil; an empty store means "permissive mode" (signature
// ignored, any zip installs). A non-empty store flips this to strict:
// every zip install MUST carry signature_b64 verifying against a key
// in the store. URL / inline paths bypass verification — they don't
// produce a signed shape today (marketplace adapters can extend later).
func buildCreateInput(r *http.Request, req installSkillReq, orgID, ownerID uuid.UUID, trust *installer.TrustStore) (skillsreg.CreateInput, string, error) {
	switch {
	case req.URL != "":
		parsed, err := installer.FromURL(r.Context(), req.URL)
		if err != nil {
			return skillsreg.CreateInput{}, "", err
		}
		in := installerToCreate(parsed, orgID, ownerID, skillsreg.SourceImported)
		return in, "url", nil

	case req.ZipB64 != "":
		raw, err := base64.StdEncoding.DecodeString(req.ZipB64)
		if err != nil {
			return skillsreg.CreateInput{}, "", errors.New("zip_b64: invalid base64")
		}
		// P2-#10 — signature gate before parse. Empty trust store is
		// a no-op (returns "" / nil). Bad signature short-circuits
		// before installer touches the bytes.
		if _, err := installer.VerifyZipInstall(trust, raw, req.SignatureB64); err != nil {
			return skillsreg.CreateInput{}, "", err
		}
		parsed, err := installer.FromZip(raw)
		if err != nil {
			return skillsreg.CreateInput{}, "", err
		}
		in := installerToCreate(parsed, orgID, ownerID, skillsreg.SourceUser)
		return in, "zip", nil

	default:
		// Inline path — the original PS2.2 happy case.
		if req.Identifier == "" || req.Name == "" {
			return skillsreg.CreateInput{}, "",
				errors.New("identifier + name required (or use url / zip_b64)")
		}
		return skillsreg.CreateInput{
			ID:          newSkillID(),
			OrgID:       orgID,
			OwnerID:     &ownerID,
			Identifier:  req.Identifier,
			Name:        req.Name,
			Description: req.Description,
			Source:      skillsreg.SourceUser,
			Manifest:    req.Manifest,
			Content:     req.Body,
			Resources:   req.Resources,
			Paths:       req.Paths,
			Permissions: req.Permissions,
			Status:      skillsreg.StatusActive,
		}, "inline", nil
	}
}

// installerToCreate is the bridge between the installer package's
// ParsedSkill (transport-agnostic) and the registry's CreateInput
// (carries IDs + ownership + source semantics).
func installerToCreate(p *installer.ParsedSkill, orgID, ownerID uuid.UUID, source skillsreg.Source) skillsreg.CreateInput {
	return skillsreg.CreateInput{
		ID:          newSkillID(),
		OrgID:       orgID,
		OwnerID:     &ownerID,
		Identifier:  p.Identifier,
		Name:        p.Name,
		Description: p.Description,
		Source:      source,
		Manifest:    p.Manifest,
		Content:     p.Body,
		Resources:   p.Resources,
		Paths:       p.Paths,
		Permissions: p.Permissions,
		Status:      skillsreg.StatusActive,
	}
}

// ─── Update ─────────────────────────────────────────────────

type updateSkillReq struct {
	Description   *string                            `json:"description,omitempty"`
	Body          *string                            `json:"body,omitempty"`
	Manifest      *skillsreg.Manifest                `json:"manifest,omitempty"`
	Paths         *[]string                          `json:"paths,omitempty"`
	Permissions   *[]string                          `json:"permissions,omitempty"`
	Resources     *map[string]skillsreg.ResourceMeta `json:"resources,omitempty"`
	ZipFileSha256 *string                            `json:"zip_file_sha256,omitempty"`
}

func (s *Server) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	cur, err := s.Skills.Get(r.Context(), id)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if cur.OrgID != orgID {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	var req updateSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	in := skillsreg.UpdateInput{ID: id}
	if req.Description != nil {
		in.Description = *req.Description
		in.SetDescription = true
	}
	if req.Body != nil {
		in.Content = *req.Body
		in.SetContent = true
	}
	if req.Manifest != nil {
		in.Manifest = *req.Manifest
		in.SetManifest = true
	}
	if req.Paths != nil {
		in.Paths = *req.Paths
		in.SetPaths = true
	}
	if req.Permissions != nil {
		in.Permissions = *req.Permissions
		in.SetPermissions = true
	}
	if req.Resources != nil {
		in.Resources = *req.Resources
		in.SetResources = true
	}
	if req.ZipFileSha256 != nil {
		in.ZipFileSha256 = *req.ZipFileSha256
		in.SetZipFileSha256 = true
	}

	out, err := s.Skills.Update(r.Context(), in)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, skillToJSON(out))
}

// ─── Delete ─────────────────────────────────────────────────

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	cur, err := s.Skills.Get(r.Context(), id)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if cur.OrgID != orgID {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := s.Skills.Delete(r.Context(), id); err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// ─── Toggle ─────────────────────────────────────────────────

type toggleSkillReq struct {
	AgentID   string `json:"agent_id"`
	IsEnabled bool   `json:"is_enabled"`
	Pinned    bool   `json:"pinned"`
}

func (s *Server) handleToggleSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	skillID := r.PathValue("id")
	cur, err := s.Skills.Get(r.Context(), skillID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	// Bundled skills (org_id = BundledOrgID = uuid.Nil) are visible to
	// every org by design; the per-org check applies only to
	// org-owned / user-private rows.
	if cur.OrgID != orgID && cur.OrgID != skillsreg.BundledOrgID {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}

	var req toggleSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// agent_id is optional — when empty, fall back to the same
	// deterministic UUID the agent loop uses (deriveAgentID(uid, "biu")).
	// Lets the Web client toggle without picking an agent in a UI that
	// doesn't yet expose multi-agent.
	var agentID uuid.UUID
	if strings.TrimSpace(req.AgentID) == "" {
		uid, err := uuid.Parse(bauth.MustClaims(r.Context()).UserID)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "bad_user", "")
			return
		}
		agentID = deriveAgentID(uid, "biu")
	} else {
		parsed, err := uuid.Parse(req.AgentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_agent_id", err.Error())
			return
		}
		agentID = parsed
	}
	as, err := s.Skills.Toggle(r.Context(), agentID, skillID, req.IsEnabled, req.Pinned)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "toggle_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":   as.AgentID.String(),
		"skill_id":   as.SkillID,
		"is_enabled": as.IsEnabled,
		"pinned":     as.Pinned,
		"added_at":   as.AddedAt,
	})
}

// ─── Activations (read) ─────────────────────────────────────

// GET /v1/skills/{id}/activations?limit=50
//
// Returns:
//
//	{
//	  "stats": { "count": 42, "last_at": "2026-05-29T08:30:00Z" },
//	  "items": [
//	    { "id": "...", "session_id": "...", "trigger": "tool_call",
//	      "trace_id": "rn_...", "tokens_in": 12, "tokens_out": 34,
//	      "occurred_at": "2026-05-29T08:30:00Z" },
//	    ...
//	  ]
//	}
//
// Powers the SkillDetail drawer's "调用 N 次 / 最后调用 X 时间前"
// panel without forcing the client to count rows on its side.
func (s *Server) handleListSkillActivations(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	skillID := r.PathValue("id")
	cur, err := s.Skills.Get(r.Context(), skillID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	// Bundled skills are visible to every org; everything else must
	// match the caller's org.
	if cur.OrgID != orgID && cur.OrgID != skillsreg.BundledOrgID {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	stats, err := s.Skills.ActivationStats(r.Context(), skillID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stats_failed", err.Error())
		return
	}
	items, err := s.Skills.ListActivationsBySkill(r.Context(), skillID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, a := range items {
		out = append(out, map[string]any{
			"id":          a.ID.String(),
			"session_id":  a.SessionID.String(),
			"trigger":     string(a.Trigger),
			"trace_id":    a.TraceID,
			"tokens_in":   a.TokensIn,
			"tokens_out":  a.TokensOut,
			"occurred_at": a.OccurredAt,
		})
	}
	statsOut := map[string]any{"count": stats.Count}
	// Only emit last_at when there's at least one row; epoch as a
	// "no activity" sentinel would be confusing to render in UI.
	if stats.Count > 0 {
		statsOut["last_at"] = stats.LastAt
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats": statsOut,
		"items": out,
	})
}

// ─── helpers ────────────────────────────────────────────────

// skillToJSON shapes a Skill for the wire. JSON tags would normally
// drive this, but Skill is plain Go types we want to keep
// transport-agnostic, and a hand-rolled projection lets us drop
// internal fields (CreatedAt without UpdatedAt makes no sense; both
// or neither) without bleeding storage details into the wire.
func skillToJSON(s *skillsreg.Skill) map[string]any {
	out := map[string]any{
		"id":           s.ID,
		"org_id":       s.OrgID.String(),
		"identifier":   s.Identifier,
		"name":         s.Name,
		"description":  s.Description,
		"source":       string(s.Source),
		"manifest":     s.Manifest,
		"content":      s.Content,
		"content_hash": s.ContentHash,
		"resources":    s.Resources,
		"paths":        s.Paths,
		"permissions":  s.Permissions,
		"status":       string(s.Status),
		"created_at":   s.CreatedAt,
		"updated_at":   s.UpdatedAt,
	}
	if s.OwnerID != nil {
		out["owner_id"] = s.OwnerID.String()
	}
	if s.ZipFileSha256 != "" {
		out["zip_file_sha256"] = s.ZipFileSha256
	}
	if s.UpdateOfID != "" {
		out["update_of_id"] = s.UpdateOfID
	}
	return out
}

// mustOrgID extracts the caller's org from JWT claims and writes a
// friendly error if missing or unparseable. The Skills registry
// requires every write to carry an org for tenancy isolation.
func mustOrgID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims := bauth.MustClaims(r.Context())
	if claims.OrgID == "" {
		writeErr(w, http.StatusForbidden, "no_org", "caller has no org_id claim")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(claims.OrgID)
	if err != nil {
		writeErr(w, http.StatusForbidden, "bad_org_id", err.Error())
		return uuid.Nil, false
	}
	return id, true
}

// mapSkillErr translates skillsreg sentinel errors to HTTP statuses.
// Anything not recognised gets 500 + the raw message so server
// logs still pinpoint the issue without leaking internals.
func mapSkillErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skillsreg.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, skillsreg.ErrNameTaken):
		writeErr(w, http.StatusConflict, "identifier_taken", err.Error())
	case errors.Is(err, skillsreg.ErrInvalidStatus):
		writeErr(w, http.StatusBadRequest, "invalid_status", err.Error())
	case errors.Is(err, skillsreg.ErrBundledImmutable):
		writeErr(w, http.StatusForbidden, "bundled_immutable", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// newSkillID returns a "skill_<32-hex>" identifier. Hex over base32
// because the latter mixes case in ways that break URL paths in some
// edge proxies; 16 random bytes = 32 hex digits = 128 bits of
// entropy, well over the collision floor for any per-org catalogue.
func newSkillID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "skill_" + hex.EncodeToString(b[:])
}
