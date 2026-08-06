package router

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func chForLB(name string, priority int) registry.Channel {
	return registry.Channel{
		ID:       uuid.NewSHA1(uuid.NameSpaceDNS, []byte(name)),
		Priority: priority,
		Weight:   1,
		Status:   registry.StatusActive,
	}
}

// ─── InflightCounter ───────────────────────────────────────────────

func TestInflight_AcquireRelease(t *testing.T) {
	c := NewInflightCounter()
	id := uuid.New()
	if c.Snapshot(id) != 0 {
		t.Fatalf("initial should be 0")
	}
	if v := c.Acquire(id); v != 1 {
		t.Fatalf("acquire = %d", v)
	}
	if v := c.Acquire(id); v != 2 {
		t.Fatalf("acquire = %d", v)
	}
	c.Release(id)
	if c.Snapshot(id) != 1 {
		t.Fatalf("snapshot = %d", c.Snapshot(id))
	}
	c.Release(id)
	if c.Snapshot(id) != 0 {
		t.Fatalf("snapshot = %d", c.Snapshot(id))
	}
	// Extra release floors at 0 (doesn't go negative)
	c.Release(id)
	if c.Snapshot(id) != 0 {
		t.Fatalf("over-release should floor: %d", c.Snapshot(id))
	}
}

// TestInflight_ConcurrentAcquireRelease verifies (a) -race finds no
// data race, (b) the floor-at-0 semantics hold under concurrent stress.
// Two phases: first all Acquires complete, then all Releases. We can't
// interleave Acquire/Release in racing goroutines because Release is a
// no-op when count==0 (defensive) — if a Release lost the schedule
// race against its paired Acquire, the matching Acquire would never
// be cancelled out. That's the right runtime semantic but defeats a
// "balanced" test.
func TestInflight_ConcurrentAcquireRelease(t *testing.T) {
	c := NewInflightCounter()
	id := uuid.New()
	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() { defer wg.Done(); c.Acquire(id) }()
	}
	wg.Wait()
	if got := c.Snapshot(id); got != int64(N) {
		t.Fatalf("after %d Acquires, count = %d", N, got)
	}

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() { defer wg.Done(); c.Release(id) }()
	}
	wg.Wait()
	if got := c.Snapshot(id); got != 0 {
		t.Fatalf("after %d Releases, count = %d", N, got)
	}
}

func TestInflight_SnapshotAll(t *testing.T) {
	c := NewInflightCounter()
	a, b, d := uuid.New(), uuid.New(), uuid.New()
	c.Acquire(a)
	c.Acquire(a)
	c.Acquire(b)
	// d not touched

	all := c.SnapshotAll()
	if all[a] != 2 || all[b] != 1 {
		t.Fatalf("all = %+v", all)
	}
	if _, ok := all[d]; ok {
		t.Fatalf("untouched channel should not appear")
	}

	// Release b to 0 → should disappear from SnapshotAll
	c.Release(b)
	all2 := c.SnapshotAll()
	if _, ok := all2[b]; ok {
		t.Fatalf("zero-count channel should be cleaned: %+v", all2)
	}
}

// ─── LeastBusy strategy ────────────────────────────────────────────

func TestLeastBusy_Name(t *testing.T) {
	if NewLeastBusy(NewInflightCounter()).Name() != "least_busy" {
		t.Fatalf("name mismatch")
	}
}

func TestLeastBusy_NewPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil counter")
		}
	}()
	_ = NewLeastBusy(nil)
}

func TestLeastBusy_NoCandidates(t *testing.T) {
	_, err := NewLeastBusy(NewInflightCounter()).Pick(context.Background(), PickInput{})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("expected ErrNoCandidates")
	}
}

func TestLeastBusy_PicksLeastLoaded(t *testing.T) {
	c := NewInflightCounter()
	busy := chForLB("busy", 100)
	idle := chForLB("idle", 100)
	c.Acquire(busy.ID)
	c.Acquire(busy.ID)
	c.Acquire(busy.ID) // busy = 3 in-flight; idle = 0

	pick, err := NewLeastBusy(c).Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{busy, idle},
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if pick.ID != idle.ID {
		t.Fatalf("expected idle, got %s", pick.ID)
	}
}

func TestLeastBusy_PriorityBeatsLoad(t *testing.T) {
	c := NewInflightCounter()
	highBusy := chForLB("high_busy", 100)
	lowIdle := chForLB("low_idle", 50)
	c.Acquire(highBusy.ID)
	c.Acquire(highBusy.ID) // overloaded

	pick, _ := NewLeastBusy(c).Pick(context.Background(), PickInput{
		Candidates: []registry.Channel{highBusy, lowIdle},
	})
	if pick.ID != highBusy.ID {
		t.Fatalf("priority should beat in-flight count")
	}
}

func TestLeastBusy_FallbackTier(t *testing.T) {
	c := NewInflightCounter()
	top1 := chForLB("top1", 100)
	top2 := chForLB("top2", 100)
	low := chForLB("low", 50)

	pick, err := NewLeastBusy(c).Pick(context.Background(), PickInput{
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
		t.Fatalf("expected fallback")
	}
}

func TestLeastBusy_AllExcluded(t *testing.T) {
	c := NewInflightCounter()
	a := chForLB("a", 100)
	b := chForLB("b", 50)
	_, err := NewLeastBusy(c).Pick(context.Background(), PickInput{
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

func TestLeastBusy_StableTieBreak(t *testing.T) {
	c := NewInflightCounter()
	a := chForLB("a_eq", 100)
	b := chForLB("b_eq", 100) // both 0 in-flight
	for i := 0; i < 20; i++ {
		pick, _ := NewLeastBusy(c).Pick(context.Background(), PickInput{
			Candidates: []registry.Channel{a, b},
		})
		// Stable sort + identical zero count → input order wins (a)
		if pick.ID != a.ID {
			t.Fatalf("stable tie-break broken at iter %d: got %s", i, pick.ID)
		}
	}
}

// End-to-end load distribution — Acquire after each Pick simulates
// the resolver wrapper's lifecycle. After many requests with no
// Releases (long-running streams), load should be balanced across
// channels.
func TestLeastBusy_DistributesLoad(t *testing.T) {
	c := NewInflightCounter()
	channels := []registry.Channel{
		chForLB("c1", 100),
		chForLB("c2", 100),
		chForLB("c3", 100),
	}
	strategy := NewLeastBusy(c)

	const N = 30
	for i := 0; i < N; i++ {
		pick, err := strategy.Pick(context.Background(), PickInput{
			Candidates: channels,
		})
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		c.Acquire(pick.ID) // simulate request started, never finishes
	}
	all := c.SnapshotAll()
	// 30 requests / 3 channels = 10 each, ±1 due to scheduling order
	for _, ch := range channels {
		got := all[ch.ID]
		if got < 9 || got > 11 {
			t.Errorf("channel %s got %d, want ~10", ch.ID, got)
		}
	}
}
