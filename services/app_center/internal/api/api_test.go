package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/tasks"
	"github.com/biumind/biumind/services/app_center/internal/installs"
)

const (
	testSecret = "test-secret-very-long-string-for-hmac-32"
	testIss    = "iss"
	testAud    = "aud"
)

func newSrv(t *testing.T) *Server {
	t.Helper()
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), tasks.New()); err != nil {
		t.Fatalf("register: %v", err)
	}
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	return NewServer(reg, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func mintToken(t *testing.T) string {
	t.Helper()
	s := bauth.NewSigner(testSecret, testIss, testAud, time.Hour)
	tok, err := s.Sign(&bauth.Claims{UserID: "u-1", DeviceID: "test"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func TestRequireAuth(t *testing.T) {
	srv := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest("GET", "/v1/apps", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestListReturnsManifests(t *testing.T) {
	srv := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest("GET", "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"name":"tasks"`) {
		t.Errorf("missing tasks manifest: %s", rr.Body.String())
	}
}

func TestInvokeRoundtrip(t *testing.T) {
	srv := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t)

	body := []byte(`{"action":"create","input":{"title":"buy milk"}}`)
	req := httptest.NewRequest("POST", "/v1/apps/tasks/invoke", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("invoke: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Result map[string]any `json:"result"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Result["title"] != "buy milk" {
		t.Errorf("result: %+v", out)
	}
}

func TestInvokeUnknownApp(t *testing.T) {
	srv := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t)
	req := httptest.NewRequest("POST", "/v1/apps/unknown/invoke",
		bytes.NewReader([]byte(`{"action":"x"}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestInvokeUnknownAction(t *testing.T) {
	srv := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t)
	req := httptest.NewRequest("POST", "/v1/apps/tasks/invoke",
		bytes.NewReader([]byte(`{"action":"ohno","input":{}}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rr.Code)
	}
}

// ─── invoke Authz (P0) ──────────────────────────────────────────────
//
// 通过 InvokeAuthorizer interface 注入 stub, 隔离 *pgxpool.Pool 依赖。
// 覆盖 5 个独立路径: 没装/disabled/Authz 拒绝/Authz 报错/happy path。
// stateless 模式 (无 Installer 也无 invokeAuth) 由 TestInvokeRoundtrip
// 已经间接覆盖。

type stubInvokeAuth struct {
	// 按 (scope, scopeID, identifier) 三元组返回。空字符串 key 段做通配。
	rows map[string]*installs.Installation
	// AuthorizeInvoke 返回的错误。nil 表示 ALLOW。
	authzErr error
	// 调用计数，校验 disabled 路径不会进 Authz。
	authorizeCalls int
}

func (s *stubInvokeAuth) GetByIdentifier(_ context.Context, scope, scopeID, identifier string) (*installs.Installation, error) {
	key := fmt.Sprintf("%s|%s|%s", scope, scopeID, identifier)
	if r, ok := s.rows[key]; ok {
		return r, nil
	}
	return nil, installs.ErrNotFound
}

func (s *stubInvokeAuth) AuthorizeInvoke(_ context.Context, _ *installs.Installation, _, _ string, _ []string) error {
	s.authorizeCalls++
	return s.authzErr
}

func newSrvWithAuth(t *testing.T, auth *stubInvokeAuth) (*Server, *http.ServeMux) {
	t.Helper()
	srv := newSrv(t)
	srv.WithInvokeAuthorizer(auth)
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, mux
}

func invokeReqHTTP(t *testing.T) *http.Request {
	t.Helper()
	body := []byte(`{"action":"create","input":{"title":"buy milk"}}`)
	req := httptest.NewRequest("POST", "/v1/apps/tasks/invoke", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+mintToken(t))
	return req
}

func TestInvokeAuthz_NotInstalled(t *testing.T) {
	auth := &stubInvokeAuth{rows: map[string]*installs.Installation{}}
	_, mux := newSrvWithAuth(t, auth)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, invokeReqHTTP(t))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_installed") {
		t.Errorf("want code=not_installed, got %s", rr.Body.String())
	}
	if auth.authorizeCalls != 0 {
		t.Errorf("AuthorizeInvoke must not be called when install missing")
	}
}

func TestInvokeAuthz_Disabled(t *testing.T) {
	auth := &stubInvokeAuth{rows: map[string]*installs.Installation{
		"user|u-1|tasks": {ID: "i-1", Identifier: "tasks", Scope: "user", ScopeID: "u-1", Enabled: false},
	}}
	_, mux := newSrvWithAuth(t, auth)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, invokeReqHTTP(t))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "install_disabled") {
		t.Errorf("want code=install_disabled, got %s", rr.Body.String())
	}
	if auth.authorizeCalls != 0 {
		t.Errorf("AuthorizeInvoke must not be called when install disabled")
	}
}

func TestInvokeAuthz_Denied(t *testing.T) {
	auth := &stubInvokeAuth{
		rows: map[string]*installs.Installation{
			"user|u-1|tasks": {ID: "i-1", Identifier: "tasks", Scope: "user", ScopeID: "u-1", Enabled: true},
		},
		authzErr: fmt.Errorf("%w: cedar deny", installs.ErrPermissionDenied),
	}
	_, mux := newSrvWithAuth(t, auth)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, invokeReqHTTP(t))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "permission_denied") {
		t.Errorf("want code=permission_denied, got %s", rr.Body.String())
	}
}

func TestInvokeAuthz_AuthzTransportError(t *testing.T) {
	auth := &stubInvokeAuth{
		rows: map[string]*installs.Installation{
			"user|u-1|tasks": {ID: "i-1", Identifier: "tasks", Scope: "user", ScopeID: "u-1", Enabled: true},
		},
		authzErr: errors.New("network unreachable"),
	}
	_, mux := newSrvWithAuth(t, auth)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, invokeReqHTTP(t))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "authz_failed") {
		t.Errorf("want code=authz_failed, got %s", rr.Body.String())
	}
}

func TestInvokeAuthz_HappyPath(t *testing.T) {
	auth := &stubInvokeAuth{
		rows: map[string]*installs.Installation{
			"user|u-1|tasks": {ID: "i-1", Identifier: "tasks", Scope: "user", ScopeID: "u-1", Enabled: true},
		},
	}
	_, mux := newSrvWithAuth(t, auth)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, invokeReqHTTP(t))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rr.Code, rr.Body.String())
	}
	if auth.authorizeCalls != 1 {
		t.Errorf("AuthorizeInvoke calls=%d, want 1", auth.authorizeCalls)
	}
}
