// Tests for the permission-ask UI surface (renderer + key handler).

package repl

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func TestRenderPermissionAsk_NoSuggestions(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": "rm /tmp/x"},
		Reason:   "destructive bash",
	}
	got := renderPermissionAsk(ask)
	if !strings.Contains(got, "Permission required") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "[a]llow once") {
		t.Errorf("missing standard hotkey hint: %q", got)
	}
}

func TestRenderPermissionAsk_WithSuggestions(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		ToolName: "Read",
		Input:    map[string]any{"file_path": "/tmp/scratch/x.go"},
		Reason:   "outside working dirs",
		Suggestions: []engine.AskSuggestion{
			{
				Label:  "Allow + add scratch/ to working dirs (this session)",
				HotKey: "w",
				Update: &sdkproto.AddDirectories{
					Type:        sdkproto.PermissionUpdateAddDirectories,
					Directories: []string{"/tmp/scratch"},
					Destination: sdkproto.PermissionDestSession,
				},
			},
		},
	}
	got := renderPermissionAsk(ask)
	if !strings.Contains(got, "[w] Allow + add scratch/ to working dirs") {
		t.Errorf("missing suggestion line; got:\n%s", got)
	}
	if !strings.Contains(got, "[a]llow once") {
		t.Errorf("standard hint should still be present alongside suggestion: %q", got)
	}
}

func TestRenderPermissionAsk_SuggestionMissingFieldsDropped(t *testing.T) {
	// Defensive: a suggestion missing HotKey or Label is silently
	// skipped so a malformed runner-side suggestion doesn't render
	// a broken row.
	ask := &engine.PermissionAskEvent{
		ToolName: "Read",
		Suggestions: []engine.AskSuggestion{
			{Label: "no hotkey"},
			{HotKey: "w"},
		},
	}
	got := renderPermissionAsk(ask)
	if strings.Contains(got, "no hotkey") {
		t.Errorf("missing-HotKey suggestion should not render: %q", got)
	}
}

func TestPermissionAskKey_SuggestionHotKey(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{
		ToolName: "Read",
		Input:    map[string]any{"file_path": "/tmp/scratch/x.go"},
		Decision: respCh,
		Suggestions: []engine.AskSuggestion{
			{
				Label: "add", HotKey: "w",
				Update: &sdkproto.AddDirectories{
					Type:        sdkproto.PermissionUpdateAddDirectories,
					Directories: []string{"/tmp/scratch"},
					Destination: sdkproto.PermissionDestSession,
				},
			},
		},
	}

	m := newTestModel(t, "test-model")
	m.permissionAsk = ask
	m = applyKey(t, m, "w")

	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermAllow {
			t.Errorf("'w' must Allow; got %v", ans.Decision)
		}
		if len(ans.AppliedUpdates) != 1 {
			t.Errorf("'w' must attach 1 applied update; got %+v", ans.AppliedUpdates)
		}
	default:
		t.Fatal("decision channel didn't receive an answer")
	}
	if m.permissionAsk != nil {
		t.Errorf("ask should be cleared after answer")
	}
}

func TestPermissionAskKey_StandardAllowStillWorks(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{
		ToolName: "Read",
		Decision: respCh,
		Suggestions: []engine.AskSuggestion{
			{HotKey: "w", Label: "add", Update: &sdkproto.AddDirectories{}},
		},
	}
	m := newTestModel(t, "test-model")
	m.permissionAsk = ask
	m = applyKey(t, m, "a")
	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermAllow {
			t.Errorf("'a' must Allow; got %v", ans.Decision)
		}
		if len(ans.AppliedUpdates) != 0 {
			t.Errorf("'a' must NOT carry updates; got %+v", ans.AppliedUpdates)
		}
	default:
		t.Fatal("decision channel didn't receive an answer")
	}
}

// applyKey drives one keypress through handleKey. We build a tea.KeyMsg
// for a single rune (sufficient for hotkeys "a"/"w"/"d") and dispatch.
func applyKey(t *testing.T, m model, key string) model {
	t.Helper()
	r := []rune(key)
	if len(r) != 1 {
		t.Fatalf("applyKey: only single-rune keys supported; got %q", key)
	}
	msg := tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: r})
	out, _ := m.handleKey(msg)
	return out.(model)
}
