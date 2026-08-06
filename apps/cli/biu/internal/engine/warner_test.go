// Verifies that the runner consults the optional Warner interface
// and surfaces every returned warning into PermissionAskEvent.Reason
// before blocking on the user's answer. Tools that don't implement
// Warner pay zero cost — TestPermissionAskNoWarnerNoNote covers the
// negative path.

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// warningTool is a fakeTool variant that also implements Warner. We
// keep it local to this test file so the standard fakeTool stays
// minimal — adding Warnings to it would change the type-assertion
// truthiness for every other test.
type warningTool struct {
	*fakeTool
	warnings []string
}

func (w *warningTool) Warnings(_ map[string]any) []string {
	return w.warnings
}

func TestPermissionAskSurfacesWarnerNotes(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running rm", "tu_1", "Bash", `{"command":"rm -rf /tmp/x"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &warningTool{
		fakeTool: &fakeTool{name: "Bash", destructive: true},
		warnings: []string{"may recursively force-remove files"},
	}
	reg.Register(bash)

	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test"})
	ch := eng.Submit(context.Background(), "wipe /tmp/x")

	var sawAsk *PermissionAskEvent
	doneCh := make(chan struct{})
	go func() {
		for ev := range ch {
			if a, ok := ev.(*PermissionAskEvent); ok {
				sawAsk = a
				a.Decision <- PermissionAnswer{Decision: PermAllow}
			}
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine hung")
	}
	if sawAsk == nil {
		t.Fatal("expected PermissionAskEvent")
	}
	if !strings.Contains(sawAsk.Reason, "Note: may recursively force-remove files") {
		t.Errorf("warning not appended to Reason; got %q", sawAsk.Reason)
	}
}

// Multiple warnings are joined onto separate lines so the dialog can
// render them as a list. Order is preserved (first warning first).
func TestPermissionAskJoinsMultipleWarnings(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("dual", "tu_1", "Bash", `{"command":"x"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &warningTool{
		fakeTool: &fakeTool{name: "Bash", destructive: true},
		warnings: []string{"first warning", "second warning"},
	}
	reg.Register(bash)
	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test"})
	ch := eng.Submit(context.Background(), "go")

	var sawAsk *PermissionAskEvent
	doneCh := make(chan struct{})
	go func() {
		for ev := range ch {
			if a, ok := ev.(*PermissionAskEvent); ok {
				sawAsk = a
				a.Decision <- PermissionAnswer{Decision: PermAllow}
			}
		}
		close(doneCh)
	}()
	<-doneCh
	if sawAsk == nil {
		t.Fatal("expected PermissionAskEvent")
	}
	if !strings.Contains(sawAsk.Reason, "Note: first warning") ||
		!strings.Contains(sawAsk.Reason, "Note: second warning") {
		t.Errorf("multi-warning reason missing entries: %q", sawAsk.Reason)
	}
	first := strings.Index(sawAsk.Reason, "Note: first warning")
	second := strings.Index(sawAsk.Reason, "Note: second warning")
	if first > second {
		t.Errorf("warnings out of order in %q", sawAsk.Reason)
	}
}

// Tools that don't implement Warner produce a Reason with no "Note:"
// prefix injected by the runner. Catches accidental enrichment of
// non-Warner tools that would change the dialog text for every tool.
func TestPermissionAskNoWarnerNoNote(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("plain", "tu_1", "Bash", `{"command":"ls"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &fakeTool{name: "Bash", destructive: true} // NOT a Warner
	reg.Register(bash)
	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test"})
	ch := eng.Submit(context.Background(), "ls")

	var sawAsk *PermissionAskEvent
	doneCh := make(chan struct{})
	go func() {
		for ev := range ch {
			if a, ok := ev.(*PermissionAskEvent); ok {
				sawAsk = a
				a.Decision <- PermissionAnswer{Decision: PermAllow}
			}
		}
		close(doneCh)
	}()
	<-doneCh
	if sawAsk == nil {
		t.Fatal("expected PermissionAskEvent")
	}
	if strings.Contains(sawAsk.Reason, "Note:") {
		t.Errorf("non-Warner tool should produce no Note: prefix; got %q", sawAsk.Reason)
	}
}
