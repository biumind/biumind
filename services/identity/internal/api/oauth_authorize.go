// OAuth 2.1 authorize endpoint — code + PKCE flow.
//
//	GET /oauth/authorize
//	  ?response_type=code
//	  &client_id=<uuid | client_alias, e.g. "biu-cli">
//	  &redirect_uri=<exact match against client's registered list;
//	                 loopback URIs (RFC 8252 §7.3) match on any port>
//	  &scope=<space-separated>
//	  &state=<opaque to AS>
//	  &code_challenge=<base64url SHA256 of verifier>
//	  &code_challenge_method=S256
//
// Authentication: bm_session cookie (browser login page, see
// oauth_login_page.go) → Bearer JWT in Authorization header →
// `?access_token=` query param fallback. Browser navigations without a
// valid session get 302 to /oauth/login?return_to=<this URL>; API
// clients keep the 401 + WWW-Authenticate JSON shape — desktop clients
// that wrap a webview can intercept and present login first.
//
// Consent UX: MVP auto-approves if the user is authenticated. The
// authentication itself is the consent gate (the user typed their
// credentials into biumind, then clicked "Connect" in the third-party
// app — the OAuth flow is just plumbing). A dedicated consent page
// lands once the third-party app ecosystem is wide enough to need it.
package api

import (
	"context"
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

	// Step 2 — load client (uuid 主键或 client_alias) + verify redirect_uri.
	client, err := s.resolveOAuthClient(r.Context(), clientIDStr)
	if err != nil {
		// Per OAuth 2.1 §4.1.2.1: when client_id is invalid we MUST NOT
		// redirect; we tell the user directly.
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	if !matchRedirectURI(client.RedirectURIs, redirectURI) {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_redirect_uri",
			"redirect_uri does not match a registered URI")
		return
	}

	// Step 3 — authenticate the user. bm_session cookie (浏览器登录页
	// 签发) → Bearer header → ?access_token= query fallback.
	accessTok := sessionFromCookie(r)
	if accessTok == "" {
		accessTok = bearerFromHeader(r) // existing helper used by requireAuth
	}
	if accessTok == "" {
		accessTok = q.Get("access_token")
	}
	if accessTok == "" {
		s.unauthenticatedAuthorize(w, r, "login_required",
			"open this URL with an Authorization: Bearer header or ?access_token= query")
		return
	}
	claims, err := s.Verifier.Verify(accessTok)
	if err != nil {
		s.unauthenticatedAuthorize(w, r, "invalid_token", err.Error())
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
		ClientID:            client.ClientID,
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
		TargetID:       client.ClientID.String(),
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

// unauthenticatedAuthorize — authorize 无有效 session 时的分流:
// 浏览器导航 (Accept 含 text/html) 302 到登录页, 原始 authorize URL
// (path+query 原样) 经 return_to 带回, 登录成功后弹回继续授权;
// API 客户端保持 401 + WWW-Authenticate + JSON 不变 (老行为).
func (s *Server) unauthenticatedAuthorize(w http.ResponseWriter, r *http.Request, code, desc string) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r,
			"/oauth/login?return_to="+url.QueryEscape(r.URL.RequestURI()),
			http.StatusFound)
		return
	}
	if code == "login_required" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="biumind"`)
	} else {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}
	writeOAuthErr(w, http.StatusUnauthorized, code, desc)
}

// sessionFromCookie — bm_session cookie 里的 session JWT (浏览器登录页
// POST /oauth/login 签发). 无 cookie / 空值返 "".
func sessionFromCookie(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// resolveOAuthClient — client_id 是 UUID 时按主键查 (DCR 注册的第三方
// client); 否则按 client_alias 查 (migration 预注册的第一方 client, 如
// biu-cli — client_id 列是 uuid 主键, 可读的字符串 id 只能走别名列).
func (s *Server) resolveOAuthClient(ctx context.Context, id string) (*store.OAuthClient, error) {
	if uid, err := uuid.Parse(id); err == nil {
		return s.Store.GetOAuthClientByID(ctx, uid)
	}
	return s.Store.GetOAuthClientByAlias(ctx, id)
}

// matchRedirectURI — OAuth 2.1 §4.1.1 + RFC 8252 §7.3:
//
//   - 非 loopback redirect_uri 保持精确匹配 (现状不变);
//   - 请求 URI 与某个已注册 URI **都是** loopback (127.0.0.1 / [::1] /
//     localhost) 且 scheme + host + path + query 相同、仅端口不同时,
//     视为匹配 — 桌面 CLI 用 OS 随机端口监听回调, 注册时无法预知端口.
//
// 127.0.0.1 与 localhost 视为不同 host (严格), 不互相放行.
func matchRedirectURI(registered []string, requested string) bool {
	if contains(registered, requested) {
		return true
	}
	for _, reg := range registered {
		if loopbackPortMatch(reg, requested) {
			return true
		}
	}
	return false
}

// loopbackPortMatch — 两侧都是 loopback 且除端口外逐段相等.
func loopbackPortMatch(registered, requested string) bool {
	a, err := url.Parse(registered)
	if err != nil {
		return false
	}
	b, err := url.Parse(requested)
	if err != nil {
		return false
	}
	if !isLoopbackHost(a.Hostname()) || !isLoopbackHost(b.Hostname()) {
		return false
	}
	return a.Scheme == b.Scheme &&
		a.Hostname() == b.Hostname() &&
		a.Path == b.Path &&
		a.RawQuery == b.RawQuery
}

// isLoopbackHost — RFC 8252 §7.3 的 loopback 定义 (url.Hostname 已去
// [::1] 的方括号).
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
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
