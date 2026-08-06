// S12-1 Trigger client tests —— httptest mock brain endpoints。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClient_CreateTaskSession_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing/wrong Bearer header: %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["mode"] != "task" {
			t.Errorf("mode=%v want task", body["mode"])
		}
		if body["prompt"] != "hi from telegram" {
			t.Errorf("prompt lost: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session_id":"550e8400-e29b-41d4-a716-446655440000","session_token":"tok","expires_at":"2026-12-31T00:00:00Z","mode":"task"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := NewClient(ts.URL, "channel-admin-tok", nil)
	resp, err := c.CreateTaskSession(context.Background(), CreateTaskSessionReq{
		Prompt: "hi from telegram",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != uuid.MustParse("550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("session_id=%v", resp.SessionID)
	}
	if resp.Mode != "task" {
		t.Errorf("mode=%q want task", resp.Mode)
	}
}

func TestClient_CreateTaskSession_NoRuntime(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"no_runtime_available","message":"no online runtime in pool"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := NewClient(ts.URL, "tok", nil)
	_, err := c.CreateTaskSession(context.Background(), CreateTaskSessionReq{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error when no runtime")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", apiErr.Status)
	}
	if !apiErr.IsNoRuntime() {
		t.Errorf("IsNoRuntime() = false; body=%q", apiErr.Body)
	}
}

func TestClient_CreateTaskSession_BadStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := NewClient(ts.URL, "wrong", nil)
	_, err := c.CreateTaskSession(context.Background(), CreateTaskSessionReq{Prompt: "x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", apiErr.Status)
	}
	if apiErr.IsNoRuntime() {
		t.Errorf("401 wrongly identified as no-runtime")
	}
}

func TestClient_CreateTaskSession_NetworkErr(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "tok", nil)
	_, err := c.CreateTaskSession(context.Background(), CreateTaskSessionReq{Prompt: "x"})
	if err == nil {
		t.Fatal("expected network error")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("network err wrongly wrapped as APIError: %v", err)
	}
}
