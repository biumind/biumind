package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// stubTool is the minimal Tool implementation engine_test.go also
// uses; redeclared here to keep deferred-tests independent.
type stubTool struct {
	name     string
	desc     string
	deferred bool
}

func (s *stubTool) Name() string                            { return s.name }
func (s *stubTool) Description(_ map[string]any) string     { return s.desc }
func (s *stubTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (s *stubTool) IsReadOnly(_ map[string]any) bool        { return true }
func (s *stubTool) IsDestructive(_ map[string]any) bool     { return false }
func (s *stubTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (s *stubTool) InterruptBehavior() string               { return "cancel" }
func (s *stubTool) ShouldDefer() bool                       { return s.deferred }
func (s *stubTool) Call(_ context.Context, _ map[string]any, _ *ToolEnv) (*ToolResultPayload, error) {
	return &ToolResultPayload{Content: []state.ContentBlock{{Type: state.ContentText, Text: "ok"}}}, nil
}

// TestDeferredSelection_AddHas — basic add/has/Names round-trip
// including idempotency on duplicate Add.
func TestDeferredSelection_AddHas(t *testing.T) {
	s := NewDeferredSelection()
	if s.Has("x") {
		t.Errorf("empty selection should have nothing")
	}
	s.Add("a", "b", "a") // dup ignored
	if !s.Has("a") || !s.Has("b") {
		t.Errorf("Add then Has failed")
	}
	if s.Has("c") {
		t.Errorf("unrelated name should not be present")
	}
	got := s.Names()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Names = %v, want [a b]", got)
	}
}

// TestDeferredSelection_Nil — nil receiver semantics: Add silently
// no-ops, Has returns false. Lets ToolEnv ship a nil Selections in
// test harnesses without forcing a guard at every call site.
func TestDeferredSelection_Nil(t *testing.T) {
	var s *DeferredSelection
	s.Add("x") // must not panic
	if s.Has("x") {
		t.Errorf("nil selection should always say false")
	}
	if got := s.Names(); got != nil {
		t.Errorf("nil Names should be nil; got %v", got)
	}
}

// TestFilterDeferredCatalog — non-deferred always present, deferred
// only when in the selection set.
func TestFilterDeferredCatalog(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "Read"})
	r.Register(&stubTool{name: "Write"})
	r.Register(&stubTool{name: "mcp__github__create_issue", deferred: true})
	r.Register(&stubTool{name: "mcp__github__list_pulls", deferred: true})

	sel := NewDeferredSelection()
	got := FilterDeferredCatalog(r, sel)
	names := toolNames(got)
	if !contains(names, "Read") || !contains(names, "Write") {
		t.Errorf("non-deferred should always pass through: %v", names)
	}
	if contains(names, "mcp__github__create_issue") || contains(names, "mcp__github__list_pulls") {
		t.Errorf("deferred should be filtered out when not selected: %v", names)
	}

	// Unlock one — only it surfaces; the other deferred stays hidden.
	sel.Add("mcp__github__create_issue")
	got2 := FilterDeferredCatalog(r, sel)
	n2 := toolNames(got2)
	if !contains(n2, "mcp__github__create_issue") {
		t.Errorf("selected deferred should surface: %v", n2)
	}
	if contains(n2, "mcp__github__list_pulls") {
		t.Errorf("non-selected deferred should stay hidden: %v", n2)
	}
}

// TestDeferredAttachment_FormatAndContents — the system-prompt block
// lists every un-selected deferred tool, sorted, and is empty when
// nothing's deferred or everything's unlocked.
func TestDeferredAttachment_FormatAndContents(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "Read"})
	r.Register(&stubTool{name: "mcp__b__x", deferred: true})
	r.Register(&stubTool{name: "mcp__a__y", deferred: true})

	sel := NewDeferredSelection()
	att := DeferredAttachment(r, sel)
	if !strings.Contains(att, "<available-deferred-tools>") {
		t.Errorf("missing wrapper tag: %s", att)
	}
	// Sort order: a__y appears before b__x.
	idxA := strings.Index(att, "mcp__a__y")
	idxB := strings.Index(att, "mcp__b__x")
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Errorf("expected sorted listing (a before b): %s", att)
	}

	// Unlock one — the unlocked name vanishes from the attachment.
	sel.Add("mcp__a__y")
	att2 := DeferredAttachment(r, sel)
	if strings.Contains(att2, "mcp__a__y") {
		t.Errorf("unlocked deferred should not appear: %s", att2)
	}
	if !strings.Contains(att2, "mcp__b__x") {
		t.Errorf("still-deferred should remain: %s", att2)
	}

	// Unlock everything — attachment is empty (cache-friendly: no
	// system message gets emitted that turn).
	sel.Add("mcp__b__x")
	if got := DeferredAttachment(r, sel); got != "" {
		t.Errorf("empty when all unlocked; got %q", got)
	}

	// No deferred tools at all → also empty.
	r2 := NewRegistry()
	r2.Register(&stubTool{name: "Read"})
	if got := DeferredAttachment(r2, NewDeferredSelection()); got != "" {
		t.Errorf("empty when nothing deferred; got %q", got)
	}
}

// TestBuildToolSpecs_FilterDeferred — buildToolSpecs honours the
// selection set: deferred tools are dropped from the wire-level
// catalog until selected.
func TestBuildToolSpecs_FilterDeferred(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "Read"})
	r.Register(&stubTool{name: "mcp__big__x", deferred: true})
	r.Register(&stubTool{name: "mcp__big__y", deferred: true})

	specs := buildToolSpecs(r, NewDeferredSelection())
	if specNamed(specs, "Read") == nil {
		t.Error("Read should be in catalog")
	}
	if specNamed(specs, "mcp__big__x") != nil {
		t.Error("deferred tool leaked into wire catalog")
	}

	// nil selection ⇒ keep everything (back-compat).
	specsAll := buildToolSpecs(r, nil)
	if specNamed(specsAll, "mcp__big__x") == nil {
		t.Error("nil selection should disable filtering")
	}
}

// ─── tiny helpers ─────────────────────────────────────────────────

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func specNamed(specs []ToolSpec, name string) *ToolSpec {
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i]
		}
	}
	return nil
}
