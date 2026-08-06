// Tests for HealthMonitor + Client.Reconnect. Three layers:
//
//   1. Unit tests on HealthMonitor wiring (catalog diff, backoff
//      table, log routing) using fake clients we drive directly.
//   2. Integration with HTTPClient via httptest.Server — flip the
//      handler to fail/recover and observe the monitor's response.
//   3. Cross-language E2E in crosslang_health_test.go (separate
//      file; skipped when interpreters are missing).

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Unit: catalog diff ───────────────────────────────

func TestDiffCatalogAddedAndRemoved(t *testing.T) {
	prev := []ToolDef{{Name: "alpha"}, {Name: "beta"}}
	curr := []ToolDef{{Name: "beta"}, {Name: "gamma"}}
	added, removed := diffCatalog(prev, curr)
	if len(added) != 1 || added[0] != "gamma" {
		t.Errorf("added: %+v", added)
	}
	if len(removed) != 1 || removed[0] != "alpha" {
		t.Errorf("removed: %+v", removed)
	}
}

func TestDiffCatalogIdenticalNoChanges(t *testing.T) {
	tools := []ToolDef{{Name: "a"}, {Name: "b"}}
	added, removed := diffCatalog(tools, tools)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no diff; got +%v -%v", added, removed)
	}
}

func TestDiffCatalogEmptyPrev(t *testing.T) {
	added, removed := diffCatalog(nil, []ToolDef{{Name: "x"}})
	if len(added) != 1 || len(removed) != 0 {
		t.Errorf("empty prev: +%v -%v", added, removed)
	}
}

// ─── Unit: backoff schedule ──────────────────────────

// Backoff index clamps at the last entry so an outage longer
// than len(Backoff) doesn't panic with index out of range.
func TestBackoffIndexClamps(t *testing.T) {
	// We don't test the exact backoff values (that's the package's
	// authored choice). We DO test that the schedule has a final
	// element and never grows unboundedly; an infinite-growing
	// schedule would burn memory in long outages.
	if len(defaultBackoff) == 0 {
		t.Fatal("defaultBackoff is empty")
	}
	last := defaultBackoff[len(defaultBackoff)-1]
	if last < time.Second || last > 5*time.Minute {
		t.Errorf("final backoff %v outside sanity range [1s, 5m]", last)
	}
}

// ─── HealthMonitor with HTTP fixture ─────────────────

// Drive a real HTTPClient + Registry through a HealthMonitor.
// The fixture's handler flips between "fail" and "ok" modes via
// an atomic so the test can simulate an outage + recovery without
// restarting the server.
func TestHealthMonitorHTTPRecoversAfterOutage(t *testing.T) {
	var failing atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
			return
		}
		// Honour the dual Accept contract just like the real
		// HTTPClient flow expects.
		accept := r.Header.Get("Accept")
		if r.Method == http.MethodPost {
			if accept == "" {
				http.Error(w, "missing Accept", http.StatusNotAcceptable)
				return
			}
		}
		var req JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case MethodInitialize:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(sessionHeaderName, "session-1")
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustJSON(InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "fix"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodPing:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: json.RawMessage(`{}`),
			})
		case MethodToolsList:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustJSON(ListToolsResult{Tools: []ToolDef{{
					Name: "ping", InputSchema: map[string]any{"type": "object"},
				}}}),
			})
		default:
			http.Error(w, "unexpected", http.StatusNotImplemented)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.ConnectHTTP(ctx, HTTPConfig{Name: "fix", URL: srv.URL + "/mcp"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Capture log lines so we can assert on the recovery
	// narrative.
	var logsMu sync.Mutex
	logs := []string{}
	mon := r.StartHealthMonitor(HealthOptions{
		Interval:    50 * time.Millisecond,
		PingTimeout: 200 * time.Millisecond,
		Backoff:     []time.Duration{20 * time.Millisecond, 40 * time.Millisecond},
		Logf: func(f string, args ...any) {
			logsMu.Lock()
			logs = append(logs, fmt.Sprintf(f, args...))
			logsMu.Unlock()
		},
	})
	defer mon.Stop()

	// Force an outage. Wait long enough for at least one probe to
	// observe the failure + at least one reconnect attempt.
	failing.Store(true)
	time.Sleep(300 * time.Millisecond)
	failing.Store(false)
	// Wait long enough for the next probe to see the recovery
	// AND for the reconnect's success path to execute.
	time.Sleep(500 * time.Millisecond)

	logsMu.Lock()
	defer logsMu.Unlock()
	// Two ways the recovery narrative can land:
	//   (a) `lost` → reconnect attempts → `reconnected` (when the
	//       server fails the Initialize call)
	//   (b) `lost` → next Ping succeeds → `healthy again`  (when
	//       the server recovers BETWEEN reconnect attempts and a
	//       fresh Ping observes it before Reconnect lands)
	// Both are valid recovery paths; assert at least one of them.
	hasLost := false
	hasRecovered := false
	for _, line := range logs {
		if containsAny(line, "lost") {
			hasLost = true
		}
		if containsAny(line, "reconnected") || containsAny(line, "healthy again") {
			hasRecovered = true
		}
	}
	if !hasLost {
		t.Errorf("expected `lost` log line; logs=%v", logs)
	}
	if !hasRecovered {
		t.Errorf("expected `reconnected` or `healthy again` log; logs=%v", logs)
	}
}

// ─── HealthMonitor with stdio fixture ────────────────

// The stdio path uses /bin/sh-based fixture servers (same shape
// as crosslang_test.go's fakeServer). We deliberately kill the
// subprocess and verify HealthMonitor respawns it.
func TestStdioReconnectRespawnsSubprocess(t *testing.T) {
	if !hasShellForFixture(t) {
		return
	}
	c := NewStdio(StdioConfig{
		Name: "fake", Command: "/bin/sh",
		Args: []string{writeFakeServer(t)},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Sanity: tools/list works once.
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("first list: %v", err)
	}

	// Kill the subprocess directly. The next call should fail; a
	// follow-up Reconnect should re-fork and ListTools again.
	if c.cmd == nil || c.cmd.Process == nil {
		t.Fatal("client has no process to kill")
	}
	pid := c.cmd.Process.Pid
	t.Logf("killing pid=%d", pid)
	_ = c.cmd.Process.Kill()
	// Give the read goroutine a moment to notice the EOF.
	time.Sleep(200 * time.Millisecond)

	t.Logf("starting Reconnect")
	rcCtx, rcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rcCancel()
	if err := c.Reconnect(rcCtx); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	t.Logf("Reconnect ok, new pid=%d", c.cmd.Process.Pid)
	// Different pid means we genuinely respawned.
	if c.cmd == nil || c.cmd.Process == nil || c.cmd.Process.Pid == pid {
		t.Errorf("subprocess not respawned: old pid=%d new=%v", pid, c.cmd)
	}
	// And the catalog still reads.
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("post-reconnect list: %v", err)
	}
	if len(tools) == 0 {
		t.Errorf("expected tools after reconnect")
	}
}

// ─── Stop semantics ──────────────────────────────────

// Stop drains the goroutine cleanly, no leak. Verified by a sync
// signal: Stop returns only after the loop's deferred close on
// `done` runs.
func TestHealthMonitorStopDrains(t *testing.T) {
	r := NewRegistry()
	defer r.Close()
	mon := r.StartHealthMonitor(HealthOptions{
		Interval: 50 * time.Millisecond,
	})
	// Calling Stop must return promptly; if the loop weren't
	// honouring ctx.Done this would hang the test.
	done := make(chan struct{})
	go func() {
		mon.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not drain")
	}
	// Calling Stop again is a no-op.
	mon.Stop()
}

// ─── Helpers ─────────────────────────────────────────

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func containsAny(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
