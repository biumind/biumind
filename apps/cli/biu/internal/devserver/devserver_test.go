package devserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stubInvoker struct {
	gotSlug   string
	gotAction string
	out       any
	err       error
}

func (s *stubInvoker) Invoke(_ context.Context, slug, action string, _ json.RawMessage) (any, error) {
	s.gotSlug = slug
	s.gotAction = action
	return s.out, s.err
}

func startTestServer(t *testing.T, inv Invoker) *Server {
	t.Helper()
	s := New(inv)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	addr, _, err := s.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(http.DefaultClient.CloseIdleConnections)
	// Cache addr for tests via the server; verify Addr() echoes it.
	if got := s.Addr(); got != addr {
		t.Fatalf("Addr mismatch: %s vs %s", got, addr)
	}
	// Tiny settle so listener is fully up.
	time.Sleep(20 * time.Millisecond)
	return s
}

func TestHealth(t *testing.T) {
	s := startTestServer(t, nil)
	resp, err := http.Get("http://" + s.Addr() + "/v1/dev/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != true {
		t.Errorf("ok=%v", body["ok"])
	}
}

func TestListAppsEmpty(t *testing.T) {
	s := startTestServer(t, nil)
	resp, err := http.Get("http://" + s.Addr() + "/v1/dev/apps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct{ Apps []DevApp }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(body.Apps))
	}
}

func TestUpsertAndList(t *testing.T) {
	s := startTestServer(t, nil)
	s.UpsertApp(DevApp{Slug: "rss", Identifier: "rss", Title: "RSS", Version: "0.1.0"})
	s.UpsertApp(DevApp{Slug: "tasks", Identifier: "tasks", Title: "Tasks", Version: "0.2.0"})
	resp, _ := http.Get("http://" + s.Addr() + "/v1/dev/apps")
	defer resp.Body.Close()
	var body struct{ Apps []DevApp }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Apps) != 2 {
		t.Fatalf("expected 2, got %d", len(body.Apps))
	}
	// SetApps should drop entries.
	s.SetApps([]DevApp{{Slug: "rss"}})
	resp2, _ := http.Get("http://" + s.Addr() + "/v1/dev/apps")
	defer resp2.Body.Close()
	var body2 struct{ Apps []DevApp }
	_ = json.NewDecoder(resp2.Body).Decode(&body2)
	if len(body2.Apps) != 1 || body2.Apps[0].Slug != "rss" {
		t.Errorf("SetApps did not replace: %+v", body2.Apps)
	}
}

func TestInvokeRoutesToInvoker(t *testing.T) {
	stub := &stubInvoker{out: map[string]any{"items": []int{1, 2, 3}}}
	s := startTestServer(t, stub)
	resp, err := http.Post(
		"http://"+s.Addr()+"/v1/dev/apps/rss/invoke",
		"application/json",
		strings.NewReader(`{"action":"fetch","input":{"feed":"a"}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if stub.gotSlug != "rss" || stub.gotAction != "fetch" {
		t.Errorf("invoker not called with right args: slug=%s action=%s",
			stub.gotSlug, stub.gotAction)
	}
}

func TestInvokeMissingActionRejected(t *testing.T) {
	s := startTestServer(t, &stubInvoker{})
	resp, _ := http.Post(
		"http://"+s.Addr()+"/v1/dev/apps/rss/invoke",
		"application/json",
		strings.NewReader(`{}`),
	)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestInvokeInvokerError(t *testing.T) {
	s := startTestServer(t, &stubInvoker{err: errors.New("boom")})
	resp, _ := http.Post(
		"http://"+s.Addr()+"/v1/dev/apps/rss/invoke",
		"application/json",
		strings.NewReader(`{"action":"fetch"}`),
	)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 on invoke error, got %d", resp.StatusCode)
	}
}

func TestInvokeWithoutInvoker503(t *testing.T) {
	s := startTestServer(t, nil)
	resp, _ := http.Post(
		"http://"+s.Addr()+"/v1/dev/apps/rss/invoke",
		"application/json",
		strings.NewReader(`{"action":"fetch"}`),
	)
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

func TestPushEventBufferRing(t *testing.T) {
	s := New(nil)
	for i := 0; i < 75; i++ {
		s.PushEvent(Event{Kind: EventSubprocLog, Message: "x"})
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) != s.maxBuf {
		t.Errorf("ring buffer size: got %d want %d", len(s.events), s.maxBuf)
	}
}

func TestManifest404OnUnknownSlug(t *testing.T) {
	s := startTestServer(t, nil)
	resp, _ := http.Get("http://" + s.Addr() + "/v1/dev/apps/missing/manifest")
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestManifestServesUpsertedApp(t *testing.T) {
	s := startTestServer(t, nil)
	s.UpsertApp(DevApp{
		Slug:     "rss",
		Manifest: map[string]any{"name": "rss", "version": "0.1.0"},
	})
	resp, _ := http.Get("http://" + s.Addr() + "/v1/dev/apps/rss/manifest")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["name"] != "rss" {
		t.Errorf("manifest body: %+v", body)
	}
}
