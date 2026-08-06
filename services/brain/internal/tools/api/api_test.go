package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// stubVerifier short-circuits the bearer check in tests by always
// resolving to the given user id.
func newServerForTest(t *testing.T) (*Server, *tools.Registry, uuid.UUID) {
	t.Helper()
	uid := uuid.New()
	reg := tools.New()
	srv := &Server{
		Registry: reg,
		Verifier: nil, // bypass via wrapper below
		Logger:   slog.Default(),
	}
	_ = uid
	return srv, reg, uid
}

// callWithUser bypasses the JWT verifier so we can test the handler
// in isolation. Invokes handler with context already populated.
func callWithUser(t *testing.T, h http.HandlerFunc, uid uuid.UUID,
	method, path string, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{
		UserID: uid.String(),
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestListReturnsAvailableForCloud(t *testing.T) {
	srv, reg, uid := newServerForTest(t)
	reg.MustRegister(tools.Tool{Descriptor: tools.Descriptor{
		Name: "wiki.search", Runtime: tools.RuntimeBoth,
	}})
	reg.MustRegister(tools.Tool{Descriptor: tools.Descriptor{
		Name: "fs.read", Runtime: tools.RuntimeClient,
	}})

	rec := callWithUser(t, srv.handleList, uid, "GET", "/v1/tools", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Mode  string           `json:"execution_mode"`
		Tools []map[string]any `json:"tools"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Mode != "cloud" {
		t.Errorf("mode default: %q", resp.Mode)
	}
	if len(resp.Tools) != 1 || resp.Tools[0]["name"] != "wiki.search" {
		t.Errorf("expected 1 cloud tool, got %+v", resp.Tools)
	}
}

func TestListClientFilter(t *testing.T) {
	srv, reg, uid := newServerForTest(t)
	reg.MustRegister(tools.Tool{Descriptor: tools.Descriptor{
		Name: "fs.read", Runtime: tools.RuntimeClient,
	}})
	reg.MustRegister(tools.Tool{Descriptor: tools.Descriptor{
		Name: "wiki.search", Runtime: tools.RuntimeCloud,
	}})

	rec := callWithUser(t, srv.handleList, uid,
		"GET", "/v1/tools?execution_mode=client", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var resp struct {
		Tools []map[string]any `json:"tools"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Tools) != 1 || resp.Tools[0]["name"] != "fs.read" {
		t.Errorf("expected fs.read only, got %+v", resp.Tools)
	}
}

func TestInvokeRoundTripWithUserContext(t *testing.T) {
	srv, reg, uid := newServerForTest(t)
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(ctx context.Context, in json.RawMessage) (any, error) {
			// Critical: this asserts the user id reaches the tool.
			gotUID := tools.UserIDFromContext(ctx)
			if gotUID != uid {
				t.Errorf("user id missing: got %v want %v", gotUID, uid)
			}
			return map[string]any{"got": string(in)}, nil
		},
	})

	rec := callWithUser(t, srv.handleInvoke, uid, "POST",
		"/v1/tools/invoke", `{"name":"echo","input":{"x":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Name       string         `json:"name"`
		Result     map[string]any `json:"result"`
		DurationMs int64          `json:"duration_ms"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Name != "echo" {
		t.Errorf("name: %q", resp.Name)
	}
	if resp.Result["got"] != `{"x":1}` {
		t.Errorf("payload: %+v", resp.Result)
	}
	if resp.DurationMs < 0 {
		t.Errorf("duration: %d", resp.DurationMs)
	}
}

func TestInvokeUnknownTool404(t *testing.T) {
	srv, _, uid := newServerForTest(t)
	rec := callWithUser(t, srv.handleInvoke, uid, "POST",
		"/v1/tools/invoke", `{"name":"nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeWrongRuntime403(t *testing.T) {
	srv, reg, uid := newServerForTest(t)
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{
			Name: "client_only", Runtime: tools.RuntimeClient,
		},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "should not run", nil
		},
	})
	rec := callWithUser(t, srv.handleInvoke, uid, "POST",
		"/v1/tools/invoke",
		`{"name":"client_only","execution_mode":"cloud"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeDescriptorOnly400(t *testing.T) {
	srv, reg, uid := newServerForTest(t)
	if err := reg.RegisterDescriptor(tools.Descriptor{
		Name: "client_fs", Runtime: tools.RuntimeBoth,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	rec := callWithUser(t, srv.handleInvoke, uid, "POST",
		"/v1/tools/invoke", `{"name":"client_fs"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeMissingNameRejected(t *testing.T) {
	srv, _, uid := newServerForTest(t)
	rec := callWithUser(t, srv.handleInvoke, uid, "POST",
		"/v1/tools/invoke", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rec.Code)
	}
}
