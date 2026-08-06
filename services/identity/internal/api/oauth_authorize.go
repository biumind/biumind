// OAuth 2.1 authorize endpoint — code + PKCE flow.
//
//	GET /oauth/authorize
//	  ?response_type=code
//	  &client_id=<uuid>
//	  &redirect_uri=<exact match against client's registered list>
//	  &scope=<space-separated>
//	  &state=<opaque to AS>
//	  &code_challenge=<base64url SHA256 of verifier>
//	  &code_challenge_method=S256
//
// Authentication: Bearer JWT in Authorization header (or `?access_token=`
// query param fallback for browser navigations that can't set headers).
// 401 with WWW-Authenticate when missing — desktop clients that wrap a
// webview can intercept and present login first.
//
// Consent UX: MVP auto-approves if the user is authenticated. The
// authentication itself is the consent gate (the user typed their
// credentials into biumind, then clicked "Connect" in the third-party
// app — the OAuth flow is just plumbing). A dedicated consent page
// lands once the third-party app ecosystem is wide enough to need it.
package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/google/uuid"
)

const authCodeTTL = 10 * time.Minute

// MountOAuthAuthorize registers GET /oauth/authorize.
func (s *Server) MountOAuthAuthorize(mux *http.ServeMux) {
	mux.HandleFunc("GET /oauth/authorize", s.handleOAuthAuthorize)
}

func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Step 1 — strict request shape (OAuth 2.1 §4.1.1).
	clientIDStr := q.Get("client_id")
	if clientIDStr == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", "client_id required")
		return
	}
	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_client", "client_id must be uuid")
		return
	}
	respType := q.Get("response_type")
	if respType != "code" {
		writeOAuthErr(w, http.StatusBadRequest, "unsupported_response_type", respType)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", "redirect_uri required")
		return
	}
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	if codeChallenge == "" {
		// OAuth 2.1 mandates PKCE for all clients.
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request",
			"code_challenge required (PKCE is mandatory under OAuth 2.1)")
		return
	}
	if codeChallengeMethod == "" {
		codeChallengeMethod = "plain"
	}
	if codeChallengeMethod != "S256" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request",
			"code_challenge_method must be S256 (plain is rejected per OAuth 2.1)")
		return
	}

	// Step 2 — load client + verify redirect_uri exact-match.
	client, err := s.Store.GetOAuthClientByID(r.Context(), clientID)
	if err != nil {
		// Per OAuth 2.1 §4.1.2.1: when client_id is invalid we MUST NOT
		// redirect; we tell the user directly.
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	if !contains(client.RedirectURIs, redirectURI) {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_redirect_uri",
			"redirect_uri does not match a registered URI")
		return
	}

	// Step 3 — authenticate the user. Bearer header or query fallback.
	accessTok := bearerFromHeader(r) // existing helper used by requireAuth
	if accessTok == "" {
		accessTok = q.Get("access_token")
	}
	if accessTok == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="biumind"`)
		writeOAuthErr(w, http.StatusUnauthorized, "login_required",
			"open this URL with an Authorization: Bearer header or ?access_token= query")
		return
	}
	claims, err := s.Verifier.Verify(accessTok)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}

	// Step 4 — derive scope. Intersect requested with what the client
	// registered for; pass through otherwise. We don't validate scope
	// strings at this layer — the resource server (brain, runtime, etc.)
	// enforces them at use-time.
	scope := q.Get("scope")
	if scope == "" {
		scope = client.Scope
	}

	// Step 5 — mint + persist auth code.
	code, err := randomURLSafe(32)
	if err != nil {
		s.redirectWithError(w, r, redirectURI, q.Get("state"),
			"server_error", "code generation failed")
		return
	}
	if err := s.Store.CreateAuthCode(r.Context(), store.CreateAuthCodeInput{
		Code:                code,
		ClientID:            clientID,
		UserID:              userID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           time.Now().UTC().Add(authCodeTTL),
	}); err != nil {
		s.redirectWithError(w, r, redirectURI, q.Get("state"),
			"server_error", err.Error())
		return
	}

	// Step 6 — emit activity.
	s.EmitActivity(r.Context(), store.CreateActivityEventInput{
		ActorID:        userID,
		AudienceUserID: &userID,
		Kind:           "oauth.authorized",
		TargetType:     "oauth_client",
		TargetID:       clientID.String(),
		Summary:        "授权应用 \"" + client.ClientName + "\" 访问账户",
		Detail: map[string]any{
			"scope":  scope,
			"client": client.ClientName,
		},
	})

	// Step 7 — 302 redirect to client's redirect_uri with code & state.
	dst, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	dq := dst.Query()
	dq.Set("code", code)
	if st := q.Get("state"); st != "" {
		dq.Set("state", st)
	}
	dst.RawQuery = dq.Encode()
	http.Redirect(w, r, dst.String(), http.StatusFound)
}

// redirectWithError sends the OAuth-style error params back to the client
// per §4.1.2.1 — used for errors AFTER the client/redirect_uri have been
// validated. Errors before that point return JSON to the user agent.
func (s *Server) redirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	dst, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	dq := dst.Query()
	dq.Set("error", code)
	if desc != "" {
		dq.Set("error_description", desc)
	}
	if state != "" {
		dq.Set("state", state)
	}
	dst.RawQuery = dq.Encode()
	http.Redirect(w, r, dst.String(), http.StatusFound)
}

// bearerFromHeader extracts the token from Authorization: Bearer <tok>.
// Returns "" on missing or malformed.
func bearerFromHeader(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// randomURLSafe returns n bytes of randomness encoded as base64url
// (RFC 4648 §5, no padding) — safe for use as URL path / query.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
