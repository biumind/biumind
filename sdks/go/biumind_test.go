package biumind

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func TestRelayClient_Messages(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "claude-test" {
			t.Errorf("model: %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","content":[{"text":"hi"}]}`))
	}))

	client := NewRelayClient(Config{RelayURL: ts.URL, Token: "t"})
	raw, err := client.Messages(context.Background(), MessagesRequest{
		Model:    "claude-test",
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if !strings.Contains(string(raw), `"msg_1"`) {
		t.Errorf("body: %s", raw)
	}
}

func TestRelayClient_Stream(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start"}`,
		``,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}}`,
		``,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))

	client := NewRelayClient(Config{RelayURL: ts.URL, Token: "t"})
	chunks, errs := client.MessagesStream(context.Background(), MessagesRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	var got string
	for c := range chunks {
		got += c
	}
	for err := range errs {
		t.Fatalf("stream err: %v", err)
	}
	if got != "hello" {
		t.Errorf("text: %q", got)
	}
}

func TestRelayClient_RateLimitError(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rpm"}`))
	}))
	client := NewRelayClient(Config{RelayURL: ts.URL, Token: "t"})
	_, err := client.Messages(context.Background(), MessagesRequest{Model: "m"})
	var be *Error
	if !asError(err, &be) {
		t.Fatalf("expected *biumind.Error, got %T %v", err, err)
	}
	if !be.IsRateLimit() {
		t.Errorf("expected 429, got %d", be.Status)
	}
	if be.RetryAfter.Seconds() != 12 {
		t.Errorf("retry after: %v", be.RetryAfter)
	}
}

func TestMemoryClient_StoreAndRecall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/memory", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["project_id"] != "p" {
			t.Fatalf("project: %v", body["project_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mem_1","project_id":"p","kind":"recall","content":"hi","salience":0.5,"created_at":"2026-01-01T00:00:00Z","last_accessed_at":"2026-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("GET /v1/memory/recall", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"memories":[{"id":"mem_1","project_id":"p","kind":"recall","content":"hi","salience":0.5,"score":1.23,"created_at":"2026-01-01T00:00:00Z","last_accessed_at":"2026-01-01T00:00:00Z"}],"mode":"hybrid","query":"hi"}`))
	})
	ts := newTestServer(t, mux)

	client := NewMemoryClient(Config{RelayURL: ts.URL, Token: "t"})
	m, err := client.Store(context.Background(), "p", "hi", StoreOptions{})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if m.ID != "mem_1" {
		t.Errorf("id: %s", m.ID)
	}
	r, err := client.Recall(context.Background(), "p", "hi", RecallOptions{})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if r.Mode != "hybrid" {
		t.Errorf("mode: %s", r.Mode)
	}
	if r.Memories[0].Score == nil || *r.Memories[0].Score != 1.23 {
		t.Errorf("score: %+v", r.Memories[0].Score)
	}
}

func TestMemoryClient_AuthError(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	client := NewMemoryClient(Config{RelayURL: ts.URL, Token: "t"})
	_, err := client.List(context.Background(), "p", ListOptions{})
	var be *Error
	if !asError(err, &be) || !be.IsAuth() {
		t.Errorf("expected auth error, got %v", err)
	}
}

func TestMemoryClient_RejectsInvalidKind(t *testing.T) {
	client := NewMemoryClient(Config{RelayURL: "http://x", Token: "t"})
	_, err := client.Store(context.Background(), "p", "x",
		StoreOptions{Kind: "garbage"})
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Errorf("expected invalid kind error, got %v", err)
	}
}

func TestLoadConfig_RequiresEnv(t *testing.T) {
	t.Setenv("BIUMIND_MODEL_RELAY_URL", "")
	t.Setenv("BIUMIND_TOKEN", "")
	if _, err := LoadConfig(); err == nil {
		t.Errorf("expected error when env empty")
	}
	t.Setenv("BIUMIND_MODEL_RELAY_URL", "https://relay")
	t.Setenv("BIUMIND_TOKEN", "tok")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RelayURL != "https://relay" || cfg.BrainURL != "https://relay" {
		t.Errorf("brain default: %s", cfg.BrainURL)
	}
}

// asError is a tiny errors.As-compatible helper kept inline so the
// test file has no extra imports.
func asError[T error](err error, target *T) bool {
	for e := err; e != nil; {
		if v, ok := e.(T); ok {
			*target = v
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
