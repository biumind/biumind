package chat

// agent_loop_runner_test.go — RunAgentLoop / RunAgentLoopBuffered share
// runAgentLoop; these tests pin the buffered variant's contract (the MCP
// wiki.chat path consumes it) plus the SSE variant's pass-through of the
// shared core's result.

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// newHubServer stands up a scripted fake model-relay (see hubScript in
// agent_test.go) and returns it with cleanup registered.
func newHubServer(t *testing.T, script *hubScript) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(script.handler())
	t.Cleanup(srv.Close)
	return srv
}

// Buffered variant: same loop, no live SSE client — the answer text is
// read back from the returned emitter, token usage from the result.
func TestRunAgentLoopBuffered_ReturnsTextAndUsage(t *testing.T) {
	scene := sseScene(
		[2]string{"delta", `{"text":"Answer: "}`},
		[2]string{"delta", `{"text":"42."}`},
		[2]string{"stop", `{"reason":"end_turn","usage":{"prompt_tokens":7,"completion_tokens":4}}`},
		[2]string{"end", `{}`},
	)
	script := &hubScript{scenes: []string{scene}}
	srv := newHubServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	res, be, err := sender.RunAgentLoopBuffered(context.Background(), "tok",
		AgentLoopRunInput{
			System:   "sys",
			UserText: "question",
			Model:    "test-model",
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("expected 1 model-relay call, got %d", got)
	}
	if res == nil || res.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %+v", res)
	}
	if res.PromptTokens != 7 || res.CompletionTokens != 4 {
		t.Errorf("token usage: got %+v", res)
	}
	if got := be.AccumulatedText(); got != "Answer: 42." {
		t.Errorf("accumulated text: got %q", got)
	}
}

// Empty bearer falls back to StaticBearer (stdio / dev deployments with
// PassThroughAuth=false) — the fallback must reach the relay call.
func TestRunAgentLoopBuffered_EmptyBearerFallsBackToStatic(t *testing.T) {
	scene := sseScene(
		[2]string{"delta", `{"text":"ok"}`},
		[2]string{"stop", `{"reason":"end_turn"}`},
		[2]string{"end", `{}`},
	)
	script := &hubScript{scenes: []string{scene}}
	srv := newHubServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	sender.StaticBearer = "static-tok"
	if _, _, err := sender.RunAgentLoopBuffered(context.Background(), "",
		AgentLoopRunInput{UserText: "q", Model: "m"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("expected 1 model-relay call, got %d", got)
	}
}

// Loop failure surfaces as error; the emitter still carries the
// block.error frame (callers must not double-report).
func TestRunAgentLoopBuffered_RelayErrorPropagates(t *testing.T) {
	script := &hubScript{scenes: nil} // first call 500s
	srv := newHubServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	_, _, err := sender.RunAgentLoopBuffered(context.Background(), "tok",
		AgentLoopRunInput{UserText: "q", Model: "m"})
	if err == nil {
		t.Fatal("expected error when relay fails")
	}
}

// Allowlist + budgets pass through to the loop — a tool-turn scene with
// a default-deny allowlist must not invoke unlisted tools. Here the
// model asks for an unlisted tool; the loop rejects it in-process and
// the relay sees a follow-up turn with the error folded back.
func TestRunAgentLoopBuffered_AllowlistGatesTools(t *testing.T) {
	toolTurn := sseScene(
		[2]string{"tool_call_start", `{"id":"t1","name":"danger_exec"}`},
		[2]string{"tool_call_end", `{"id":"t1"}`},
		[2]string{"stop", `{"reason":"tool_use"}`},
		[2]string{"end", `{}`},
	)
	finalTurn := sseScene(
		[2]string{"delta", `{"text":"cannot run that"}`},
		[2]string{"stop", `{"reason":"end_turn"}`},
		[2]string{"end", `{}`},
	)
	script := &hubScript{scenes: []string{toolTurn, finalTurn}}
	srv := newHubServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	sender.Tools = tools.New() // empty registry; allowlist rejects first
	res, be, err := sender.RunAgentLoopBuffered(context.Background(), "tok",
		AgentLoopRunInput{
			UserText:  "q",
			Model:     "m",
			Allowlist: map[string]struct{}{"wiki_search": {}},
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q", res.StopReason)
	}
	if got := be.AccumulatedText(); got != "cannot run that" {
		t.Errorf("accumulated text: got %q", got)
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("expected 2 relay calls (tool round-trip), got %d", got)
	}
}
