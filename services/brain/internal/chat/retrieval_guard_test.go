package chat

// P2 #19 retrieval-budget tests: budget rejection, duplicate-signature
// rejection, no-yield early stop, and non-retrieval passthrough. Each
// test scripts the model-relay SSE stream (hubScript) and asserts on
// the tool_result payloads fed into the NEXT request body plus the
// invoker call count.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// toolScene scripts one assistant turn that calls `name` with raw JSON
// args, then stops with tool_use.
func toolScene(id, name, args string) string {
	esc, _ := json.Marshal(args) // escape args for embedding in the delta JSON
	return sseScene(
		[2]string{"tool_call_start", `{"id":"` + id + `","name":"` + name + `"}`},
		[2]string{"tool_call_args", `{"id":"` + id + `","delta":` + string(esc) + `}`},
		[2]string{"tool_call_end", `{"id":"` + id + `"}`},
		[2]string{"stop", `{"reason":"tool_use"}`},
		[2]string{"end", `{}`},
	)
}

func finalTextScene(text string) string {
	return sseScene(
		[2]string{"delta", `{"text":"` + text + `"}`},
		[2]string{"stop", `{"reason":"end_turn"}`},
		[2]string{"end", `{}`},
	)
}

// registerRetrieval registers a retrieval-class stub returning `result`.
// `calls` counts actual invocations (rejections must NOT reach it).
func registerRetrieval(reg *tools.Registry, name string, result any, calls *atomic.Int32) {
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: name, Runtime: tools.RuntimeCloud},
		Retrieval:  true,
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			calls.Add(1)
			return result, nil
		},
	})
}

func runLoop(t *testing.T, loop *AgentLoop) {
	t.Helper()
	_, err := loop.Run(context.Background(), AgentRunInput{
		Model:   "m",
		Mode:    tools.ExecutionCloud,
		History: []hubMessage{{Role: "user", Content: "go"}},
		Emitter: newTestEmitter(),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

// 1) Budget exhaustion: 2nd retrieval call with DIFFERENT args is
// rejected once the budget (1) is spent; the invoker runs only once.
func TestAgentLoopRetrievalBudgetExceeded(t *testing.T) {
	loop, script, reg := newAgentTestRig(t,
		toolScene("t1", "wiki_search", `{"query":"alpha"}`),
		toolScene("t2", "wiki_search", `{"query":"beta"}`),
		finalTextScene("done"),
	)
	var calls atomic.Int32
	registerRetrieval(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{map[string]any{"title": "hit"}}},
		&calls)
	loop.RetrievalBudget = 1

	runLoop(t, loop)

	if got := calls.Load(); got != 1 {
		t.Errorf("invoker ran %d times, want 1 (budget=1)", got)
	}
	if got := script.calls.Load(); got != 3 {
		t.Fatalf("expected 3 model-relay calls, got %d", got)
	}
	body := script.bodies[2]
	if !strings.Contains(body, "retrieval budget exhausted") {
		t.Errorf("3rd request missing budget rejection tool_result: %s", body)
	}
}

// 2) Duplicate signature: same tool + semantically identical args
// (case/whitespace differences normalize away) is rejected without
// touching the invoker — even with budget to spare.
func TestAgentLoopDuplicateRetrievalRejected(t *testing.T) {
	loop, script, reg := newAgentTestRig(t,
		toolScene("t1", "websearch", `{"query":" Foo "}`),
		toolScene("t2", "websearch", `{"query":"foo"}`),
		finalTextScene("done"),
	)
	var calls atomic.Int32
	registerRetrieval(reg, "websearch",
		map[string]any{"query": "foo", "results": []any{map[string]any{"title": "hit"}}},
		&calls)
	loop.RetrievalBudget = 10

	runLoop(t, loop)

	if got := calls.Load(); got != 1 {
		t.Errorf("invoker ran %d times, want 1 (2nd call is a duplicate)", got)
	}
	body := script.bodies[2]
	if !strings.Contains(body, "duplicate call") {
		t.Errorf("3rd request missing duplicate rejection tool_result: %s", body)
	}
}

// 3) No-yield early stop: two consecutive empty results trip the streak
// limit (2); the 3rd retrieval call is rejected with a wrap-up hint.
func TestAgentLoopNoYieldEarlyStop(t *testing.T) {
	loop, script, reg := newAgentTestRig(t,
		toolScene("t1", "wiki_search", `{"query":"q1"}`),
		toolScene("t2", "wiki_search", `{"query":"q2"}`),
		toolScene("t3", "wiki_search", `{"query":"q3"}`),
		finalTextScene("done"),
	)
	var calls atomic.Int32
	registerRetrieval(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{}}, // always empty
		&calls)
	loop.RetrievalBudget = 10
	loop.NoYieldStreakLimit = 2

	runLoop(t, loop)

	if got := calls.Load(); got != 2 {
		t.Errorf("invoker ran %d times, want 2 (streak limit hit)", got)
	}
	body := script.bodies[3]
	if !strings.Contains(body, "no new information") {
		t.Errorf("4th request missing no-yield rejection tool_result: %s", body)
	}
}

// 4) Non-retrieval tools are unaffected by the retrieval budget: after
// the budget is spent, a normal tool still executes; only retrieval
// calls are rejected.
func TestAgentLoopBudgetSparesNonRetrievalTools(t *testing.T) {
	loop, script, reg := newAgentTestRig(t,
		toolScene("t1", "wiki_search", `{"query":"alpha"}`),
		toolScene("t2", "echo", `{"msg":"ping"}`),
		toolScene("t3", "wiki_search", `{"query":"beta"}`),
		finalTextScene("done"),
	)
	var searchCalls, echoCalls atomic.Int32
	registerRetrieval(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{map[string]any{"title": "hit"}}},
		&searchCalls)
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, in json.RawMessage) (any, error) {
			echoCalls.Add(1)
			return map[string]any{"echoed": string(in)}, nil
		},
	})
	loop.RetrievalBudget = 1

	runLoop(t, loop)

	if got := searchCalls.Load(); got != 1 {
		t.Errorf("search invoker ran %d times, want 1", got)
	}
	if got := echoCalls.Load(); got != 1 {
		t.Errorf("echo invoker ran %d times, want 1 (non-retrieval must pass)", got)
	}
	body := script.bodies[3]
	if !strings.Contains(body, "retrieval budget exhausted") {
		t.Errorf("4th request missing budget rejection tool_result: %s", body)
	}
}

// 5) Zero budget (default) leaves the loop untouched: repeated
// retrieval calls all execute — guards nothing.
func TestAgentLoopZeroBudgetDisablesGuard(t *testing.T) {
	loop, script, reg := newAgentTestRig(t,
		toolScene("t1", "wiki_search", `{"query":"same"}`),
		toolScene("t2", "wiki_search", `{"query":"same"}`),
		finalTextScene("done"),
	)
	var calls atomic.Int32
	registerRetrieval(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{}},
		&calls)
	// RetrievalBudget stays 0.

	runLoop(t, loop)

	if got := calls.Load(); got != 2 {
		t.Errorf("invoker ran %d times, want 2 (guard disabled)", got)
	}
	if got := script.calls.Load(); got != 3 {
		t.Fatalf("expected 3 model-relay calls, got %d", got)
	}
}
