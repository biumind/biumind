// OAuth 浏览器登录流 e2e — 真 PG + 真 Server (跟 refresh_e2e_test.go 同 harness).
//
// 覆盖 BiuMind-CLI-OAuth-Login-Plan S3/S4:
//   - 浏览器 (Accept: text/html) 无 session 请求 authorize → 302 /oauth/login
//     且 return_to 原样带回完整 query
//   - API 客户端无 session → 401 login_required JSON (老行为不变)
//   - POST /oauth/login 错误密码 → 重渲染 + 错误文案 (不泄露用户存在性)
//   - 正确密码 → Set-Cookie bm_session (HttpOnly/Lax) + 302 return_to
//   - 带 bm_session cookie 走 authorize → loopback 随机端口放行出 code (S1+S4)
//   - Bearer header 老路径回归
package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedOAuthClientE2E 插入一个带 alias 的 public client (等价 00018 migration
// 的 biu-cli 种子). client_alias 列不存在 (migration 未跑) 时 skip.
func seedOAuthClientE2E(t *testing.T, pool *pgxpool.Pool, alias string, redirectURIs []string) {
	t.Helper()
	ctx := context.Background()
	var hasAlias bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'identity' AND table_name = 'oauth_clients'
			  AND column_name = 'client_alias'
		)
	`).Scan(&hasAlias); err != nil {
		t.Fatal(err)
	}
	if !hasAlias {
		t.Skip("identity.oauth_clients.client_alias missing — apply migration 00018 first")
	}
	clientID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.oauth_clients
			(client_id, client_alias, client_name, redirect_uris,
			 grant_types, response_types, token_endpoint_auth_method, scope)
		VALUES ($1, $2, 'OAuth E2E Client', $3,
			 ARRAY['authorization_code','refresh_token']::text[],
			 ARRAY['code']::text[], 'none', '')
	`, clientID, alias, redirectURIs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.oauth_authorization_codes WHERE client_id = $1`, clientID)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.oauth_clients WHERE client_id = $1`, clientID)
	})
}

func doForm(mux *http.ServeMux, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestOAuthBrowserLoginFlowE2E(t *testing.T) {
	s, mux, pool := newE2EServer(t)

	email := fmt.Sprintf("oauth-e2e-%s@example.com", uuid.NewString()[:8])
	pw := "test-password-long-enough"
	access, _ := loginE2E(t, mux, pool, s, email, pw, "oauth-e2e-install")

	alias := "e2e-cli-" + uuid.NewString()[:8]
	seedOAuthClientE2E(t, pool, alias, []string{"http://127.0.0.1/callback"})

	// CLI 真实请求形态: client_id=alias + loopback 随机端口.
	challenge := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	authorizeURL := "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {alias},
		"redirect_uri":          {"http://127.0.0.1:55321/callback"},
		"scope":                 {""},
		"state":                 {"st-123"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	// ── 1. 浏览器无 session → 302 登录页, return_to 原样带回 ──
	req := httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("browser no-session: got %d want 302; body=%s", rr.Code, rr.Body.String())
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil || loc.Path != "/oauth/login" {
		t.Fatalf("browser no-session: Location = %q, want /oauth/login", rr.Header().Get("Location"))
	}
	if got := loc.Query().Get("return_to"); got != authorizeURL {
		t.Errorf("return_to mismatch:\n got %q\nwant %q", got, authorizeURL)
	}

	// ── 2. API 客户端无 session → 401 login_required JSON (老行为) ──
	req = httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("api no-session: got %d want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "login_required") {
		t.Errorf("api no-session: body should contain login_required, got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Error("api no-session: WWW-Authenticate missing")
	}

	// ── 3. POST /oauth/login 错误密码 → 401 重渲染 + 错误文案 ──
	rr = doForm(mux, "/oauth/login", url.Values{
		"email": {email}, "password": {"wrong-password"}, "return_to": {authorizeURL},
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("login wrong pw: got %d want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "邮箱或密码不正确") {
		t.Error("login wrong pw: generic error message missing")
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Error("login wrong pw: must not set bm_session cookie")
		}
	}

	// ── 3b. POST 外域 return_to → 400 ──
	rr = doForm(mux, "/oauth/login", url.Values{
		"email": {email}, "password": {pw},
		"return_to": {"https://evil.com/oauth/authorize?x=1"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("login external return_to: got %d want 400", rr.Code)
	}

	// ── 4. POST 正确密码 → bm_session cookie + 302 return_to ──
	rr = doForm(mux, "/oauth/login", url.Values{
		"email": {email}, "password": {pw}, "return_to": {authorizeURL},
	})
	if rr.Code != http.StatusFound {
		t.Fatalf("login ok: got %d want 302; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != authorizeURL {
		t.Errorf("login ok: Location = %q, want %q", got, authorizeURL)
	}
	var session *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("login ok: bm_session cookie missing")
	}
	if !session.HttpOnly || session.Path != "/" || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attrs: HttpOnly=%v Path=%q SameSite=%v",
			session.HttpOnly, session.Path, session.SameSite)
	}

	// ── 5. 带 bm_session cookie 走 authorize → loopback 随机端口放行出 code ──
	req = httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(session)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize with cookie: got %d want 302; body=%s", rr.Code, rr.Body.String())
	}
	cb, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if cb.Scheme != "http" || cb.Hostname() != "127.0.0.1" || cb.Port() != "55321" || cb.Path != "/callback" {
		t.Errorf("authorize with cookie: unexpected redirect %q", cb.String())
	}
	if cb.Query().Get("code") == "" || cb.Query().Get("state") != "st-123" {
		t.Errorf("authorize with cookie: code/state missing in %q", cb.String())
	}

	// ── 6. Bearer header 老路径回归 (uuid client_id + 精确匹配) ──
	uuidClientID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO identity.oauth_clients
			(client_id, client_name, redirect_uris,
			 grant_types, response_types, token_endpoint_auth_method, scope)
		VALUES ($1, 'OAuth E2E UUID Client', ARRAY['https://app.example.com/cb']::text[],
			 ARRAY['authorization_code','refresh_token']::text[],
			 ARRAY['code']::text[], 'none', '')
	`, uuidClientID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.oauth_authorization_codes WHERE client_id = $1`, uuidClientID)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.oauth_clients WHERE client_id = $1`, uuidClientID)
	})
	uuidAuthorizeURL := "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {uuidClientID.String()},
		"redirect_uri":          {"https://app.example.com/cb"},
		"state":                 {"st-uuid"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	req = httptest.NewRequest(http.MethodGet, uuidAuthorizeURL, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize with Bearer: got %d want 302; body=%s", rr.Code, rr.Body.String())
	}
	cb, err = url.Parse(rr.Header().Get("Location"))
	if err != nil || cb.Query().Get("code") == "" || cb.Query().Get("state") != "st-uuid" {
		t.Errorf("authorize with Bearer: bad redirect %q", rr.Header().Get("Location"))
	}

	// ── 7. cookie 无效 + 浏览器 → 302 登录页 (而非 401) ──
	req = httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-a-jwt"})
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound || !strings.HasPrefix(rr.Header().Get("Location"), "/oauth/login") {
		t.Errorf("authorize bad cookie browser: got %d Location=%q, want 302 /oauth/login",
			rr.Code, rr.Header().Get("Location"))
	}
}
