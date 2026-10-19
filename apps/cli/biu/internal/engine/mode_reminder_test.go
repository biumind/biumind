// Tests for the auto-permission-mode reminder (mode_reminder.go) —
// enter/exit injection, idempotency across repeated SetMode calls,
// and the Options.ModeReminder kill switch.

package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

func newReminderEngine(t *testing.T, perms *permissions.Context, reminder bool, turns int) (*engine.QueryEngine, *state.AppState) {
	t.Helper()
	script := make([][]engine.StreamFrame, 0, turns)
	for i := 0; i < turns; i++ {
		script = append(script, textTurn("ok"))
	}
	prov := &scripted{turns: script}
	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: engine.NewRegistry(), Provider: prov,
		Model:        "test",
		Permissions:  perms,
		ModeReminder: reminder,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng, st
}

func countReminder(flat, marker string) int {
	return strings.Count(flat, marker)
}

// Entering auto injects the enter reminder exactly once on the next
// turn; repeating SetMode(ModeAuto) before / after that turn does not
// duplicate it.
func TestModeReminderEnterInjectsOnce(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	perms.SetMode(permissions.ModeAuto) // duplicate before any turn
	eng, st := newReminderEngine(t, perms, true, 2)

	drainAll(eng.Submit(context.Background(), "first"))
	perms.SetMode(permissions.ModeAuto) // duplicate between turns
	drainAll(eng.Submit(context.Background(), "second"))

	flat := flattenSystemMessages(st)
	if n := countReminder(flat, "Auto permission mode is active"); n != 1 {
		t.Errorf("enter reminder should appear exactly once across two turns; got %d\n%s", n, flat)
	}
	if strings.Contains(flat, "no longer active") {
		t.Errorf("exit reminder should not appear while still in auto:\n%s", flat)
	}
	for _, must := range []string{"handled automatically", "AskUserQuestion"} {
		if !strings.Contains(flat, must) {
			t.Errorf("enter reminder missing %q:\n%s", must, flat)
		}
	}
}

// Leaving auto injects the exit reminder once; switching back to auto
// re-injects the enter reminder (enter+exit pair per crossing).
func TestModeReminderExitAndReenter(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	eng, st := newReminderEngine(t, perms, true, 3)

	drainAll(eng.Submit(context.Background(), "in auto"))
	perms.SetMode(permissions.ModeDefault)
	drainAll(eng.Submit(context.Background(), "back to default"))
	perms.SetMode(permissions.ModeAuto)
	drainAll(eng.Submit(context.Background(), "auto again"))

	flat := flattenSystemMessages(st)
	if n := countReminder(flat, "Auto permission mode is active"); n != 2 {
		t.Errorf("enter reminder should appear twice (enter → exit → enter); got %d", n)
	}
	if n := countReminder(flat, "no longer active"); n != 1 {
		t.Errorf("exit reminder should appear exactly once; got %d", n)
	}
}

// ModeReminder=false keeps history untouched even across transitions.
func TestModeReminderDisabledInjectsNothing(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	eng, st := newReminderEngine(t, perms, false, 2)

	drainAll(eng.Submit(context.Background(), "in auto"))
	perms.SetMode(permissions.ModeDefault)
	drainAll(eng.Submit(context.Background(), "default"))

	flat := flattenSystemMessages(st)
	if strings.Contains(flat, "permission-mode-reminder") {
		t.Errorf("disabled reminder must not inject anything:\n%s", flat)
	}
}

// Sessions that never enter auto get no reminder at all.
func TestModeReminderNeverAutoInjectsNothing(t *testing.T) {
	perms := permissions.NewContext()
	eng, st := newReminderEngine(t, perms, true, 1)

	drainAll(eng.Submit(context.Background(), "plain turn"))

	flat := flattenSystemMessages(st)
	if strings.Contains(flat, "permission-mode-reminder") {
		t.Errorf("no auto transition → no reminder:\n%s", flat)
	}
}
