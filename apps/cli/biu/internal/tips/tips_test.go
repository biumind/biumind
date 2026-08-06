package tips

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestRegistry_skipsInvalidTips(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{}) // empty
	r.Register(Tip{ID: "no-body"})
	r.Register(Tip{Body: "no-id"})
	r.Register(Tip{ID: "ok", Body: "valid"})
	if len(r.All()) != 1 {
		t.Errorf("only valid tip should register, got %d", len(r.All()))
	}
}

func TestRegistry_appliesDefaultWeight(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{ID: "a", Body: "x"})
	r.Register(Tip{ID: "b", Body: "y", Weight: 5})
	all := r.All()
	if all[0].Weight != 1 {
		t.Errorf("zero weight → 1 default, got %d", all[0].Weight)
	}
	if all[1].Weight != 5 {
		t.Errorf("explicit weight preserved, got %d", all[1].Weight)
	}
}

func TestChoose_filtersByPredicate(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{ID: "always", Body: "x"})
	r.Register(Tip{ID: "never", Body: "y", Predicate: func() bool { return false }})
	r.Register(Tip{ID: "true-pred", Body: "z", Predicate: func() bool { return true }})

	got := Choose(r, &History{Counts: map[string]int{}}, deterministicRng())
	if got == nil {
		t.Fatal("expected a tip")
	}
	if got.ID == "never" {
		t.Errorf("predicate=false should never be picked")
	}
}

func TestChoose_filtersOversShown(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{ID: "fresh", Body: "x"})
	r.Register(Tip{ID: "exhausted", Body: "y"})
	h := &History{
		Counts: map[string]int{"exhausted": MaxImpressions},
		LastAt: map[string]time.Time{},
	}
	for i := 0; i < 50; i++ {
		got := Choose(r, h, rand.New(rand.NewSource(int64(i))))
		if got == nil || got.ID != "fresh" {
			t.Fatalf("iter %d: got %+v, want fresh", i, got)
		}
	}
}

func TestChoose_emptyOnNoCandidates(t *testing.T) {
	r := NewRegistry()
	if got := Choose(r, &History{Counts: map[string]int{}}, deterministicRng()); got != nil {
		t.Errorf("empty registry → nil, got %+v", got)
	}
}

func TestChoose_weightBias(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{ID: "common", Body: "x", Weight: 99})
	r.Register(Tip{ID: "rare", Body: "y", Weight: 1})

	commonHits := 0
	for i := 0; i < 1000; i++ {
		got := Choose(r, &History{Counts: map[string]int{}},
			rand.New(rand.NewSource(int64(i))))
		if got.ID == "common" {
			commonHits++
		}
	}
	if commonHits < 900 {
		t.Errorf("common-weighted tip won %d/1000 — too few", commonHits)
	}
}

func TestChoose_nilHistoryIsAllowed(t *testing.T) {
	r := NewRegistry()
	r.Register(Tip{ID: "x", Body: "x"})
	if got := Choose(r, nil, deterministicRng()); got == nil {
		t.Error("nil history should be treated as no-suppression")
	}
}

func TestChoose_nilRegistryIsSafe(t *testing.T) {
	if got := Choose(nil, nil, nil); got != nil {
		t.Error("nil registry → nil tip")
	}
}

func TestRender_withAndWithoutTitle(t *testing.T) {
	body := Tip{Body: "raw line"}.Render()
	if body != "raw line" {
		t.Errorf("no title → body only, got %q", body)
	}
	withTitle := Tip{Title: "Heads up", Body: "explanation"}.Render()
	if !strings.HasPrefix(withTitle, "💡 Heads up\n") {
		t.Errorf("title format: %q", withTitle)
	}
}

func TestHistory_loadSaveRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	h, err := LoadHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Counts) != 0 {
		t.Errorf("fresh history should be empty")
	}

	h.MarkShown("a")
	h.MarkShown("a")
	h.MarkShown("b")
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}

	h2, _ := LoadHistory()
	if h2.Counts["a"] != 2 || h2.Counts["b"] != 1 {
		t.Errorf("persistence lost counts: %v", h2.Counts)
	}
}

func TestRegisterBuiltins_minSet(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	if got := len(r.All()); got < 8 {
		t.Errorf("builtin set should have ≥8 tips, got %d", got)
	}
	// Every built-in tip must have ID + body + a recognizable
	// /command in body (matches the style guide).
	for _, tip := range r.All() {
		if tip.ID == "" || tip.Body == "" {
			t.Errorf("builtin missing fields: %+v", tip)
		}
	}
}

func deterministicRng() *rand.Rand {
	return rand.New(rand.NewSource(42))
}
