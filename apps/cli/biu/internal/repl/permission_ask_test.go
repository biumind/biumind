// Tests for the permission-ask UI surface (renderer + key handler).

package repl

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func modelWithAsk(t *testing.T, ask *engine.PermissionAskEvent) model {
	t.Helper()
	m := newTestModel(t, "test-model")
	// Feedback typing drives the real textarea — zero-value models
	// panic in Reset/Focusing, so give the test model a live one.
	m.textarea = textarea.New()
	m.permissionAsk = ask
	return m
}

func TestRenderPermissionAsk_NoSuggestions(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": "rm /tmp/x"},
		Reason:   "destructive bash",
	}
	got := renderPermissionAsk(modelWithAsk(t, ask))
	for _, want := range []string{
		"Permission required",
		"1. Allow once",
		"2. Always allow this exact command (this session)",
		"3. Deny",
		"4. Deny with feedback",
		"enter confirm",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Cursor marker on the first option.
	if !strings.Contains(got, "▶ 1. Allow once") {
		t.Errorf("first option should be focused:\n%s", got)
	}
}

func TestRenderPermissionAsk_FullCommandNeverTruncated(t *testing.T) {
	long := "cd /some/very/long/path/that/would/definitely/be/truncated/by/the/old/summarise/input/path && npm run build -- --mode=production --flags=galore && echo done"
	ask := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": long},
	}
	got := renderPermissionAsk(modelWithAsk(t, ask))
	if !strings.Contains(got, long) {
		t.Errorf("approval dialog must show the full command; got:\n%s", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("approval dialog must not ellipsize; got:\n%s", got)
	}
	// Multi-line commands render every line.
	multi := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": "ls -la\npwd\nwhoami"},
	}
	got = renderPermissionAsk(modelWithAsk(t, multi))
	for _, line := range []string{"$ ls -la", "· pwd", "· whoami"} {
		if !strings.Contains(got, line) {
			t.Errorf("missing command line %q in:\n%s", line, got)
		}
	}
}

func TestRenderPermissionAsk_DangerWarning(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": "sudo rm -rf /var/log"},
	}
	got := renderPermissionAsk(modelWithAsk(t, ask))
	if !strings.Contains(got, "Dangerous:") {
		t.Errorf("dangerous command must be flagged:\n%s", got)
	}
	safe := &engine.PermissionAskEvent{
		ToolName: "Bash",
		Input:    map[string]any{"command": "ls -la"},
	}
	if got := renderPermissionAsk(modelWithAsk(t, safe)); strings.Contains(got, "Dangerous:") {
		t.Errorf("benign command must not be flagged:\n%s", got)
	}
	// Non-Bash tools never flag, even with a scary-looking path.
	read := &engine.PermissionAskEvent{
		ToolName: "Read",
		Input:    map[string]any{"file_path": "/sudo/rm"},
	}
	if got := renderPermissionAsk(modelWithAsk(t, read)); strings.Contains(got, "Dangerous:") {
		t.Errorf("non-Bash tool must not be flagged:\n%s", got)
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
	got := renderPermissionAsk(modelWithAsk(t, ask))
	if !strings.Contains(got, "3. Allow + add scratch/ to working dirs") {
		t.Errorf("suggestion should be a numbered option; got:\n%s", got)
	}
	if !strings.Contains(got, "[w]") {
		t.Errorf("suggestion hotkey hint should render; got:\n%s", got)
	}
	// Read outside working dirs → whole-tool session label.
	if !strings.Contains(got, "Always allow Read (this session)") {
		t.Errorf("tool-scope always label missing:\n%s", got)
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
	got := renderPermissionAsk(modelWithAsk(t, ask))
	if strings.Contains(got, "no hotkey") {
		t.Errorf("missing-HotKey suggestion should not render: %q", got)
	}
}

func TestRenderPermissionAsk_SubAgentOriginAndQueue(t *testing.T) {
	ask := &engine.PermissionAskEvent{ToolName: "Bash"}
	ask.SetBaseForTest("agent-2", "tu_agent_1")
	m := modelWithAsk(t, ask)
	m.permissionQueue = []*engine.PermissionAskEvent{{}, {}}
	got := renderPermissionAsk(m)
	if !strings.Contains(got, "(sub-agent)") {
		t.Errorf("forwarded sub-agent ask must be labelled: %q", got)
	}
	if !strings.Contains(got, "+2 queued") {
		t.Errorf("queued count must render: %q", got)
	}
	// Root-engine asks carry no parent tool_use id → no label.
	root := modelWithAsk(t, &engine.PermissionAskEvent{ToolName: "Bash"})
	if got := renderPermissionAsk(root); strings.Contains(got, "sub-agent") {
		t.Errorf("root ask must not be labelled as sub-agent: %q", got)
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

	m := modelWithAsk(t, ask)
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
	m := modelWithAsk(t, ask)
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

func TestPermissionAskKey_ArrowNavAndEnter(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)

	// ↓ moves to option 2 (always-allow-session); wraps around.
	m = applyKey(t, m, "down")
	if m.permissionCursor != 1 {
		t.Fatalf("cursor should be 1 after down; got %d", m.permissionCursor)
	}
	// Enter on option 2 = allow + remember.
	m = applyKey(t, m, "enter")
	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermAllow || !ans.Remember {
			t.Errorf("option 2 must Allow+Remember; got %+v", ans)
		}
	default:
		t.Fatal("decision channel didn't receive an answer")
	}
}

func TestPermissionAskKey_DigitKeys(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)

	// "3" = Deny (no suggestions → options are once/always/deny/feedback).
	m = applyKey(t, m, "3")
	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermDeny {
			t.Errorf("digit 3 must Deny; got %v", ans.Decision)
		}
	default:
		t.Fatal("decision channel didn't receive an answer")
	}
	// Out-of-range digit is swallowed without answering.
	if m.permissionAsk != nil {
		t.Error("dialog should close after deny")
	}
}

func TestPermissionAskKey_DigitOutOfRangeSwallowed(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)
	m = applyKey(t, m, "9") // only 4 options
	select {
	case <-respCh:
		t.Fatal("out-of-range digit must not answer")
	default:
	}
	if m.permissionAsk == nil {
		t.Error("dialog should stay open")
	}
}

func TestPermissionAskKey_DenyWithFeedback(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)

	// Digit 4 = Deny with feedback → typing mode.
	m = applyKey(t, m, "4")
	if !m.permissionTyping {
		t.Fatal("option 4 must enter feedback typing mode")
	}
	// Type the feedback, then Enter submits deny+feedback.
	m.textarea.SetValue("don't delete, move to trash instead")
	m = applyKey(t, m, "enter")
	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermDeny {
			t.Errorf("feedback submit must Deny; got %v", ans.Decision)
		}
		if ans.Feedback != "don't delete, move to trash instead" {
			t.Errorf("feedback text lost: %q", ans.Feedback)
		}
	default:
		t.Fatal("decision channel didn't receive an answer")
	}
	if m.permissionTyping {
		t.Error("typing mode should end after submit")
	}
}

func TestPermissionAskKey_FeedbackEscGoesBack(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)
	m = applyKey(t, m, "4")
	if !m.permissionTyping {
		t.Fatal("option 4 must enter feedback typing mode")
	}
	m = applyKey(t, m, "esc")
	if m.permissionTyping {
		t.Error("esc should leave typing mode")
	}
	select {
	case <-respCh:
		t.Fatal("esc from typing must not answer the ask")
	default:
	}
	if m.permissionAsk == nil {
		t.Error("dialog should still be open after esc-from-typing")
	}
}

func TestPermissionAskKey_EscDeniesButKeepsTurn(t *testing.T) {
	respCh := make(chan engine.PermissionAnswer, 1)
	ask := &engine.PermissionAskEvent{ToolName: "Bash", Decision: respCh}
	m := modelWithAsk(t, ask)
	cancelled := false
	m.streamCancel = func() { cancelled = true }

	m = applyKey(t, m, "esc")
	select {
	case ans := <-respCh:
		if ans.Decision != engine.PermDeny {
			t.Errorf("esc must Deny; got %v", ans.Decision)
		}
	default:
		t.Fatal("esc didn't answer the ask")
	}
	if cancelled {
		t.Error("esc must NOT abort the stream — that's q's job")
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

// applyKey drives one keypress through handleKey. We build a tea.KeyMsg
// for a single rune (sufficient for hotkeys "a"/"w"/"d") and dispatch.
func applyKey(t *testing.T, m model, key string) model {
	t.Helper()
	msg := keyMsg(key)
	out, _ := m.handleKey(msg)
	return out.(model)
}

func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg(tea.Key{Type: tea.KeyEnter})
	case "esc":
		return tea.KeyMsg(tea.Key{Type: tea.KeyEscape})
	case "up":
		return tea.KeyMsg(tea.Key{Type: tea.KeyUp})
	case "down":
		return tea.KeyMsg(tea.Key{Type: tea.KeyDown})
	}
	r := []rune(key)
	if len(r) != 1 {
		return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(key)})
	}
	return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: r})
}
