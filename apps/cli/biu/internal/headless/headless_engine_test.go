package headless

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// fakeAnthropic is a tiny Anthropic-Messages-API stub: it replies
// with one text-only turn so the SDK agent can drive end-to-end
// without a real key.
const fakeAnthropicSSE = `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"e2e ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`

func newFakeAnthropic(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(fakeAnthropicSSE))
	}))
}

// TestHeadlessEngineEmitsAGUIEvents verifies the full pipe from a
// headless run → SDK agent → fake Anthropic → AG-UI JSONL output.
func TestHeadlessEngineEmitsAGUIEvents(t *testing.T) {
	srv := newFakeAnthropic(t)
	defer srv.Close()

	agent, err := biumindkit.New(biumindkit.Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   srv.URL,
		LoadProjectMemory:   biumindkit.NoMemory,
		LoadProjectSettings: biumindkit.NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	var buf bytes.Buffer
	err = Run(context.Background(), &buf, Options{
		Prompt: "say hi", JSON: true, Agent: agent,
	})
	if err != nil {
		t.Fatalf("headless run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`"type":"RUN_STARTED"`,
		`"type":"TEXT_MESSAGE_CONTENT"`,
		`"type":"RUN_FINISHED"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n— got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "e2e ok") {
		t.Errorf("assistant body missing: %s", out)
	}
}

// TestHeadlessRequiresProvider — when neither Agent nor Provider is
// supplied, Run should error cleanly rather than panic.
func TestHeadlessRequiresProvider(t *testing.T) {
	var buf bytes.Buffer
	err := Run(context.Background(), &buf, Options{
		Prompt: "x", Model: "test",
	})
	if err == nil {
		t.Errorf("expected error when neither Provider nor Agent supplied")
	}
}
