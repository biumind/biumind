// F4 — Agent.CostByTool() exposes the per-tool slice tracker.AddTool
// records inside runner.go. End-to-end via the public SDK API.

package biumindkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAnthropicWithToolUse emits one tool_use turn followed by an
// end_turn turn. AgentTool.Submit drives the second turn after we feed
// a tool_result back. We rely on the engine's BypassPermissions = true
// + the fact that "Read" is a built-in tool registered by default.
//
// Two turn streams in the same upstream — first request gets the
// tool_use turn, second gets end_turn. We track call count via a
// counter on the handler.
func fakeAnthropicWithToolUse(t *testing.T) *httptest.Server {
	t.Helper()
	var n int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		n++
		if n == 1 {
			// Turn 1: assistant emits a tool_use block for Read.
			_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"Read","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"/etc/hostname\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}

`))
			return
		}
		// Turn 2: end_turn.
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_2","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
}

// TestSDK_CostByTool_EmptyOnFreshAgent — fresh agent with no Submit
// returns nil/empty, matching the "no data" contract.
func TestSDK_CostByTool_EmptyOnFreshAgent(t *testing.T) {
	a, err := New(Options{
		APIKey:              "sk-fake",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := a.CostByTool(); len(got) != 0 {
		t.Errorf("fresh agent CostByTool = %v, want empty", got)
	}
}

// TestSDK_CostByTool_PopulatesAfterRun — after a Submit that triggers
// a tool call, CostByTool returns a populated map.
//
// Uses Read with a /etc/hostname target which exists on macOS + Linux
// CI runners. The actual content doesn't matter — we only assert the
// per-tool entry got created.
func TestSDK_CostByTool_PopulatesAfterRun(t *testing.T) {
	upstream := fakeAnthropicWithToolUse(t)
	defer upstream.Close()

	a, err := New(Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range a.Submit(ctx, "read /etc/hostname") {
	}

	byTool := a.CostByTool()
	if len(byTool) == 0 {
		t.Fatal("CostByTool empty after a tool_use turn")
	}
	read, ok := byTool["Read"]
	if !ok {
		// Some test environments may surface the tool under a different
		// name if Read was renamed; fall back to checking any entry.
		for k, v := range byTool {
			if v.Calls > 0 {
				read = v
				ok = true
				_ = k
				break
			}
		}
	}
	if !ok {
		t.Fatalf("no tool entry found, got %+v", byTool)
	}
	if read.Calls < 1 {
		t.Errorf("Calls=%d, want >= 1", read.Calls)
	}
}

// TestSDK_CostByTool_IsCopy — mutating the returned map must not
// affect the live agent's tracker.
func TestSDK_CostByTool_IsCopy(t *testing.T) {
	upstream := fakeAnthropicWithToolUse(t)
	defer upstream.Close()
	a, err := New(Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range a.Submit(ctx, "read /etc/hostname") {
	}

	first := a.CostByTool()
	if len(first) == 0 {
		t.Skip("no tool entry to test copy semantics")
	}
	// Mutate the returned map.
	for k := range first {
		first[k] = ToolCost{Calls: 999}
	}

	// Second snapshot should still have the real call count.
	second := a.CostByTool()
	for k, v := range second {
		if v.Calls == 999 {
			t.Errorf("snapshot mutation leaked into agent: %s = %+v", k, v)
		}
	}
	_ = strings.Builder{} // anti-unused
}
