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
	got := renderPermissionAsk(ask, 0)
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
	got := renderPermissionAsk(ask, 0)
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
	got := renderPermissionAsk(ask, 0)
	if strings.Contains(got, "no hotkey") {
		t.Errorf("missing-HotKey suggestion should not render: %q", got)
	}
}

func TestRenderPermissionAsk_SubAgentOriginAndQueue(t *testing.T) {
	ask := &engine.PermissionAskEvent{ToolName: "Bash"}
	ask.SetBaseForTest("agent-2", "tu_agent_1")
	got := renderPermissionAsk(ask, 2)
	if !strings.Contains(got, "(sub-agent)") {
		t.Errorf("forwarded sub-agent ask must be labelled: %q", got)
	}
	if !strings.Contains(got, "+2 queued") {
		t.Errorf("queued count must render: %q", got)
	}
	// Root-engine asks carry no parent tool_use id → no label.
	root := &engine.PermissionAskEvent{ToolName: "Bash"}
	if got := renderPermissionAsk(root, 0); strings.Contains(got, "sub-agent") {
		t.Errorf("root ask must not be labelled as sub-agent: %q", got)
	}
}

func TestPermissionAskQueue_FIFO(t *testing.T) {
	ch1 := make(chan engine.PermissionAnswer, 1)
	ch2 := make(chan engine.PermissionAnswer, 1)
	ask1 := &engine.PermissionAskEvent{ToolName: "Bash", Decision: ch1}
	ask2 := &engine.PermissionAskEvent{ToolName: "Read", Decision: ch2}

	m := newTestModel(t, "test-model")
	m.permissionAsk = ask1
	m.permissionQueue = []*engine.PermissionAskEvent{ask2}

	// Answering the visible ask promotes the queued one.
	m = applyKey(t, m, "a")
	select {
	case ans := <-ch1:
		if ans.Decision != engine.PermAllow {
			t.Fatalf("ask1 expected allow; got %v", ans.Decision)
		}
	default:
		t.Fatal("ask1 didn't receive an answer")
	}
	if m.permissionAsk != ask2 {
		t.Fatal("queued ask2 should have been promoted to the visible slot")
	}
	if len(m.permissionQueue) != 0 {
		t.Fatalf("queue should be drained; len=%d", len(m.permissionQueue))
	}

	// The promoted ask answers through its own Decision channel.
	m = applyKey(t, m, "d")
	select {
	case ans := <-ch2:
		if ans.Decision != engine.PermDeny {
			t.Fatalf("ask2 expected deny; got %v", ans.Decision)
		}
	default:
		t.Fatal("ask2 didn't receive an answer")
	}
	if m.permissionAsk != nil {
		t.Fatal("slot should be empty after the queue drains")
	}
}

func TestPermissionAskQueue_QuitDeniesQueued(t *testing.T) {
	ch1 := make(chan engine.PermissionAnswer, 1)
	ch2 := make(chan engine.PermissionAnswer, 1)
	ask1 := &engine.PermissionAskEvent{ToolName: "Bash", Decision: ch1}
	ask2 := &engine.PermissionAskEvent{ToolName: "Read", Decision: ch2}

	m := newTestModel(t, "test-model")
	m.permissionAsk = ask1
	m.permissionQueue = []*engine.PermissionAskEvent{ask2}

	m = applyKey(t, m, "q")
	for i, ch := range []chan engine.PermissionAnswer{ch1, ch2} {
		select {
		case ans := <-ch:
			if ans.Decision != engine.PermDeny {
				t.Errorf("ask%d expected deny on quit; got %v", i+1, ans.Decision)
			}
		default:
			t.Errorf("ask%d left unanswered on quit — its engine would hang", i+1)
		}
	}
	if m.permissionAsk != nil || len(m.permissionQueue) != 0 {
		t.Error("quit must clear both the visible ask and the queue")
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
