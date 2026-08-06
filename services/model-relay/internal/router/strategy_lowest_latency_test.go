package router

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func chWithLatency(name string, priority, latencyMs int) registry.Channel {
	return registry.Channel{
		ID:           uuid.NewSHA1(uuid.NameSpaceDNS, []byte(name)),
		Priority:     priority,
		Weight:       1,
		LatencyP50Ms: latencyMs,
		Status:       registry.StatusActive,
	}
}

func TestLowestLatency_Name(t *testing.T) {
	if NewLowestLatency().Name() != "lowest_latency" {
		t.Fatalf("expected lowest_latency")
	}
}

func TestLowestLatency_NoCandidates(t *testing.T) {
	_, err := NewLowestLatency().Pick(context.Background(), PickInput{})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}
}

func TestLowestLatency_PicksLowest(t *testing.T) {
	slow := chWithLatency("slow", 100, 800)
	fast := chWithLatency("fast", 100, 200)
	med := chWithLatency("med", 100, 400)

	pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{slow, fast, med},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != fast.ID {
		t.Fatalf("expected fast, got %s", pick.ID)
	}
}

// Channels with 0 latency (never measured) should be picked first so
// they build statistics. Without this, a new channel never gets traffic.
func TestLowestLatency_UnmeasuredFirst(t *testing.T) {
	measured := chWithLatency("measured", 100, 100)
	unmeasured := chWithLatency("new", 100, 0)

	pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{measured, unmeasured},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != unmeasured.ID {
		t.Fatalf("unmeasured should win to gather statistics, got %s", pick.ID)
	}
}

// Priority always beats latency — admin ordering is sacred.
func TestLowestLatency_PriorityBeatsLatency(t *testing.T) {
	highPrioSlow := chWithLatency("high_slow", 100, 1000)
	lowPrioFast := chWithLatency("low_fast", 50, 50)

	pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{highPrioSlow, lowPrioFast},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != highPrioSlow.ID {
		t.Fatalf("priority must beat latency: got %s", pick.ID)
	}
}

// Top tier all excluded → fall back to next tier and pick lowest there.
func TestLowestLatency_FallbackTier(t *testing.T) {
	top1 := chWithLatency("top1", 100, 50)
	top2 := chWithLatency("top2", 100, 60)
	low := chWithLatency("low", 50, 200)

	pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{top1, top2, low},
		Exclude: map[uuid.UUID]error{
			top1.ID: errors.New("a"),
			top2.ID: errors.New("b"),
		},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != low.ID {
		t.Fatalf("expected fallback to low tier, got %s", pick.ID)
	}
}

func TestLowestLatency_AllExcluded(t *testing.T) {
	a := chWithLatency("a", 100, 50)
	b := chWithLatency("b", 50, 200)
	_, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{a, b},
		Exclude: map[uuid.UUID]error{
			a.ID: errors.New("e"),
			b.ID: errors.New("e"),
		},
	})
	if !errors.Is(err, ErrAllExcluded) {
		t.Fatalf("expected ErrAllExcluded, got %v", err)
	}
}

// Stable sort: equal latency falls back to slice order (which is
// admin-controlled via weight in the underlying Repo query).
func TestLowestLatency_StableTieBreak(t *testing.T) {
	a := chWithLatency("a_eq", 100, 100)
	b := chWithLatency("b_eq", 100, 100)

	for i := 0; i < 20; i++ {
		pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b},
		})
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		// Stable sort means input order wins on ties — `a` (first) every time.
		if pick.ID != a.ID {
			t.Fatalf("expected stable tie-break to first input, got %s on iter %d", pick.ID, i)
		}
	}
}

// Mixed measured / unmeasured / excluded: unmeasured first, but if
// excluded, fall through to lowest measured.
func TestLowestLatency_UnmeasuredExcludedFallthrough(t *testing.T) {
	unmeasured := chWithLatency("u", 100, 0)
	fast := chWithLatency("fast", 100, 100)
	slow := chWithLatency("slow", 100, 500)

	pick, err := NewLowestLatency().Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{unmeasured, fast, slow},
		Exclude:    map[uuid.UUID]error{unmeasured.ID: errors.New("just retried")},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != fast.ID {
		t.Fatalf("expected fast (lowest measured) after unmeasured excluded, got %s", pick.ID)
	}
}
