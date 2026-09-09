package relaymodel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func testResolver(relayURL string) *Resolver {
	return New(relayURL, "tok", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// 200 → code, TTL 内命中缓存; bearer 契约锁住。
func TestResolver_ModeCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/internal/models/preferred" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("mode"); got != "embedding" {
			t.Errorf("mode = %q", got)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "emb-1"})
	}))
	defer srv.Close()

	r := testResolver(srv.URL)
	if got := r.Mode(context.Background(), "embedding"); got != "emb-1" {
		t.Fatalf("got %q", got)
	}
	if got := r.Mode(context.Background(), "embedding"); got != "emb-1" {
		t.Fatalf("cached got %q", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("expect 1 relay call (cache hit on 2nd); got %d", n)
	}
}

// 404 → "" 且不缓存(下次重试); EnvOr 的 env 覆盖不打 HTTP。
func TestResolver_NotFoundAndEnvOverride(t *testing.T) {
	var calls atomic.Int32
	srv := httpxNotFound(&calls)
	defer srv.Close()

	r := testResolver(srv.URL)
	if got := r.Mode(context.Background(), "chat"); got != "" {
		t.Fatalf("404 should yield empty; got %q", got)
	}
	if got := r.Mode(context.Background(), "chat"); got != "" {
		t.Fatalf("retry got %q", got)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("expect no negative caching (2 calls); got %d", n)
	}

	if got := r.EnvOr(context.Background(), "env.model", "chat"); got != "env.model" {
		t.Errorf("env override should win; got %q", got)
	}
	if got := r.EnvOr(context.Background(), "", "chat"); got != "" {
		t.Errorf("empty env falls to preferred (404 → empty); got %q", got)
	}
}

// relayURL / token 空 → 禁用, 不打 HTTP。
func TestResolver_Disabled(t *testing.T) {
	for _, r := range []*Resolver{
		New("", "tok", nil),
		New("http://relay:7001", "", nil),
	} {
		if got := r.Mode(context.Background(), "chat"); got != "" {
			t.Errorf("disabled resolver should yield empty; got %q", got)
		}
	}
}

func httpxNotFound(calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no usable model", http.StatusNotFound)
	}))
}
