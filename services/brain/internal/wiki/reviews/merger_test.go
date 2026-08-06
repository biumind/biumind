package reviews

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// These tests cover the API-layer validation that doesn't need a DB.
// The actual MergePages SQL behaviour (block reassignment, chunk
// rewrite, soft-delete + frontmatter hint, version bump, events) is
// validated by integration tests gated on DATABASE_URL — added in a
// follow-up so this commit stays self-contained.

func TestHandleMerge_RejectsBadCanonicalID(t *testing.T) {
	mux := http.NewServeMux()
	srv := NewServer(nil, nil, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	srv.Mount(mux)

	// We bypass the JWT middleware for this test by hitting handleMerge
	// directly — but mux only exposes the wrapped handler, and the
	// auth wrapper would short-circuit on missing Bearer. To exercise
	// just the path-param validation, build a request and call the
	// inner handler via the package boundary.
	r := httptest.NewRequest(http.MethodPost,
		"/v1/wiki/pages/not-a-uuid/merge",
		strings.NewReader(`{"from_id": "00000000-0000-0000-0000-000000000000"}`))
	r.SetPathValue("id", "not-a-uuid")
	w := httptest.NewRecorder()
	srv.handleMerge(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for bad canonical id, got %d (body=%s)",
			w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad_id") {
		t.Errorf("expected bad_id, got %s", w.Body.String())
	}
}

func TestHandleMerge_RejectsBadFromID(t *testing.T) {
	srv := NewServer(nil, nil, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	r := httptest.NewRequest(http.MethodPost,
		"/v1/wiki/pages/"+uuid.NewString()+"/merge",
		strings.NewReader(`{"from_id": "not-a-uuid"}`))
	r.SetPathValue("id", uuid.NewString())
	// Stamp claims onto context so mustUserID downstream doesn't panic
	// — the validation runs before mustUserID, but if it ever moves
	// this test stays valid.
	w := httptest.NewRecorder()
	srv.handleMerge(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for bad from_id, got %d (body=%s)",
			w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad_from_id") {
		t.Errorf("expected bad_from_id, got %s", w.Body.String())
	}
}

func TestHandleMerge_RejectsMissingBody(t *testing.T) {
	srv := NewServer(nil, nil, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	r := httptest.NewRequest(http.MethodPost,
		"/v1/wiki/pages/"+uuid.NewString()+"/merge", nil)
	r.SetPathValue("id", uuid.NewString())
	w := httptest.NewRecorder()
	srv.handleMerge(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing body, got %d", w.Code)
	}
}

// TestFindReviewByKey_ReturnsNilOnNoMatch is a smoke test that we can
// instantiate the helper; the full DB-backed lookup lives in the
// integration suite. This guards against future refactors of
// IDByDedupeKey breaking the call chain silently.
func TestFindReviewByKey_NilSafeWithoutStore(t *testing.T) {
	// Calling findReviewByKey on a Server with a nil Store must not
	// panic; it must return nil.
	srv := &Server{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("findReviewByKey with nil store panicked: %v", r)
		}
	}()
	if got := srv.findReviewByKey(context.Background(), "any-key"); got != nil {
		t.Errorf("expected nil result with nil store, got %+v", got)
	}
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
