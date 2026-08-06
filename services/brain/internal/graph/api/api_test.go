package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

func newSrv(t *testing.T) *Server {
	t.Helper()
	v := bauth.NewVerifier("test-secret-very-long-string-for-hmac-32", "iss", "aud")
	return NewServer(nil, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRequireAuthMissingBearer(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("GET",
		"/v1/graph/projects/00000000-0000-0000-0000-000000000000/nodes", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d", rr.Code)
	}
}

func TestRequireAuthBadToken(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("POST",
		"/v1/graph/projects/00000000-0000-0000-0000-000000000000/extract",
		bytes.NewReader([]byte(`{"content":{}}`)))
	req.Header.Set("Authorization", "Bearer junk")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d", rr.Code)
	}
}
