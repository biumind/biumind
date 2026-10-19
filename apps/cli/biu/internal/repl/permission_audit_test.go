// Tests for the REPL rendering of PermissionAuditEvent (engine_loop.go):
// the audit line attaches to the matching tool row and renders dim
// under it; a stray audit with no live row degrades to a system note.

package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestPermissionAudit_AttachesToToolRow(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.ToolUseStartEvent{
		ID: "tu_1", Name: "Bash",
		Input: map[string]any{"command": "rm -rf /tmp/x"},
	})
	m, _ = m.handleEngineEvent(&engine.PermissionAuditEvent{
		ToolUseID: "tu_1", ToolName: "Bash",
		Outcome: "auto-approved", Policy: "auto-mode-approve", Reason: "auto mode",
	})

	if len(m.toolRows) != 1 {
		t.Fatalf("toolRows = %+v", m.toolRows)
	}
	want := "auto: Bash approved without prompting"
	if m.toolRows[0].Audit != want {
		t.Errorf("Audit = %q, want %q", m.toolRows[0].Audit, want)
	}
	rendered := renderToolRows(m.toolRows)
	if !strings.Contains(rendered, "⏵⏵ "+want) {
		t.Errorf("audit line missing from rendered rows:\n%s", rendered)
	}
}

func TestPermissionAudit_GuardAskLineUsesReason(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.ToolUseStartEvent{
		ID: "tu_1", Name: "Bash",
		Input: map[string]any{"command": "sudo apt update"},
	})
	m, _ = m.handleEngineEvent(&engine.PermissionAuditEvent{
		ToolUseID: "tu_1", ToolName: "Bash",
		Outcome: "guard-ask", Policy: "dangerous-command-ask",
		Reason: "dangerous command guard: runs as root (sudo)",
	})

	want := "dangerous command guard: runs as root (sudo)"
	if m.toolRows[0].Audit != want {
		t.Errorf("Audit = %q, want %q", m.toolRows[0].Audit, want)
	}
}

func TestPermissionAudit_NoMatchingRowDegradesToSystemNote(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.PermissionAuditEvent{
		ToolUseID: "tu_missing", ToolName: "Bash",
		Outcome: "auto-approved", Policy: "auto-mode-approve", Reason: "auto mode",
	})

	if len(m.history) != 1 || m.history[0].Role != "system" {
		t.Fatalf("stray audit should land as a system note; history = %+v", m.history)
	}
	if !strings.Contains(m.history[0].Content, "auto: Bash approved without prompting") {
		t.Errorf("system note content wrong: %q", m.history[0].Content)
	}
}

// The audit survives folding: once the batch flushes into the
// transcript the line stays attached to the row.
func TestPermissionAudit_SurvivesTranscriptFold(t *testing.T) {
	m := model{}
	m, _ = m.handleEngineEvent(&engine.ToolUseStartEvent{
		ID: "tu_1", Name: "Bash",
		Input: map[string]any{"command": "make build"},
	})
	m, _ = m.handleEngineEvent(&engine.PermissionAuditEvent{
		ToolUseID: "tu_1", ToolName: "Bash",
		Outcome: "auto-approved", Policy: "auto-mode-approve", Reason: "auto mode",
	})
	m, _ = m.handleEngineEvent(&engine.AssistantMessageEvent{StopReason: "end_turn"})

	body := m.transcriptString()
	if !strings.Contains(body, "⏵⏵ auto: Bash approved without prompting") {
		t.Errorf("audit line lost after transcript fold:\n%s", body)
	}
}
