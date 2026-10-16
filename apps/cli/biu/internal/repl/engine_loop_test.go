package repl

import (
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
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

// Tool rows must fold into the transcript ahead of the assistant text
// they produced, and the live area must be empty once the turn ends —
// previously DoneEvent left them pinned above the input forever.
func TestHandleEngineEvent_ToolRowsFoldIntoTranscript(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.ToolUseStartEvent{
		ID: "t1", Name: "Read",
		Input: map[string]any{"path": "foo.go"},
	})
	m, _ = m.handleEngineEvent(&engine.ToolUseResultEvent{
		ID: "t1", Name: "Read", Elapsed: 3 * time.Millisecond,
	})
	if len(m.toolRows) != 1 {
		t.Fatalf("toolRows = %+v", m.toolRows)
	}
	m, _ = m.handleEngineEvent(&engine.StreamTokenEvent{Text: "the answer"})
	m, _ = m.handleEngineEvent(&engine.AssistantMessageEvent{StopReason: "end_turn"})
	m, _ = m.handleEngineEvent(&engine.DoneEvent{StopReason: "end_turn"})

	if len(m.toolRows) != 0 {
		t.Fatalf("live toolRows not flushed: %+v", m.toolRows)
	}
	if len(m.toolClusters) != 1 {
		t.Fatalf("toolClusters = %+v", m.toolClusters)
	}
	if m.toolClusters[0].beforeIdx != 0 {
		t.Fatalf("beforeIdx = %d, want 0 (ahead of the assistant message)",
			m.toolClusters[0].beforeIdx)
	}
	body := m.transcriptString()
	toolAt := strings.Index(body, "Read foo.go")
	textAt := strings.Index(body, "the answer")
	if toolAt == -1 || textAt == -1 || toolAt > textAt {
		t.Fatalf("transcript order wrong (tool@%d text@%d):\n%s",
			toolAt, textAt, body)
	}
}

// A turn canceled mid-tool must not leave a perpetual "…" row in the
// scrollback: flush stamps leftover running rows as interrupted.
func TestFlushToolRows_MarksRunningInterrupted(t *testing.T) {
	m := model{
		toolRows: []toolRow{{
			ID: "t1", Name: "Bash", Phase: "running",
			Input: map[string]any{"command": "sleep 99"},
		}},
	}
	m.flushToolRows()
	if len(m.toolRows) != 0 {
		t.Fatalf("toolRows not cleared: %+v", m.toolRows)
	}
	if len(m.toolClusters) != 1 {
		t.Fatalf("toolClusters = %+v", m.toolClusters)
	}
	row := m.toolClusters[0].rows[0]
	if row.Phase != "error" || row.Message != "interrupted" {
		t.Fatalf("row = %+v, want interrupted error", row)
	}
	if got := renderToolRows(m.toolClusters[0].rows); !strings.Contains(got, "interrupted") {
		t.Fatalf("rendered cluster missing interrupted marker:\n%s", got)
	}
}

// /clear must wipe tool clusters and live rows too, not just history —
// otherwise stale tool rows survive on the cleared screen.
func TestSlashClear_WipesToolClusters(t *testing.T) {
	m := model{
		history: []client.Message{{Role: "assistant", Content: "old"}},
		toolClusters: []toolCluster{{
			beforeIdx: 0,
			rows:      []toolRow{{ID: "t1", Name: "Read", Phase: "done"}},
		}},
		toolRows: []toolRow{{ID: "t2", Name: "Bash", Phase: "running"}},
	}
	um, _ := m.runSlash("/clear")
	m = um.(model)
	if len(m.history) != 0 || len(m.toolClusters) != 0 || len(m.toolRows) != 0 {
		t.Fatalf("after /clear: history=%d clusters=%d rows=%d",
			len(m.history), len(m.toolClusters), len(m.toolRows))
	}
}
