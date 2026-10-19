// Tests for the /mode auto first-time opt-in dialog (mode_optin.go).
//
// Covered:
//   - first /mode auto → dialog armed, mode NOT switched yet
//   - "y" → mode switches + marker persists to the user layer
//   - "n" / esc → mode unchanged, nothing persisted
//   - already-opted-in user → no dialog, direct switch
//   - non-auto targets never trigger the dialog

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

func TestModeAuto_FirstTimeArmsOptInDialog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, "test-model")

	out, _ := m.runSlash("/mode auto")
	nm := out.(model)

	if !nm.modeOptInPending {
		t.Error("first /mode auto should arm the opt-in dialog")
	}
	if got := nm.engine.Permissions().Mode(); got != permissions.ModeDefault {
		t.Errorf("mode must not switch before confirmation; got %q", got)
	}
	panel := renderModeOptIn(nm)
	for _, want := range []string{"Enable auto permission mode?", "WITHOUT asking", "dangerousCommandGuard", "y enable"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q:\n%s", want, panel)
		}
	}
}

func TestModeOptIn_ConfirmSwitchesAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := newTestModel(t, "test-model")

	out, _ := m.runSlash("/mode auto")
	m = out.(model)
	m = applyKey(t, m, "y")

	if m.modeOptInPending {
		t.Error("dialog should close after confirmation")
	}
	if got := m.engine.Permissions().Mode(); got != permissions.ModeAuto {
		t.Errorf("mode should be auto after 'y'; got %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".biumind", "settings.json"))
	if err != nil {
		t.Fatalf("opt-in marker should persist to user settings: %v", err)
	}
	if !strings.Contains(string(raw), `"autoModeOptIn": true`) {
		t.Errorf("user settings should carry autoModeOptIn=true; got %s", raw)
	}

	// Switching away and back must NOT re-prompt: the marker is set.
	m = m.applyPermissionMode(permissions.ModeDefault)
	out, _ = m.runSlash("/mode auto")
	m = out.(model)
	if m.modeOptInPending {
		t.Error("opted-in user should not see the dialog again")
	}
	if got := m.engine.Permissions().Mode(); got != permissions.ModeAuto {
		t.Errorf("second switch should go straight to auto; got %q", got)
	}
}

func TestModeOptIn_CancelKeepsMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, "test-model")

	out, _ := m.runSlash("/mode auto")
	m = out.(model)
	m = applyKey(t, m, "n")

	if m.modeOptInPending {
		t.Error("dialog should close after cancel")
	}
	if got := m.engine.Permissions().Mode(); got != permissions.ModeDefault {
		t.Errorf("cancel must leave the mode alone; got %q", got)
	}
	// Nothing persisted — a later attempt must prompt again.
	out, _ = m.runSlash("/mode auto")
	m = out.(model)
	if !m.modeOptInPending {
		t.Error("cancel should not persist the marker; dialog must reappear")
	}
	// Esc behaves the same.
	m = applyKey(t, m, "esc")
	if m.modeOptInPending {
		t.Error("esc should cancel the dialog")
	}
	if got := m.engine.Permissions().Mode(); got != permissions.ModeDefault {
		t.Errorf("esc must leave the mode alone; got %q", got)
	}
}

func TestModeAuto_SkipsDialogWhenAlreadyOptedIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".biumind"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"permissions":{"autoModeOptIn":true}}`)
	if err := os.WriteFile(filepath.Join(home, ".biumind", "settings.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, "test-model")
	out, _ := m.runSlash("/mode auto")
	m = out.(model)

	if m.modeOptInPending {
		t.Error("opted-in user should not see the dialog")
	}
	if got := m.engine.Permissions().Mode(); got != permissions.ModeAuto {
		t.Errorf("mode should switch directly; got %q", got)
	}
}

func TestMode_NonAutoTargetsNeverPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, target := range []string{"default", "plan", "acceptEdits", "bypassPermissions"} {
		m := newTestModel(t, "test-model")
		out, _ := m.runSlash("/mode " + target)
		m = out.(model)
		if m.modeOptInPending {
			t.Errorf("/mode %s should never arm the opt-in dialog", target)
		}
	}
}

// The dialog renders above the input area while armed and disappears
// from inputArea once resolved.
func TestModeOptIn_PanelLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, "test-model")
	// inputArea drives the real textarea — zero-value models panic in
	// View (same caveat as modelWithAsk).
	m.textarea = textarea.New()
	if got := renderModeOptIn(m); got != "" {
		t.Errorf("disarmed dialog must render nothing; got %q", got)
	}
	out, _ := m.runSlash("/mode auto")
	m = out.(model)
	if !strings.Contains(m.inputArea(), "Enable auto permission mode?") {
		t.Error("armed dialog should render inside inputArea")
	}
}
