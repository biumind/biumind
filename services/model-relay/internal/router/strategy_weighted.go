// Weighted is the MVP strategy: pick the highest-priority equivalence
// class of candidates, then weighted-random within that class. Falls
// through to the next priority class only if the top class is empty
// (after applying Exclude).
//
// Why this matches the design intent:
//
//   priority 100  weight 3  → "main account, 3 shares of this tier"
//   priority 100  weight 1  → "backup account, 1 share of this tier"
//   priority 50   weight 1  → "fallback account, never picked while
//                              the priority-100 tier has options"
//
// On retry (Exclude non-empty), failed channels in the top class are
// skipped; if the entire top class is excluded, the strategy descends
// to priority 50 — same fallback semantics as new-api but with one
// integer-math-only path, no SQL re-query.

package router

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Weighted is the default Strategy. Callers construct via
// NewWeighted(); the type itself is exported only so test code can
// poke at the rng for deterministic assertions.
type Weighted struct {
	// rngSeed is overridable for tests. Production code uses
	// cryptographic rand via the secureUint64 helper, ignoring this
	// field. Tests set rngSeed to a deterministic value via a small
	// test-only constructor (NewWeightedSeeded).
	rngSeed *uint64
}

// NewWeighted returns the default-configured Weighted strategy.
func NewWeighted() *Weighted {
	return &Weighted{}
}

// Name implements Strategy.
func (w *Weighted) Name() string { return string(registry.StrategyWeighted) }

// Pick implements Strategy.
func (s *Weighted) Pick(ctx context.Context, in PickInput) (*registry.Channel, error) {
	if len(in.Candidates) == 0 {
		return nil, ErrNoCandidates
	}

	// Walk the candidates by priority tier. Candidates is already
	// sorted (priority DESC), so a single pass groups the equivalence
	// classes in order.
	var (
		tierStart = 0
		topPrio   = in.Candidates[0].Priority
	)
	for i := 0; i <= len(in.Candidates); i++ {
		atEnd := i == len(in.Candidates)
		if !atEnd && in.Candidates[i].Priority == topPrio {
			continue // still in the same tier
		}
		// Priority transition or end of slice — try the tier we just
		// closed.
		tier := in.Candidates[tierStart:i]
		if pick := s.pickFromTier(tier, in.Exclude); pick != nil {
			return pick, nil
		}
		// Tier exhausted (all excluded); advance to next.
		if atEnd {
			break
		}
		tierStart = i
		topPrio = in.Candidates[i].Priority
	}

	return nil, ErrAllExcluded
}

// pickFromTier runs weighted random within a single priority tier,
// after filtering Exclude. Returns nil if every channel in the tier
// is excluded. The exclude error values are ignored at this layer —
// any presence in the map means "skip".
func (s *Weighted) pickFromTier(tier []registry.Channel, exclude map[uuid.UUID]error) *registry.Channel {
	// Total weight of in-play channels.
	var totalWeight uint64
	for i := range tier {
		if _, skip := exclude[tier[i].ID]; skip {
			continue
		}
		// Weight is int >= 0 by CHECK constraint; treat 0 as 1 so
		// rows with no explicit weight still participate. (Schema
		// default is 1, so 0 only happens if admin set it deliberately
		// — we still want a non-empty in-play tier to produce a pick
		// rather than fall through to next tier silently.)
		w := tier[i].Weight
		if w <= 0 {
			w = 1
		}
		totalWeight += uint64(w)
	}
	if totalWeight == 0 {
		return nil // every candidate was excluded
	}

	r := s.randUint64() % totalWeight
	var cum uint64
	for i := range tier {
		if _, skip := exclude[tier[i].ID]; skip {
			continue
		}
		cw := uint64(tier[i].Weight)
		if cw == 0 {
			cw = 1
		}
		cum += cw
		if r < cum {
			return &tier[i]
		}
	}
	// Numerical edge case (shouldn't happen given the above):
	// fall back to the first non-excluded channel.
	for i := range tier {
		if _, skip := exclude[tier[i].ID]; !skip {
			return &tier[i]
		}
	}
	return nil
}

// randUint64 returns a cryptographically random uint64. Using
// crypto/rand keeps the strategy fork-safe across goroutines without
// needing a sync.Mutex around math/rand. Cost is ~100ns per call —
// negligible compared to the SELECT + decrypt cost of a real request.
func (s *Weighted) randUint64() uint64 {
	if s.rngSeed != nil {
		// Test path — deterministic. We just rotate the seed so a
		// sequence of Pick() calls returns predictable results.
		seed := *s.rngSeed
		*s.rngSeed = seed*6364136223846793005 + 1442695040888963407 // PCG-style
		return seed
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing on Linux/macOS means /dev/urandom is gone —
		// at that point everything is broken. Panic so we don't silently
		// route every request to the same channel.
		panic(fmt.Sprintf("router/weighted: crypto/rand failed: %v", err))
	}
	return binary.BigEndian.Uint64(b[:])
}
