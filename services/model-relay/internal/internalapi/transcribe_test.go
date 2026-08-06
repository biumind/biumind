package internalapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/api"
)

// transcribeReq — multipart 路径用 header X-Internal-User-Id 传 user_id。
func transcribeReq(t *testing.T, token, userID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/transcribe",
		strings.NewReader("fake-multipart-body"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if userID != "" {
		r.Header.Set("X-Internal-User-Id", userID)
	}
	return r
}

func TestTranscribe_AuthRequired(t *testing.T) {
	s := &Server{Token: "secret", Transcriptions: &api.TranscriptionsHandler{}}
	mux := http.NewServeMux()
	s.MountTranscribe(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, transcribeReq(t, "", "u1"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, transcribeReq(t, "wrong", "u1"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", w.Code)
	}
}

func TestTranscribe_MissingUserID(t *testing.T) {
	s := &Server{Token: "secret", Transcriptions: &api.TranscriptionsHandler{}}
	mux := http.NewServeMux()
	s.MountTranscribe(mux)

	// 无 X-Internal-User-Id header → 400
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, transcribeReq(t, "secret", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id header: got %d, want 400", w.Code)
	}
}

func TestTranscribe_HandlerNotWired(t *testing.T) {
	s := &Server{Token: "secret"} // Transcriptions nil
	mux := http.NewServeMux()
	s.MountTranscribe(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, transcribeReq(t, "secret", "u1"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil handler: got %d, want 503", w.Code)
	}
}
