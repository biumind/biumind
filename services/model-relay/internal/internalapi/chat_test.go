package internalapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/api"
)

func chatReq(t *testing.T, token string, body map[string]any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/chat", bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestChat_AuthRequired(t *testing.T) {
	s := &Server{Token: "secret", Messages: &api.MessagesHandler{}}
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "wrong", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", w.Code)
	}
}

func TestChat_MissingUserID(t *testing.T) {
	s := &Server{Token: "secret", Messages: &api.MessagesHandler{}}
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "secret", map[string]any{"model": "claude-opus-4-8"}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id: got %d, want 400", w.Code)
	}
}

func TestChat_HandlerNotWired(t *testing.T) {
	s := &Server{Token: "secret"} // Messages nil
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "secret", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil handler: got %d, want 503", w.Code)
	}
}
