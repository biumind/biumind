package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

func newSrv(t *testing.T) *Server {
	t.Helper()
	verifier := bauth.NewVerifier("test-secret-very-long-string-for-hmac-32", "iss", "aud")
	return NewServer(nil, nil, verifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRequireAuthMissingBearer(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("POST", "/v1/wiki/projects", bytes.NewReader([]byte(`{"name":"x"}`)))
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
	req := httptest.NewRequest("POST", "/v1/wiki/projects", bytes.NewReader([]byte(`{"name":"x"}`)))
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d", rr.Code)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	signer := bauth.NewSigner("test-secret-very-long-string-for-hmac-32", "iss", "aud", time.Minute)
	tok, _ := signer.Sign(&bauth.Claims{UserID: "11111111-1111-1111-1111-111111111111"})

	req := httptest.NewRequest("POST", "/v1/wiki/projects", bytes.NewReader([]byte(`{"name":""}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClipMissingContent(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	signer := bauth.NewSigner("test-secret-very-long-string-for-hmac-32", "iss", "aud", time.Minute)
	tok, _ := signer.Sign(&bauth.Claims{UserID: "11111111-1111-1111-1111-111111111111"})

	body, _ := json.Marshal(clipReq{URL: "https://x.example", Title: "T"})
	req := httptest.NewRequest("POST", "/v1/wiki/projects/22222222-2222-2222-2222-222222222222/sources/clip",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "missing_content") {
		t.Errorf("expected missing_content; got %s", rr.Body.String())
	}
}
