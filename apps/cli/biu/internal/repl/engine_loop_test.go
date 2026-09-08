package repl

import (
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// Regression: pending was a value strings.Builder; bubbletea copies the
// model per Update, so the second streamed token panicked with
// "strings: illegal use of non-zero Builder copied by value".
func TestHandleEngineEvent_MultiTokenStream(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.StreamTokenEvent{Text: "hello"})
	m, _ = m.handleEngineEvent(&engine.StreamTokenEvent{Text: " world"})
	if got := m.pendingString(); got != "hello world" {
		t.Fatalf("pending = %q, want %q", got, "hello world")
	}
	m, _ = m.handleEngineEvent(&engine.AssistantMessageEvent{StopReason: "end_turn"})
	if len(m.history) != 1 || m.history[0].Content != "hello world" {
		t.Fatalf("history = %+v", m.history)
	}
	if got := m.pendingString(); got != "" {
		t.Fatalf("pending not reset: %q", got)
	}
}

// Legacy streaming path (deltaMsg) had the same value-copy panic on the
// second chunk.
func TestUpdate_MultiDeltaStream(t *testing.T) {
	m := model{}
	um, _ := m.Update(deltaMsg{text: "foo"})
	m = um.(model)
	um, _ = m.Update(deltaMsg{text: "bar"})
	m = um.(model)
	if got := m.pendingString(); got != "foobar" {
		t.Fatalf("pending = %q, want %q", got, "foobar")
	}
	um, _ = m.Update(streamDoneMsg{})
	m = um.(model)
	if len(m.history) != 1 || m.history[0].Content != "foobar" {
		t.Fatalf("history = %+v", m.history)
	}
}
