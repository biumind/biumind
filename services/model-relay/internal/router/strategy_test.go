package router

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Test-side stand-in for an upstream failure. Real callers pass the
// HTTP error / pgx error — Strategy doesn't care about the type.
var errTestFail = fmt.Errorf("test: upstream 500")

// channels with deterministic IDs derived from name, so test
// assertions can refer to them by short label.
func ch(name string, priority, weight int) registry.Channel {
	return registry.Channel{
		ID:       uuid.NewSHA1(uuid.NameSpaceDNS, []byte(name)),
		Priority: priority,
		Weight:   weight,
		Status:   registry.StatusActive,
	}
}

func newSeeded(seed uint64) *Weighted {
	w := NewWeighted()
	w.rngSeed = &seed
	return w
}

func TestWeighted_NoCandidates(t *testing.T) {
	w := NewWeighted()
	_, err := w.Pick(context.Background(), PickInput{})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}
}

func TestWeighted_SinglePicksItself(t *testing.T) {
	only := ch("only", 100, 1)
	pick, err := NewWeighted().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{only},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != only.ID {
		t.Fatalf("expected only candidate to be picked")
	}
}

// Two candidates at the same priority with weights 9:1 — over many
// trials the 9-weight one should be picked ~90% of the time. We allow
// a wide tolerance because only 1000 trials.
func TestWeighted_DistributionWithinTier(t *testing.T) {
	a := ch("a", 100, 9)
	b := ch("b", 100, 1)
	w := NewWeighted()

	const trials = 5000
	count := map[uuid.UUID]int{}
	for i := 0; i < trials; i++ {
		p, err := w.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b},
		})
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		count[p.ID]++
	}
	// a should be picked roughly 90% — accept 80~95% to keep the test
	// non-flaky on slow CI.
	ratioA := float64(count[a.ID]) / float64(trials)
	if ratioA < 0.80 || ratioA > 0.95 {
		t.Fatalf("weight 9:1 distribution off: a=%.3f (count=%d)", ratioA, count[a.ID])
	}
}

// Higher priority always wins over lower, regardless of weight.
func TestWeighted_PriorityBeatsWeight(t *testing.T) {
	high := ch("high", 100, 1) // priority 100, weight 1
	low := ch("low", 50, 1000) // priority 50, weight 1000
	w := NewWeighted()

	for i := 0; i < 50; i++ {
		p, err := w.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{high, low},
		})
		if err != nil || p.ID != high.ID {
			t.Fatalf("priority should beat weight: pick=%v err=%v", p, err)
		}
	}
}

// When the entire top priority tier is excluded, descend to the next.
func TestWeighted_PriorityFallback(t *testing.T) {
	high1 := ch("high1", 100, 1)
	high2 := ch("high2", 100, 1)
	low := ch("low", 50, 1)

	w := NewWeighted()
	excl := map[uuid.UUID]error{
		high1.ID: errTestFail, high2.ID: errTestFail,
	}
	pick, err := w.Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{high1, high2, low},
		Exclude:    excl,
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != low.ID {
		t.Fatalf("expected fallback to low priority, got %v", pick.ID)
	}
}

// All candidates excluded → ErrAllExcluded
func TestWeighted_AllExcluded(t *testing.T) {
	a := ch("a", 100, 1)
	b := ch("b", 50, 1)
	w := NewWeighted()
	_, err := w.Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{a, b},
		Exclude: map[uuid.UUID]error{
			a.ID: errTestFail, b.ID: errTestFail,
		},
	})
	if !errors.Is(err, ErrAllExcluded) {
		t.Fatalf("expected ErrAllExcluded, got %v", err)
	}
}

// Three priority tiers with mixed weights: top tier all excluded,
// middle tier weight distribution remains correct.
func TestWeighted_MidTierDistributionAfterExclude(t *testing.T) {
	top := ch("top", 100, 1)
	mid1 := ch("mid1", 50, 7)
	mid2 := ch("mid2", 50, 3)
	low := ch("low", 10, 1)

	w := NewWeighted()
	excl := map[uuid.UUID]error{top.ID: errTestFail}

	const trials = 2000
	count := map[uuid.UUID]int{}
	for i := 0; i < trials; i++ {
		p, err := w.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{top, mid1, mid2, low},
			Exclude:    excl,
		})
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		count[p.ID]++
	}
	if count[top.ID] != 0 {
		t.Fatalf("excluded top still got picked %d times", count[top.ID])
	}
	if count[low.ID] != 0 {
		t.Fatalf("low tier should not be reached when mid tier non-empty: %d", count[low.ID])
	}
	ratio := float64(count[mid1.ID]) / float64(trials)
	if ratio < 0.60 || ratio > 0.80 {
		t.Fatalf("mid1 7:3 ratio off: %.3f (mid1=%d mid2=%d)",
			ratio, count[mid1.ID], count[mid2.ID])
	}
}

// Zero weight rows still participate (treated as 1) — the schema
// CHECK allows weight 0 but the resolver shouldn't silently skip them.
func TestWeighted_ZeroWeightTreatedAsOne(t *testing.T) {
	a := ch("a_zw", 100, 0)
	b := ch("b_zw", 100, 0)
	w := NewWeighted()

	const trials = 500
	count := 0
	for i := 0; i < trials; i++ {
		p, err := w.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b},
		})
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if p.ID == a.ID {
			count++
		}
	}
	// Roughly 50/50 — both treated as weight 1
	ratio := float64(count) / float64(trials)
	if ratio < 0.40 || ratio > 0.60 {
		t.Fatalf("0-weight not treated as equal: a=%.3f", ratio)
	}
}

// Seeded path is deterministic — useful for tests of higher layers
// that need reproducible Pick results.
func TestWeighted_SeededDeterministic(t *testing.T) {
	a := ch("a_s", 100, 1)
	b := ch("b_s", 100, 1)
	c := ch("c_s", 100, 1)

	w1 := newSeeded(42)
	w2 := newSeeded(42)
	for i := 0; i < 20; i++ {
		p1, _ := w1.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b, c},
		})
		p2, _ := w2.Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b, c},
		})
		if p1.ID != p2.ID {
			t.Fatalf("seeded Pick should be deterministic at iter %d", i)
		}
	}
}

// Registry rejects double-registration loud + Get returns false on miss.
func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	w := NewWeighted()
	reg.Register(w)

	got, ok := reg.Get(w.Name())
	if !ok || got != w {
		t.Fatalf("Register/Get roundtrip broken")
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Fatalf("Get should miss")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate")
		}
	}()
	reg.Register(w)
}

func TestStrategyName(t *testing.T) {
	if NewWeighted().Name() != "weighted" {
		t.Fatalf("expected name 'weighted'")
	}
}
