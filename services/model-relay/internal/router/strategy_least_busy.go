// LeastBusy — pick the channel with the fewest in-flight requests.
// Best for workloads where channels have hard concurrency caps (e.g.
// "this key allows 10 simultaneous requests"); weighted/lowest_latency
// don't see concurrency, only completion stats.
//
// Tier model identical to weighted/lowest_latency:
//   1. Highest priority tier first (admin ordering wins).
//   2. Within tier, sort by Acquire count ASC; stable on ties so weight
//      breaks the tie deterministically (channels with the same
//      load get the higher-weight one first).
//   3. Skip Exclude. Empty top tier → descend.
//
// Snapshot timing: counter is read once per Pick, no atomic-on-each-
// candidate dance — by the time Strategy returns the value may already
// be stale. Acceptable for "least_busy" semantics: the goal is "spread
// load across channels", not "exact realtime min".

package router

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// LeastBusy satisfies router.Strategy.
type LeastBusy struct {
	counter *InflightCounter
}

// NewLeastBusy wires the strategy with a counter. main.go shares one
// counter across the strategy and the resolver wrapper so Acquire/
// Release land on the same map the Strategy reads.
func NewLeastBusy(counter *InflightCounter) *LeastBusy {
	if counter == nil {
		panic("router.NewLeastBusy: counter is nil")
	}
	return &LeastBusy{counter: counter}
}

// Name implements Strategy. Matches the SQL CHECK enum value.
func (s *LeastBusy) Name() string { return string(registry.StrategyLeastBusy) }

// Pick implements Strategy.
func (s *LeastBusy) Pick(ctx context.Context, in PickInput) (*registry.Channel, error) {
	if len(in.Candidates) == 0 {
		return nil, ErrNoCandidates
	}

	tierStart := 0
	topPrio := in.Candidates[0].Priority
	for i := 0; i <= len(in.Candidates); i++ {
		atEnd := i == len(in.Candidates)
		if !atEnd && in.Candidates[i].Priority == topPrio {
			continue
		}
		if pick := s.pickFromTier(in.Candidates[tierStart:i], in.Exclude); pick != nil {
			return pick, nil
		}
		if atEnd {
			break
		}
		tierStart = i
		topPrio = in.Candidates[i].Priority
	}
	return nil, ErrAllExcluded
}

// pickFromTier filters out Exclude, sorts by in-flight count ASC
// (stable so admin's input order — already weight-sorted — wins on
// ties), and returns head.
func (s *LeastBusy) pickFromTier(tier []registry.Channel, exclude map[uuid.UUID]error) *registry.Channel {
	avail := make([]*registry.Channel, 0, len(tier))
	for i := range tier {
		if _, skip := exclude[tier[i].ID]; skip {
			continue
		}
		avail = append(avail, &tier[i])
	}
	if len(avail) == 0 {
		return nil
	}
	// Snapshot all counts in one pass — read-locks the counter once
	// rather than per-element.
	counts := make(map[uuid.UUID]int64, len(avail))
	for _, c := range avail {
		counts[c.ID] = s.counter.Snapshot(c.ID)
	}
	sort.SliceStable(avail, func(i, j int) bool {
		return counts[avail[i].ID] < counts[avail[j].ID]
	})
	return avail[0]
}
