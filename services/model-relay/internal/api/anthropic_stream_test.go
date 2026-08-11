package api

import (
	"net/http/httptest"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// TestStreamAsAnthropic_TruncationReportedUnsuccessful verifies the
// glm-truncation accounting fix: when the upstream stream closes
// mid-tool-call-arguments (no FrameStop, no FrameError — a silent EOF),
// streamAsAnthropic must report success=false so billing/usage reflect
// the truth. It still emits a complete Anthropic SSE wire (masquerades
// end_turn + closes the partial block) so the engine can self-heal the
// resulting malformed tool_use.
func TestStreamAsAnthropic_TruncationReportedUnsuccessful(t *testing.T) {
	frames := make(chan provider.StreamFrame, 4)
	frames <- provider.StreamFrame{Type: provider.FrameToolCallStart,
		ToolCall: &provider.ToolCallDelta{ID: "c1", Name: "webReader"}}
	frames <- provider.StreamFrame{Type: provider.FrameToolCallArgs,
		ToolCall: &provider.ToolCallDelta{ID: "c1", ArgsDelta: `{"`}}
	close(frames) // truncation: channel closed without a terminal FrameStop

	rec := httptest.NewRecorder()
	_, ok, errCode := streamAsAnthropic(rec, rec, frames, "glm-5.2")
	if ok {
		t.Errorf("truncated stream must report success=false, got true")
	}
	if errCode != "truncated" {
		t.Errorf("expected errCode=%q, got %q", "truncated", errCode)
	}
}

// TestStreamAsAnthropic_CleanStreamSuccessful is the control: a stream
// that ends with a terminal FrameStop reports success=true.
func TestStreamAsAnthropic_CleanStreamSuccessful(t *testing.T) {
	frames := make(chan provider.StreamFrame, 4)
	frames <- provider.StreamFrame{Type: provider.FrameDelta, Delta: "hi"}
	frames <- provider.StreamFrame{Type: provider.FrameStop, Stop: "stop"}
	close(frames)

	rec := httptest.NewRecorder()
	_, ok, errCode := streamAsAnthropic(rec, rec, frames, "glm-5.2")
	if !ok {
		t.Errorf("clean stream must report success=true, got false (errCode=%q)", errCode)
	}
	if errCode != "" {
		t.Errorf("clean stream errCode should be empty, got %q", errCode)
	}
}

// TestStreamAsAnthropic_ToolUseStreamSuccessful verifies a clean
// tool_use stream (FrameToolCallEnd + FrameStop) is success=true — the
// normal happy path must not be flagged as truncated.
func TestStreamAsAnthropic_ToolUseStreamSuccessful(t *testing.T) {
	frames := make(chan provider.StreamFrame, 6)
	frames <- provider.StreamFrame{Type: provider.FrameToolCallStart,
		ToolCall: &provider.ToolCallDelta{ID: "c1", Name: "webReader"}}
	frames <- provider.StreamFrame{Type: provider.FrameToolCallArgs,
		ToolCall: &provider.ToolCallDelta{ID: "c1", ArgsDelta: `{"url":"x"}`}}
	frames <- provider.StreamFrame{Type: provider.FrameToolCallEnd,
		ToolCall: &provider.ToolCallDelta{ID: "c1"}}
	frames <- provider.StreamFrame{Type: provider.FrameStop, Stop: "tool_calls"}
	close(frames)

	rec := httptest.NewRecorder()
	_, ok, errCode := streamAsAnthropic(rec, rec, frames, "glm-5.2")
	if !ok {
		t.Errorf("clean tool_use stream must report success=true, got false (errCode=%q)", errCode)
	}
}
