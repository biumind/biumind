package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/sandbox/internal/driver"
)

const (
	testSecret = "test-secret-very-long-string-for-hmac-32"
	testIss    = "iss"
	testAud    = "aud"
)

func newServer(t *testing.T) (*Server, *driver.Stub) {
	t.Helper()
	stub := driver.NewStub()
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	srv := NewServer(stub, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv, stub
}

func mintToken(t *testing.T, userID string) string {
	t.Helper()
	s := bauth.NewSigner(testSecret, testIss, testAud, time.Hour)
	tok, err := s.Sign(&bauth.Claims{UserID: userID, DeviceID: "test"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func TestRequireAuthMissingBearer(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestCreateGetDestroyRoundtrip(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	// create
	body := []byte(`{"image":"alpine:3.20"}`)
	req := httptest.NewRequest("POST", "/v1/sandboxes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id := created["id"].(string)
	if id == "" || created["owner_id"] != "u-1" {
		t.Fatalf("unexpected create payload: %+v", created)
	}

	// get
	req = httptest.NewRequest("GET", "/v1/sandboxes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}

	// destroy
	req = httptest.NewRequest("DELETE", "/v1/sandboxes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("destroy: %d", rr.Code)
	}

	// post-destroy get → 404
	req = httptest.NewRequest("GET", "/v1/sandboxes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestOwnerScoping(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)

	tokA := mintToken(t, "alice")
	tokB := mintToken(t, "bob")

	// alice creates a sandbox
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tokA)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id := created["id"].(string)

	// bob can't see it
	req = httptest.NewRequest("GET", "/v1/sandboxes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tokB)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403 for cross-tenant get, got %d", rr.Code)
	}

	// bob can't destroy it
	req = httptest.NewRequest("DELETE", "/v1/sandboxes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tokB)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403 for cross-tenant destroy, got %d", rr.Code)
	}
}

func TestExecStreamsSSE(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	// create
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id := created["id"].(string)

	// exec: print "hello" then exit 0
	body := []byte(`{"argv":["sh","-c","echo hello"]}`)
	req = httptest.NewRequest("POST", "/v1/sandboxes/"+id+"/exec", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	// httptest.ResponseRecorder doesn't implement http.Flusher; wrap it so
	// the SSE handler's `w.(http.Flusher)` assertion succeeds.
	rec := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	mux.ServeHTTP(rec, req.WithContext(context.Background()))
	rr = rec.ResponseRecorder
	if rr.Code != http.StatusOK {
		t.Fatalf("exec: %d %s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	if !strings.Contains(out, "event: stdout") || !strings.Contains(out, "hello") {
		t.Errorf("missing stdout SSE: %q", out)
	}
	if !strings.Contains(out, "event: exit") || !strings.Contains(out, `"code":0`) {
		t.Errorf("missing exit SSE with code 0: %q", out)
	}
}

// flushableRecorder satisfies http.Flusher on top of the standard
// recorder. Required for handlers that gate on `w.(http.Flusher)` to
// stream SSE.
type flushableRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushableRecorder) Flush() {}

// ─── Quota gate tests ────────────────────────────────────

func TestConcurrentLimitRejects(t *testing.T) {
	srv, _ := newServer(t)
	srv.WithQuota(quota.NewInMemoryLimiter(nil), 2) // cap = 2 concurrent
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-quota")

	create := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	if r := create(); r.Code != http.StatusOK {
		t.Fatalf("first create: %d %s", r.Code, r.Body.String())
	}
	if r := create(); r.Code != http.StatusOK {
		t.Fatalf("second create: %d %s", r.Code, r.Body.String())
	}
	r := create()
	if r.Code != http.StatusTooManyRequests {
		t.Errorf("third create should 429, got %d %s", r.Code, r.Body.String())
	}
	if !strings.Contains(r.Body.String(), "concurrent_limit") {
		t.Errorf("body: %s", r.Body.String())
	}
	if r.Header().Get("Retry-After") == "" {
		t.Errorf("Retry-After header missing")
	}
}

func TestDailyLimitRejects(t *testing.T) {
	srv, _ := newServer(t)
	specs := map[string]quota.Spec{
		"sandbox.daily": {Window: 24 * time.Hour, Limit: 2},
	}
	srv.WithQuota(quota.NewInMemoryLimiter(specs), 0) // concurrent gate disabled
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-daily")

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("third create should 429, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "daily_limit") {
		t.Errorf("body: %s", rr.Body.String())
	}
	if rr.Header().Get("X-RateLimit-Limit") != "2" {
		t.Errorf("X-RateLimit-Limit header: %q", rr.Header().Get("X-RateLimit-Limit"))
	}
}

// TestPerOwnerIsolation — owner B's sandbox creates do NOT count against
// owner A's concurrent cap. Without this, a noisy tenant could starve
// every other tenant by holding `MaxConcurrentPerOwner` slots open.
func TestPerOwnerIsolation(t *testing.T) {
	srv, stub := newServer(t)
	srv.WithQuota(quota.NewInMemoryLimiter(nil), 1) // cap = 1 per owner
	mux := http.NewServeMux()
	srv.Mount(mux)

	tokA := mintToken(t, "u-A")
	tokB := mintToken(t, "u-B")

	create := func(tok string) int {
		req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code
	}

	if c := create(tokA); c != http.StatusOK {
		t.Fatalf("A first create: %d", c)
	}
	// A's cap is full — A's second must 429.
	if c := create(tokA); c != http.StatusTooManyRequests {
		t.Errorf("A second create should 429, got %d", c)
	}
	// B is on a separate budget — must succeed.
	if c := create(tokB); c != http.StatusOK {
		t.Errorf("B first create should 200, got %d", c)
	}
	// Sanity: stub recorded both A's first and B's first; A's second
	// rejection means we never asked the driver to create.
	creates := 0
	for _, call := range stub.Calls {
		if strings.HasPrefix(call, "create ") {
			creates++
		}
	}
	if creates != 2 {
		t.Errorf("expected 2 driver creates (A+B); got %d (calls=%v)",
			creates, stub.Calls)
	}
}

// failingDriver wraps Stub and returns an error from Create exactly
// once. Used to verify refund-on-driver-failure: a daily quota slot
// must NOT be permanently consumed when the driver itself rejected
// the create.
type failingDriver struct {
	*driver.Stub
	failed bool
}

func (f *failingDriver) Create(ctx context.Context, in driver.CreateInput) (*driver.Sandbox, error) {
	if !f.failed {
		f.failed = true
		return nil, io.ErrUnexpectedEOF // any non-nil works
	}
	return f.Stub.Create(ctx, in)
}

func TestRefundOnDriverFailure(t *testing.T) {
	stub := driver.NewStub()
	fd := &failingDriver{Stub: stub}
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	srv := NewServer(fd, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
	specs := map[string]quota.Spec{
		"sandbox.daily": {Window: 24 * time.Hour, Limit: 1},
	}
	srv.WithQuota(quota.NewInMemoryLimiter(specs), 0)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-refund")

	// First create — driver fails. The handler must refund the slot
	// it just reserved, otherwise this owner is locked out for 24h
	// after a transient failure.
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected driver failure 500; got %d %s", rr.Code, rr.Body.String())
	}

	// Second create — slot was refunded, so this should succeed.
	req = httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("second create should 200 (slot refunded); got %d %s",
			rr.Code, rr.Body.String())
	}
}
