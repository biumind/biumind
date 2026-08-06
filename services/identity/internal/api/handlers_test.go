package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/token"
)

// Note: These tests focus on validation / auth path / response shape.
// Full integration with Postgres lives in CI smoke (docker-compose).

func TestRegisterValidation(t *testing.T) {
	srv := newTestServer(t, nil)
	cases := []struct {
		name string
		body any
		want int
	}{
		{"bad email", map[string]any{"email": "x", "password": "longpassword"}, http.StatusBadRequest},
		{"weak password", map[string]any{"email": "a@b.co", "password": "short"}, http.StatusBadRequest},
		{"empty body", map[string]any{}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doJSON(srv, "POST", "/v1/auth/register", tc.body)
			if rr.Code != tc.want {
				t.Errorf("got %d want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// TestLoginInvalidCreds skipped pending Store interface refactor (P1.B follow-up).

func TestRequireAuthMiddleware(t *testing.T) {
	srv := newTestServer(t, nil)
	rr := do(srv, "GET", "/v1/identity/me", nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d want 401", rr.Code)
	}
}

// ─── helpers ────────────────────────────────────────────

func newTestServer(t *testing.T, _ *mockStore) *http.ServeMux {
	t.Helper()
	signer := bauth.NewSigner("test-secret-very-long-string-for-hmac-32", "iss", "aud", time.Minute)
	verifier := bauth.NewVerifier("test-secret-very-long-string-for-hmac-32", "iss", "aud")
	s := &Server{
		Store:     nil, // tests that hit DB will explicitly skip / use mock
		Signer:    signer,
		Verifier:  verifier,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
		PasswordParams: passwords.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func doJSON(mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return do(mux, method, path, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
}

func do(mux *http.ServeMux, method, path string, body *bytes.Reader, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// mockStore reserved for future Store interface refactor (P1.B follow-up).
type mockStore struct{}

func TestTokenHelperRoundTrip(t *testing.T) {
	full, hash, err := token.Generate(token.RefreshTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !token.VerifyHash(full, hash) {
		t.Fatal("verify failed")
	}
}
