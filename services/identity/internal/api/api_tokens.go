// API tokens (PAT) — long-lived programmatic-access bearer tokens.
//
//	POST   /v1/identity/me/tokens          create + return secret ONCE
//	GET    /v1/identity/me/tokens          list (no secret)
//	DELETE /v1/identity/me/tokens/{id}     revoke
//	GET    /v1/identity/whoami             auth-agnostic identity check
//
// All routes require an existing Bearer token (JWT or another PAT) so
// you can mint a child token but only after authenticating with
// something. The returned secret is the FULL JWT — clients paste it
// into MCP configs / CI variables verbatim, and every downstream
// service accepts it through the existing JWT path with no changes.
//
// JWT shape (claims):
//
//	sub:  owner user id
//	jti:  random; row-id for list/revoke lookup
//	kind: "pat"             — distinguishes from session JWTs so the
//	                          refresh handler doesn't try to rotate it
//	exp:  configurable; default 1 year
//	iat:  now
//
// `bm_<8-char-prefix>_<jwt>` is the user-visible token format. We
// store the prefix in the row for redacted listing display; the prefix
// has no auth role — only the JWT signature does.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Default PAT TTL — 1 year. Configurable per token via expires_at,
// but most users want "remember this forever" semantics, and the
// alternative (forcing a yearly rotation) just trains them to copy
// the longest expiry into automation.
const defaultPATTTL = 365 * 24 * time.Hour

// MountAPITokens registers the four PAT routes on `mux`. Caller is
// responsible for protecting them via requireAuth — done at Mount
// time so the server's handler-wiring patterns stay consistent.
func (s *Server) MountAPITokens(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/identity/me/tokens", s.requireAuth(s.handleCreateAPIToken))
	mux.HandleFunc("GET /v1/identity/me/tokens", s.requireAuth(s.handleListAPITokens))
	mux.HandleFunc("DELETE /v1/identity/me/tokens/{id}", s.requireAuth(s.handleRevokeAPIToken))
	mux.HandleFunc("GET /v1/identity/whoami", s.requireAuth(s.handleWhoami))
}

// ─── wire types ────────────────────────────────────────────────

type createAPITokenReq struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	// TTLSeconds 0 → server default (1y). Caller can shrink for
	// short-lived CI tokens.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

type apiTokenOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Prefix      string   `json:"prefix"`
	Redacted    string   `json:"redacted"`
	Scopes      []string `json:"scopes"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	LastUsedAt  *string  `json:"last_used_at,omitempty"`
	ExpiresAt   string   `json:"expires_at"`
	RevokedAt   *string  `json:"revoked_at,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

type apiTokenWithSecretOut struct {
	apiTokenOut
	// Secret is the full `bm_<prefix>_<jwt>` string. Returned ONCE
	// at creation time and never persisted server-side beyond the
	// JWT signature itself (the row stores only metadata).
	Secret string `json:"secret"`
}

func tokenJSON(t *store.APIToken) apiTokenOut {
	out := apiTokenOut{
		ID:        t.ID.String(),
		Name:      t.Name,
		Prefix:    t.Prefix,
		Redacted:  redactPATPrefix(t.Prefix),
		Scopes:    t.Scopes,
		ExpiresAt: t.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.WorkspaceID != nil {
		out.WorkspaceID = t.WorkspaceID.String()
	}
	if t.ProjectID != nil {
		out.ProjectID = t.ProjectID.String()
	}
	if t.LastUsedAt != nil {
		v := t.LastUsedAt.UTC().Format(time.RFC3339)
		out.LastUsedAt = &v
	}
	if t.RevokedAt != nil {
		v := t.RevokedAt.UTC().Format(time.RFC3339)
		out.RevokedAt = &v
	}
	return out
}

func redactPATPrefix(prefix string) string {
	return "bm_" + prefix + "_…"
}

// ─── handlers ──────────────────────────────────────────────────

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var req createAPITokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "missing_name", "")
		return
	}

	c := bauth.MustClaims(r.Context())
	owner, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	// PATs can't mint other PATs of equal or higher privilege (no
	// privilege escalation via chain). Identity treats PAT-issued JWTs
	// the same as user JWTs in the verifier; the kind=pat claim lets
	// us reject mint requests originating from a PAT itself.
	if c.IsService || strings.EqualFold(getJWTKind(r.Header.Get("Authorization")), "pat") {
		writeErr(w, http.StatusForbidden, "no_pat_chain",
			"cannot mint a PAT using a PAT — sign in with the user JWT")
		return
	}

	// Optional workspace / project scopes. We don't validate they
	// exist; brain checks scope at use-time. Leaving them as opaque
	// strings keeps identity from coupling to brain's project ownership
	// rules (which evolve faster than auth contract).
	var workspaceID, projectID *uuid.UUID
	if req.WorkspaceID != "" {
		v, err := uuid.Parse(req.WorkspaceID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_workspace_id", err.Error())
			return
		}
		workspaceID = &v
	}
	if req.ProjectID != "" {
		v, err := uuid.Parse(req.ProjectID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_project_id", err.Error())
			return
		}
		projectID = &v
	}

	ttl := defaultPATTTL
	if req.TTLSeconds > 0 && time.Duration(req.TTLSeconds)*time.Second < ttl {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	expiresAt := time.Now().UTC().Add(ttl)

	prefix := randomPrefix(8)
	jti := uuid.NewString()

	// Mint the JWT. We can't use Signer.Sign directly because that
	// always uses Signer.TTL — for PAT we want a much longer expiry.
	// Build the claims + sign manually, mirroring Signer's HS256/RS256
	// dispatch.
	claims := jwt.MapClaims{
		"sub":   owner.String(),
		"jti":   jti,
		"kind":  "pat",
		"iss":   s.Signer.Issuer,
		"aud":   []string{s.Signer.Audience},
		"iat":   time.Now().Unix(),
		"exp":   expiresAt.Unix(),
		"scope": req.Scopes,
	}
	signed, err := s.signCustomJWT(claims)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign_failed", err.Error())
		return
	}

	row, err := s.Store.CreateAPIToken(r.Context(), store.CreateAPITokenInput{
		OwnerID:     owner,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        req.Name,
		Prefix:      prefix,
		JTI:         jti,
		Scopes:      req.Scopes,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	s.EmitActivity(r.Context(), store.CreateActivityEventInput{
		ActorID:        owner,
		AudienceUserID: &owner,
		Kind:           "pat.created",
		TargetType:     "pat",
		TargetID:       row.ID.String(),
		Summary:        "创建了 API Token \"" + req.Name + "\"",
		Detail: map[string]any{
			"prefix": prefix,
			"scopes": req.Scopes,
		},
	})

	secret := "bm_" + prefix + "_" + signed
	writeJSON(w, http.StatusCreated, apiTokenWithSecretOut{
		apiTokenOut: tokenJSON(row),
		Secret:      secret,
	})
}

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	owner, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	rows, err := s.Store.ListAPITokens(r.Context(), owner)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]apiTokenOut, 0, len(rows))
	for _, t := range rows {
		out = append(out, tokenJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	c := bauth.MustClaims(r.Context())
	owner, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	if err := s.Store.RevokeAPIToken(r.Context(), owner, id); err != nil {
		if errors.Is(err, store.ErrAPITokenNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.EmitActivity(r.Context(), store.CreateActivityEventInput{
		ActorID:        owner,
		AudienceUserID: &owner,
		Kind:           "pat.revoked",
		TargetType:     "pat",
		TargetID:       id.String(),
		Summary:        "撤销了 API Token",
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id.String(), "revoked": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	out := map[string]any{
		"user_id":    c.UserID,
		"org_id":     c.OrgID,
		"team_ids":   c.TeamIDs,
		"roles":      c.Roles,
		"plan":       c.Plan,
		"scope":      c.Scope,
		"is_service": c.IsService,
	}
	if c.RegisteredClaims.ExpiresAt != nil {
		out["expires_at"] = c.RegisteredClaims.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if kind := jwtKindFromAuthHeader(r); kind != "" {
		out["kind"] = kind
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── helpers ───────────────────────────────────────────────────

func randomPrefix(n int) string {
	b := make([]byte, n/2+1)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// signCustomJWT signs claims with the configured Signer's key. Delegates
// to Signer.SignMap, which dispatches HS256/RS256 (RS256 stamps the kid
// header for JWKS verifiers) and accepts arbitrary claims so PATs / OAuth
// access tokens can carry their own iat/exp/jti without going through
// the session-token TTL.
func (s *Server) signCustomJWT(claims jwt.MapClaims) (string, error) {
	return s.Signer.SignMap(claims)
}

// getJWTKind / jwtKindFromAuthHeader inspect the Authorization header
// without verifying the token (the verifier already ran in requireAuth).
// Returns the `kind` claim or "" when absent / token is malformed —
// callers treat "" as "user JWT" by convention.
func getJWTKind(authHeader string) string {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	tok := bauth.StripPATFraming(authHeader[7:])
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, err := parser.ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		return ""
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return ""
	}
	if k, ok := mc["kind"].(string); ok {
		return k
	}
	return ""
}

func jwtKindFromAuthHeader(r *http.Request) string {
	return getJWTKind(r.Header.Get("Authorization"))
}
