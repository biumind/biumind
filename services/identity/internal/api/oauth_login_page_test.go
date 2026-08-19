package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// validateReturnTo 矩阵 — 只允许同源 /oauth/authorize, 防 open redirect.
func TestValidateReturnTo(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://id.example.com/oauth/login", nil) // r.Host = id.example.com
	cases := []struct {
		name string
		in   string
		want string // "" = 拒绝
	}{
		{"relative authorize", "/oauth/authorize?client_id=x&state=y", "/oauth/authorize?client_id=x&state=y"},
		{"absolute same host https", "https://id.example.com/oauth/authorize?client_id=x", "/oauth/authorize?client_id=x"},
		{"absolute same host http", "http://id.example.com/oauth/authorize?client_id=x", "/oauth/authorize?client_id=x"},
		{"host case-insensitive", "https://ID.Example.COM/oauth/authorize?client_id=x", "/oauth/authorize?client_id=x"},
		{"empty", "", ""},
		{"external domain", "https://evil.com/oauth/authorize?client_id=x", ""},
		{"external domain http", "http://evil.com/oauth/authorize?client_id=x", ""},
		{"scheme-relative external", "//evil.com/oauth/authorize?client_id=x", ""},
		{"javascript scheme", "javascript:alert(1)", ""},
		{"wrong path", "/oauth/token?client_id=x", ""},
		{"wrong path same host", "https://id.example.com/v1/auth/login?x=1", ""},
		{"no query", "/oauth/authorize", ""},
		{"not a url", "%%%", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := validateReturnTo(r, tc.in)
			if tc.want == "" {
				if ok {
					t.Errorf("validateReturnTo(%q) should reject, got %q", tc.in, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("validateReturnTo(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
			}
		})
	}
}

// GET /oauth/login 的基础行为不需要 DB — return_to 非法直接 400.
func TestOAuthLoginPageRejectsBadReturnTo(t *testing.T) {
	srv := newTestServer(t, nil)
	for _, rt := range []string{"", "https://evil.com/oauth/authorize?x=1", "/v1/auth/login?x=1"} {
		path := "/oauth/login"
		if rt != "" {
			path += "?return_to=" + url.QueryEscape(rt)
		}
		rr := do(srv, http.MethodGet, path, nil, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("return_to=%q: got %d want 400", rt, rr.Code)
		}
	}
}

// 合法 return_to → 200 + 表单 + 安全响应头 (CSP / nosniff / no-store).
func TestOAuthLoginPageRenders(t *testing.T) {
	srv := newTestServer(t, nil)
	rt := "/oauth/authorize?client_id=biu-cli&response_type=code"
	rr := do(srv, http.MethodGet, "/oauth/login?return_to="+url.QueryEscape(rt), nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `name="return_to"`) || !strings.Contains(body, `name="password"`) {
		t.Error("login form fields missing from rendered page")
	}
	if got := rr.Header().Get("Content-Security-Policy"); got != "default-src 'none'; style-src 'unsafe-inline'" {
		t.Errorf("CSP = %q", got)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options missing")
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Error("Cache-Control no-store missing")
	}
}
