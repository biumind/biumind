// Compact boundary test for the auto-mode reminder (mode_reminder.go).
//
// The enter reminder is a one-shot history message; a compact cycle
// (auto or manual /compact — both funnel through runCompact) swaps the
// whole history for a summary and would silently drop it while
// autoReminderShown stayed true, leaving the model without the
// "approvals are automatic" contract for the rest of an auto session.
// runCompact re-arms the flag so the next turn re-injects.

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

func reminderPresent(st *state.AppState) bool {
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "Auto permission mode is active") {
				return true
			}
		}
	}
	return false
}

func TestModeReminder_ReinjectsAfterCompact(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	prov := &summariseProvider{
		scripts: [][]StreamFrame{
			textTurn("first"),
			textTurn("second"),
		},
		summary: "compact summary",
	}
	st := state.New()
	eng, err := New(Options{
		State: st, Tools: NewRegistry(), Provider: prov, Model: "test",
		Permissions:  perms,
		ModeReminder: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Turn 1: enter reminder lands in history.
	drainAll(eng.Submit(context.Background(), "one"))
	if !reminderPresent(st) {
		t.Fatal("enter reminder missing after first auto turn")
	}

	// Compact wipes the whole history — reminder included.
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		if err := eng.Compact(context.Background(), out); err != nil {
			t.Errorf("compact: %v", err)
		}
	}()
	drainAll(out)
	if reminderPresent(st) {
		t.Fatal("reminder should be gone right after compact (history was swapped)")
	}

	// Turn 2: still in auto → the lazy injector must re-inject.
	drainAll(eng.Submit(context.Background(), "two"))
	if !reminderPresent(st) {
		t.Error("enter reminder must be re-injected on the first post-compact turn")
	}

	// …and only once: turn 3 must not add a second copy.
	drainAll(eng.Submit(context.Background(), "three"))
	count := 0
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "Auto permission mode is active") {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("reminder should appear exactly once post-compact; got %d", count)
	}
}

// Left auto before the compact: the re-arm must NOT resurrect the
// reminder (and the exit reminder is correctly skipped — the enter
// reminder it would retract was wiped by the compact anyway).
func TestModeReminder_NoReinjectAfterCompactWhenNotAuto(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("first"), textTurn("second")},
		summary: "s",
	}
	st := state.New()
	eng, err := New(Options{
		State: st, Tools: NewRegistry(), Provider: prov, Model: "test",
		Permissions:  perms,
		ModeReminder: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	drainAll(eng.Submit(context.Background(), "one"))
	perms.SetMode(permissions.ModeDefault)

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		_ = eng.Compact(context.Background(), out)
	}()
	drainAll(out)

	drainAll(eng.Submit(context.Background(), "two"))
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "permission-mode-reminder") {
				t.Errorf("no reminder (enter or exit) expected post-compact outside auto; got %q", b.Text)
			}
		}
	}
}
