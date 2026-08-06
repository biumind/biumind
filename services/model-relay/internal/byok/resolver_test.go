package byok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/internal/byok/u-1/anthropic") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_key": "sk-ant-test",
			"config":  json.RawMessage(`{"region":"us"}`),
		})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	k, err := c.Get(context.Background(), "u-1", "anthropic")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if k.APIKey != "sk-ant-test" {
		t.Fatalf("api_key = %q", k.APIKey)
	}
	if string(k.Config) != `{"region":"us"}` {
		t.Fatalf("config = %s", string(k.Config))
	}
}

func TestGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	_, err := c.Get(context.Background(), "u-1", "anthropic")
	if err != ErrKeyNotFound {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
}

func TestIncrementFailure_AutoInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"auto_invalid": true})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	auto, err := c.IncrementFailure(context.Background(), "u-1", "openai")
	if err != nil {
		t.Fatalf("incr: %v", err)
	}
	if !auto {
		t.Fatal("want auto_invalid=true")
	}
}

func TestIncrementFailure_StillValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"auto_invalid": false})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	auto, _ := c.IncrementFailure(context.Background(), "u-1", "openai")
	if auto {
		t.Fatal("want auto_invalid=false")
	}
}

func TestTouchUsed_204OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	if err := c.TouchUsed(context.Background(), "u-1", "openai"); err != nil {
		t.Fatalf("touch: %v", err)
	}
}

func TestAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xyz" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"api_key": "k"})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "xyz")
	if _, err := c.Get(context.Background(), "u", "openai"); err != nil {
		t.Fatalf("get: %v", err)
	}
}
