package ingest

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalServer_MountSkippedWhenTokenEmpty(t *testing.T) {
	mux := http.NewServeMux()
	s := NewInternalServer(nil, "",
		slog.New(slog.NewTextHandler(nopWriter{}, nil)))
	s.Mount(mux)
	// Hitting the route should 404 because Mount registered nothing.
	r := httptest.NewRequest(http.MethodGet,
		"/v1/internal/wiki/sources/00000000-0000-0000-0000-000000000000?owner_id=x", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 (no route), got %d", w.Code)
	}
}

func TestInternalServer_RejectsMissingToken(t *testing.T) {
	s := mountWithToken(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet,
		"/v1/internal/wiki/sources/00000000-0000-0000-0000-000000000000?owner_id=00000000-0000-0000-0000-000000000000",
		nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", w.Code)
	}
}

func TestInternalServer_RejectsWrongToken(t *testing.T) {
	s := mountWithToken(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet,
		"/v1/internal/wiki/sources/00000000-0000-0000-0000-000000000000?owner_id=00000000-0000-0000-0000-000000000000",
		nil)
	r.Header.Set("X-Biumind-Internal-Token", "wrong-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 with wrong token, got %d", w.Code)
	}
}

func TestInternalServer_RejectsBadIDOnTokenedRoute(t *testing.T) {
	// Even with a good token, malformed UUIDs return 400 — not 401 —
	// so the worker can distinguish "auth wrong" vs "i sent garbage".
	s := mountWithToken(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet,
		"/v1/internal/wiki/sources/not-a-uuid?owner_id=00000000-0000-0000-0000-000000000000",
		nil)
	r.Header.Set("X-Biumind-Internal-Token", "secret-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for bad UUID, got %d (body=%s)",
			w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad_id") {
		t.Errorf("expected bad_id in body, got %s", w.Body.String())
	}
}

func TestInternalServer_RejectsMissingOwnerID(t *testing.T) {
	s := mountWithToken(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet,
		"/v1/internal/wiki/sources/00000000-0000-0000-0000-000000000000",
		nil)
	r.Header.Set("X-Biumind-Internal-Token", "secret-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 without owner_id, got %d", w.Code)
	}
}

// ─── helpers ───────────────────────────────────────────────────

func mountWithToken(t *testing.T, token string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	s := NewInternalServer(nil, token,
		slog.New(slog.NewTextHandler(nopWriter{}, nil)))
	s.Mount(mux)
	return mux
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
