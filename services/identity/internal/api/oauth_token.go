// OAuth 2.1 token endpoint — code exchange + refresh.
//
//	POST /oauth/token   (application/x-www-form-urlencoded)
//
// Supported grant types:
//
//   - authorization_code (with PKCE verifier — mandatory under OAuth 2.1)
//     Exchange the one-shot code from /oauth/authorize for an access token
//     (JWT, kind="oauth", short-lived) and a refresh token (rt-live- prefix,
//     long-lived, revocable).
//
//   - refresh_token
//     Exchange a refresh_token for a new access_token. We don't currently
//     rotate refresh tokens on use — the existing handleRefresh path
//     doesn't either, and /sessions gives users an explicit revoke knob.
//
// Client authentication:
//
//   - public clients (token_endpoint_auth_method=none): no auth on this
//     endpoint; PKCE verifier is the binding proof.
//   - confidential clients: HTTP Basic (client_id:client_secret) OR
//     client_secret_post (client_id+client_secret in body).
//
// Revocation: the existing refresh-token /v1/auth/logout works for OAuth-
// issued refresh tokens too; clients can also call /oauth/revoke (RFC 7009)
// implemented alongside this endpoint.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/biumind/biumind/services/identity/internal/token"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// MountOAuthToken registers POST /oauth/token + POST /oauth/revoke.
func (s *Server) MountOAuthToken(mux *http.ServeMux) {
	mux.HandleFunc("POST /oauth/token", s.handleOAuthToken)
	mux.HandleFunc("POST /oauth/revoke", s.handleOAuthRevoke)
}

func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	grantType := r.PostForm.Get("grant_type")
	switch grantType {
	case "authorization_code":
		s.handleOAuthTokenCodeExchange(w, r)
	case "refresh_token":
		s.handleOAuthTokenRefresh(w, r)
	default:
		writeOAuthErr(w, http.StatusBadRequest, "unsupported_grant_type", grantType)
	}
}

// ─── authorization_code → token pair ──────────────────────────────

func (s *Server) handleOAuthTokenCodeExchange(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	if code == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", "code required")
		return
	}
	redirectURI := r.PostForm.Get("redirect_uri")
	codeVerifier := r.PostForm.Get("code_verifier")
	if codeVerifier == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request",
			"code_verifier required (PKCE)")
		return
	}

	// Authenticate the client.
	client, authErr := s.authenticateOAuthClient(r)
	if authErr != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_client", authErr.Error())
		return
	}

	// Consume the auth code atomically. Replay → revoke + reject.
	ac, err := s.Store.ConsumeAuthCode(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAuthCodeNotFound):
			writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "code not found")
		case errors.Is(err, store.ErrAuthCodeExpired):
			writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "code expired")
		case errors.Is(err, store.ErrAuthCodeAlreadyConsumed):
			// Replay attack signature — revoke every refresh issued
			// against this client_id+user_id. We don't have a per-code
			// token tracker yet, so revoke this user's refresh tokens
			// scoped to this client's installation key.
			if ac != nil {
				go s.revokeOAuthInstallation(ac.UserID, ac.ClientID)
			}
			writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "code already used")
		default:
			writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		}
		return
	}

	// Strict cross-checks: client_id and redirect_uri MUST match what was
	// passed to /authorize (OAuth 2.1 §4.1.3 / RFC 7636 §4.6).
	if ac.ClientID != client.ClientID {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	if redirectURI != ac.RedirectURI {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}

	// Verify PKCE: SHA256(code_verifier) base64url-no-pad == code_challenge.
	if !verifyPKCE(codeVerifier, ac.CodeChallenge, ac.CodeChallengeMethod) {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_grant", "PKCE verifier mismatch")
		return
	}

	// Mint refresh token bound to (user, oauth client).
	full, hash, err := token.Generate(token.RefreshTokenPrefix)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	installationKey := "oauth:" + client.ClientID.String()
	deviceName := "OAuth: " + client.ClientName
	sid, err := s.Store.CreateOrRotateRefreshToken(
		r.Context(), ac.UserID, installationKey, hash, deviceName,
		s.RefreshTTL, s.refreshAbsoluteTTL(),
	)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Mint access token (JWT). Custom claims so the resource server can
	// distinguish OAuth-issued tokens from session JWTs and PATs.
	accessTok, err := s.signOAuthAccessJWT(ac.UserID, sid, client.ClientID, ac.Scope)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  accessTok,
		"token_type":    "Bearer",
		"expires_in":    int64(s.AccessTTL.Seconds()),
		"refresh_token": full,
		"scope":         ac.Scope,
	})
}

// ─── refresh_token → new access ──────────────────────────────────

func (s *Server) handleOAuthTokenRefresh(w http.ResponseWriter, r *http.Request) {
	rt := r.PostForm.Get("refresh_token")
	if rt == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", "refresh_token required")
		return
	}
	client, authErr := s.authenticateOAuthClient(r)
	if authErr != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_client", authErr.Error())
		return
	}

	hash := token.Hash(rt)
	t, err := s.Store.FindRefreshToken(r.Context(), hash)
	if err != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_grant", "refresh_token unknown")
		return
	}
	if t.RevokedAt != nil || t.ExpiresAt.Before(time.Now()) {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_grant", "refresh_token expired or revoked")
		return
	}
	// Bind: this refresh must belong to this OAuth client.
	wantInstall := "oauth:" + client.ClientID.String()
	if t.InstallationID != wantInstall {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_grant", "refresh_token bound to different client")
		return
	}

	access, err := s.signOAuthAccessJWT(t.UserID, t.ID, client.ClientID, "")
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int64(s.AccessTTL.Seconds()),
	})
}

// ─── /oauth/revoke (RFC 7009) ─────────────────────────────────────

func (s *Server) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	tok := r.PostForm.Get("token")
	if tok == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", "token required")
		return
	}
	// Client auth required by RFC 7009 for confidential clients; public
	// clients identify by client_id only — we accept either.
	client, _ := s.authenticateOAuthClient(r)

	// Try as refresh token (rt-live- prefix). If it doesn't look like one,
	// the access token is a JWT and revocation is best-effort: we have no
	// JWT denylist yet, so we just no-op (RFC 7009 allows). Refresh-token
	// revocation IS effective.
	if strings.HasPrefix(tok, token.RefreshTokenPrefix) {
		hash := token.Hash(tok)
		t, err := s.Store.FindRefreshToken(r.Context(), hash)
		if err == nil && t != nil {
			// If client provided, verify ownership.
			if client != nil && t.InstallationID != "oauth:"+client.ClientID.String() {
				// RFC 7009: respond 200 even if not owner — don't leak.
				w.WriteHeader(http.StatusOK)
				return
			}
			_ = s.Store.RevokeRefreshTokenByID(r.Context(), t.ID, t.UserID)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ─── helpers ──────────────────────────────────────────────────────

// authenticateOAuthClient resolves the client from HTTP Basic, form post,
// or (for public clients) just `client_id`. Returns the row + nil on
// success, nil + error on auth failure.
func (s *Server) authenticateOAuthClient(r *http.Request) (*store.OAuthClient, error) {
	clientID, secret, ok := r.BasicAuth()
	if !ok {
		clientID = r.PostForm.Get("client_id")
		secret = r.PostForm.Get("client_secret")
	}
	if clientID == "" {
		return nil, errors.New("client_id missing")
	}
	// uuid 主键 (DCR client) 或 client_alias (预注册第一方 client, 如 biu-cli).
	c, err := s.resolveOAuthClient(r.Context(), clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}
	switch c.TokenEndpointAuthMethod {
	case "none":
		// Public client. PKCE verifier is the binding proof; no secret
		// to check. Don't fail if a secret was supplied (some MCP clients
		// send empty client_secret unconditionally).
		return c, nil
	case "client_secret_basic", "client_secret_post":
		if secret == "" {
			return nil, errors.New("client_secret required")
		}
		if c.ClientSecretHash == "" {
			return nil, errors.New("client misconfigured: no secret on file")
		}
		ok, err := passwords.Verify(secret, c.ClientSecretHash)
		if err != nil || !ok {
			return nil, errors.New("client_secret mismatch")
		}
		return c, nil
	default:
		return nil, errors.New("unsupported token_endpoint_auth_method on client")
	}
}

// verifyPKCE: SHA256(code_verifier) base64url-no-pad must equal challenge
// when method is S256. Plain method is rejected at /authorize, not here.
func verifyPKCE(codeVerifier, challenge, method string) bool {
	if method != "S256" {
		return false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	// constant-time compare to defeat timing side-channels
	if len(got) != len(challenge) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ challenge[i]
	}
	return diff == 0
}

// signOAuthAccessJWT mints a short-lived bearer JWT scoped to an OAuth
// client. The kind="oauth" claim lets the verifier-side denylist /
// per-client telemetry distinguish these from session JWTs.
func (s *Server) signOAuthAccessJWT(userID, sid, clientID uuid.UUID, scope string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":       userID.String(),
		"sid":       sid.String(),
		"client_id": clientID.String(),
		"kind":      "oauth",
		"iss":       s.Signer.Issuer,
		"aud":       []string{s.Signer.Audience},
		"iat":       now.Unix(),
		"exp":       now.Add(s.AccessTTL).Unix(),
	}
	if scope != "" {
		claims["scope"] = scope
	}
	return s.signCustomJWT(claims)
}

// revokeOAuthInstallation revokes every active refresh token bound to this
// (user, client) pair. Used after a code-replay attack: the only safe
// posture is to assume the client's storage was compromised.
func (s *Server) revokeOAuthInstallation(userID, clientID uuid.UUID) {
	if s.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wantInstall := "oauth:" + clientID.String()
	rows, err := s.Store.ListActiveRefreshTokens(ctx, userID)
	if err != nil {
		return
	}
	for _, t := range rows {
		if t.InstallationID == wantInstall && t.RevokedAt == nil {
			_ = s.Store.RevokeRefreshTokenByID(ctx, t.ID, userID)
		}
	}
}
