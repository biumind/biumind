package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// hubScript scripts a sequence of SSE responses. Each call to the
// mock model-relay returns the next scene's body. The test asserts on the
// number of calls, request bodies, and event ordering observed by
// the BlockEmitter.
type hubScript struct {
	scenes []string
	calls  atomic.Int32
	bodies []string // captured request bodies, indexed by call
}

func (s *hubScript) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(s.calls.Add(1)) - 1
		if idx >= len(s.scenes) {
			http.Error(w, "scenes exhausted", http.StatusInternalServerError)
			return
		}
		// Capture body for assertions.
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		s.bodies = append(s.bodies, string(buf))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.scenes[idx]))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
}

// sseScene formats a sequence of (event, data) pairs into the wire
// format model-relay speaks.
func sseScene(pairs ...[2]string) string {
	var sb strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", p[0], p[1])
	}
	return sb.String()
}

// newTestEmitter makes a BlockEmitter writing into a discardable
// recorder. The agent loop's emitter usage is exercised; the SSE
// output is not asserted here (tested separately in
// blockemitter_test.go).
func newTestEmitter() *BlockEmitter {
	rec := httptest.NewRecorder()
	rwf := &recorderWithFlush{ResponseRecorder: rec}
	return NewBlockEmitter(rwf, rwf, uuid.New())
}

func newAgentTestRig(t *testing.T, scenes ...string) (
	*AgentLoop, *hubScript, *tools.Registry,
) {
	t.Helper()
	script := &hubScript{scenes: scenes}
	srv := httptest.NewServer(script.handler())
	t.Cleanup(srv.Close)

	sender := NewHTTPSender(nil, srv.URL)
	reg := tools.New()
	loop := NewAgentLoop(sender, reg)
	return loop, script, reg
}

// 1) Single text turn. Stream emits text deltas + stop=end_turn.
// Loop exits after one turn; text fully accumulated.
func TestAgentLoopSingleTurnText(t *testing.T) {
	scene := sseScene(
		[2]string{"delta", `{"text":"Hello "}`},
		[2]string{"delta", `{"text":"world."}`},
		[2]string{"stop", `{"reason":"end_turn","usage":{"prompt_tokens":10,"completion_tokens":3}}`},
		[2]string{"end", `{}`},
	)
	loop, script, _ := newAgentTestRig(t, scene)
	be := newTestEmitter()

	res, err := loop.Run(context.Background(), AgentRunInput{
		Model:   "test-model",
		Mode:    tools.ExecutionCloud,
		History: []hubMessage{{Role: "user", Content: "hi"}},
		Emitter: be,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("expected 1 model-relay call, got %d", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q want end_turn", res.StopReason)
	}
	if res.PromptTokens != 10 || res.CompletionTokens != 3 {
		t.Errorf("token usage: got %+v", res)
	}
	be.CloseActiveText()
	if got := be.AccumulatedText(); got != "Hello world." {
		t.Errorf("accumulated text: got %q", got)
	}
}

// 2) Tool round-trip. First scene = tool_use; second = final text.
func TestAgentLoopToolRoundTrip(t *testing.T) {
	scene1 := sseScene(
		[2]string{"delta", `{"text":"Let me check…"}`},
		[2]string{"tool_call_start", `{"id":"t1","name":"echo"}`},
		[2]string{"tool_call_args", `{"id":"t1","delta":"{\"msg\":\"ping\"}"}`},
		[2]string{"tool_call_end", `{"id":"t1"}`},
		[2]string{"stop", `{"reason":"tool_use"}`},
		[2]string{"end", `{}`},
	)
	scene2 := sseScene(
		[2]string{"delta", `{"text":"Done."}`},
		[2]string{"stop", `{"reason":"end_turn"}`},
		[2]string{"end", `{}`},
	)
	loop, script, reg := newAgentTestRig(t, scene1, scene2)
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, in json.RawMessage) (any, error) {
			return map[string]any{"echoed": string(in)}, nil
		},
	})
	be := newTestEmitter()

	res, err := loop.Run(context.Background(), AgentRunInput{
		Model:   "test-model",
		Mode:    tools.ExecutionCloud,
		History: []hubMessage{{Role: "user", Content: "do it"}},
		Emitter: be,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("expected 2 model-relay calls, got %d", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("final stop_reason: got %q", res.StopReason)
	}

	// The second model-relay request must include the tool_result message.
	body2 := script.bodies[1]
	if !strings.Contains(body2, `"tool_call_id":"t1"`) {
		t.Errorf("second request missing tool_call_id: %s", body2)
	}
	if !strings.Contains(body2, "echoed") {
		t.Errorf("second request missing tool result: %s", body2)
	}

	// Emitted parts: text + tool_use(complete) + text.
	be.CloseActiveText()
	var parts []map[string]any
	if err := json.Unmarshal(be.PartsJSON(), &parts); err != nil {
		t.Fatalf("parts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0]["type"] != "text" || parts[1]["type"] != "tool_use" ||
		parts[2]["type"] != "text" {
		t.Errorf("part shape: %+v", parts)
	}
	if parts[1]["phase"] != "success" {
		t.Errorf("tool phase: got %v", parts[1]["phase"])
	}
}

// 3) Tool error feeds back to the model. Loop keeps going.
func TestAgentLoopToolErrorIsFedBack(t *testing.T) {
	scene1 := sseScene(
		[2]string{"tool_call_start", `{"id":"t1","name":"flaky"}`},
		[2]string{"tool_call_args", `{"id":"t1","delta":"{}"}`},
		[2]string{"tool_call_end", `{"id":"t1"}`},
		[2]string{"stop", `{"reason":"tool_use"}`},
		[2]string{"end", `{}`},
	)
	scene2 := sseScene(
		[2]string{"delta", `{"text":"sorry"}`},
		[2]string{"stop", `{"reason":"end_turn"}`},
		[2]string{"end", `{}`},
	)
	loop, script, reg := newAgentTestRig(t, scene1, scene2)
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "flaky", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, fmt.Errorf("nope")
		},
	})
	be := newTestEmitter()

	_, err := loop.Run(context.Background(), AgentRunInput{
		Model:   "m",
		Mode:    tools.ExecutionCloud,
		History: []hubMessage{{Role: "user", Content: "go"}},
		Emitter: be,
	})
	if err != nil {
		t.Fatalf("run unexpectedly errored: %v", err)
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("expected 2 model-relay calls, got %d", got)
	}
	body2 := script.bodies[1]
	if !strings.Contains(body2, "error: nope") {
		t.Errorf("second request missing tool error in result: %s", body2)
	}
}

// 4) Max-turns guard: model keeps requesting tools forever; loop
// bails after MaxTurns and reports it.
func TestAgentLoopMaxTurnsGuard(t *testing.T) {
	toolScene := sseScene(
		[2]string{"tool_call_start", `{"id":"loop","name":"echo"}`},
		[2]string{"tool_call_args", `{"id":"loop","delta":"{}"}`},
		[2]string{"tool_call_end", `{"id":"loop"}`},
		[2]string{"stop", `{"reason":"tool_use"}`},
		[2]string{"end", `{}`},
	)
	// Provide enough scenes to cover MaxTurns.
	scenes := make([]string, 4)
	for i := range scenes {
		scenes[i] = toolScene
	}
	loop, script, reg := newAgentTestRig(t, scenes...)
	loop.MaxTurns = 3
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	be := newTestEmitter()

	res, err := loop.Run(context.Background(), AgentRunInput{
		Model:   "m",
		Mode:    tools.ExecutionCloud,
		History: []hubMessage{{Role: "user", Content: "go"}},
		Emitter: be,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 3 {
		t.Errorf("expected 3 model-relay calls (capped), got %d", got)
	}
	if res.StopReason != "max_turns" {
		t.Errorf("stop_reason: got %q want max_turns", res.StopReason)
	}
}
