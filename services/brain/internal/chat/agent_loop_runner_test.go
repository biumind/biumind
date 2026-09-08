package chat

// agent_loop_runner_test.go — RunAgentLoop / RunAgentLoopBuffered share
// runAgentLoop; these tests pin the buffered variant's contract (the MCP
// wiki.chat path consumes it). The live-SSE variant's event-sequence
// regression lives in agent_test.go.
//
// fake relay 喂 Anthropic Messages SSE（anthropicScript，见
// agent_v2_test.go）—— 内核收敛后 runAgentLoop 走 RunV2/biumindkit，
// 生产上由 model-relay 的 verbatim anthropic 流供帧。

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// anthropicTextWithUsage 一个带 usage 的 single-turn text scene（end_turn）。
// Anthropic 协议：input_tokens 在 message_start，output_tokens 在
// message_delta。
func anthropicTextWithUsage(text string, inTok, outTok int) string {
	return `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test","usage":{"input_tokens":` + itoa(inTok) + `,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":` + itoa(outTok) + `}}

event: message_stop
data: {"type":"message_stop"}

`
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// newRelayServer stands up a scripted fake model-relay (see
// anthropicScript in agent_v2_test.go) and returns it with cleanup
// registered.
func newRelayServer(t *testing.T, script *anthropicScript) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(script.handler())
	t.Cleanup(srv.Close)
	return srv
}

// Buffered variant: same kernel, no live SSE client — the answer text is
// read back from the returned emitter, token usage from the result.
func TestRunAgentLoopBuffered_ReturnsTextAndUsage(t *testing.T) {
	script := &anthropicScript{
		scenes: []string{anthropicTextWithUsage("Answer: 42.", 7, 4)},
	}
	srv := newRelayServer(t, script)

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
	// PassThrough 契约：caller bearer 原样做 Authorization。
	if script.auths[0] != "Bearer tok" {
		t.Errorf("upstream Authorization: got %q", script.auths[0])
	}
}

// Empty bearer falls back to StaticBearer (stdio / dev deployments with
// PassThroughAuth=false) — the fallback must reach the relay call.
func TestRunAgentLoopBuffered_EmptyBearerFallsBackToStatic(t *testing.T) {
	script := &anthropicScript{scenes: []string{anthropicTextOnly("ok")}}
	srv := newRelayServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	sender.StaticBearer = "static-tok"
	if _, _, err := sender.RunAgentLoopBuffered(context.Background(), "",
		AgentLoopRunInput{UserText: "q", Model: "m"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("expected 1 model-relay call, got %d", got)
	}
	if script.auths[0] != "Bearer static-tok" {
		t.Errorf("upstream Authorization: got %q want Bearer static-tok",
			script.auths[0])
	}
}

// Kernel failure surfaces as error; the emitter still carries the
// block.error frame (callers must not double-report).
func TestRunAgentLoopBuffered_RelayErrorPropagates(t *testing.T) {
	script := &anthropicScript{scenes: nil} // first call 500s
	srv := newRelayServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	_, _, err := sender.RunAgentLoopBuffered(context.Background(), "tok",
		AgentLoopRunInput{UserText: "q", Model: "m"})
	if err == nil {
		t.Fatal("expected error when relay fails")
	}
}

// Allowlist + budgets pass through to the kernel — a tool-turn scene with
// a default-deny allowlist must not invoke unlisted tools. Here the model
// asks for a tool that was never advertised; biumindkit folds the
// unknown-tool rejection into a soft tool_result and the relay sees a
// follow-up turn.
func TestRunAgentLoopBuffered_AllowlistGatesTools(t *testing.T) {
	script := &anthropicScript{scenes: []string{
		anthropicToolUseScene("toolu_1", "danger_exec", `{}`),
		anthropicTextOnly("cannot run that"),
	}}
	srv := newRelayServer(t, script)

	sender := NewHTTPSender(nil, srv.URL)
	sender.Tools = tools.New() // empty registry; nothing is advertised
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
