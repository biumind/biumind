package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// stubHinter mirrors planhint.Analyser without importing it (would
// be an import cycle for engine tests). Returns Note when prompt
// contains "refactor".
type stubHinter struct {
	enabled bool
	calls   int
}

func (h *stubHinter) Enabled() bool { return h != nil && h.enabled }

func (h *stubHinter) Analyse(prompt string) PlanHint {
	if h == nil || !h.enabled {
		return PlanHint{}
	}
	h.calls++
	if strings.Contains(strings.ToLower(prompt), "refactor") {
		return PlanHint{Note: "Consider EnterPlanMode first.", MatchedKeyword: "refactor"}
	}
	return PlanHint{}
}

func TestPlanHinterFiresForLargeChange(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("ok"),
	}}
	st := state.New()
	reg := NewRegistry()
	hinter := &stubHinter{enabled: true}
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
		PlanHinter:        hinter,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "refactor the auth module"))

	// Find a system message containing the hint note.
	var hit bool
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText && strings.Contains(b.Text, "EnterPlanMode") {
				hit = true
			}
		}
	}
	if !hit {
		t.Fatalf("expected plan-mode hint in state; messages=%+v", st.Snapshot())
	}
}

func TestPlanHinterSilentOnSmallChange(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("ok"),
	}}
	st := state.New()
	reg := NewRegistry()
	hinter := &stubHinter{enabled: true}
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
		PlanHinter:        hinter,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "fix the typo in README"))

	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText && strings.Contains(b.Text, "EnterPlanMode") {
				t.Fatalf("unexpected hint for small change: %q", b.Text)
			}
		}
	}
}

func TestPlanHinterSkippedInPlanMode(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("ok"),
	}}
	st := state.New()
	reg := NewRegistry()
	hinter := &stubHinter{enabled: true}
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModePlan)
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: perms,
		PlanHinter:  hinter,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "refactor everything"))

	if hinter.calls != 0 {
		t.Errorf("hinter must not be called when already in plan mode; calls=%d", hinter.calls)
	}
}
