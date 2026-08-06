// LowestLatency strategy — pick the channel with the smallest observed
// p50 latency.
//
// Data source: channels.latency_p50_ms — populated by
// supervisor.RecordSuccess via an EWMA (1/8 weight on each new sample,
// see registry/channels.go). Health probe seeds it; production traffic
// keeps it fresh. Stale or never-tested channels (latency_p50_ms == 0)
// get probed first so the strategy gathers statistics before
// committing — without this rule a brand-new channel with no data
// would be ignored forever in favour of older slower ones.
//
// Failure semantics — same priority-tier model as weighted:
//   1. Pick the highest-priority equivalence class (still binds admin
//      ordering: priority 100 always beats priority 50 regardless of
//      latency).
//   2. Within the class, sort by latency_p50_ms ASC, treating 0 as
//      negative infinity ("hasn't been measured, give it a chance").
//   3. Skip Exclude.
//   4. If the top class is fully excluded, descend to next priority.
//
// On retry (Exclude non-empty), failed channels are skipped exactly
// like weighted. The error map is currently ignored — future
// "intelligent retry" extensions could read it (e.g. don't pick a
// channel that 429'd two attempts ago).

package router

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// LowestLatency satisfies router.Strategy.
type LowestLatency struct{}

// NewLowestLatency returns the lowest_latency Strategy.
func NewLowestLatency() *LowestLatency { return &LowestLatency{} }

// Name implements Strategy. Matches the SQL CHECK enum value.
func (s *LowestLatency) Name() string { return string(registry.StrategyLowestLatency) }

// Pick implements Strategy.
func (s *LowestLatency) Pick(ctx context.Context, in PickInput) (*registry.Channel, error) {
	if len(in.Candidates) == 0 {
		return nil, ErrNoCandidates
	}

	// Walk priority tiers — Candidates is pre-sorted by priority DESC
	// (registry.Channels.ListActiveByModel guarantees this).
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

// pickFromTier sorts the in-play subset of `tier` by latency ascending
// and returns the head. latency_p50_ms == 0 channels get sorted to the
// front (treated as -1 in the comparator) so they're probed first;
// that lets a new channel build up history rather than starving.
func (s *LowestLatency) pickFromTier(tier []registry.Channel, exclude map[uuid.UUID]error) *registry.Channel {
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
	sort.SliceStable(avail, func(i, j int) bool {
		li := avail[i].LatencyP50Ms
		lj := avail[j].LatencyP50Ms
		// 0 = never measured. Sort to the front so it gets traffic and
		// builds statistics. Two channels with the same latency keep
		// their input order (stable sort), which matches admin's weight
		// configuration as a tiebreaker.
		if li == 0 && lj != 0 {
			return true
		}
		if lj == 0 && li != 0 {
			return false
		}
		return li < lj
	})
	return avail[0]
}
