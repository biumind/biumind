// inflight.go — per-channel concurrent-request counter.
//
// least_busy strategy uses the live count to pick the channel with the
// fewest in-flight requests. main.go wires:
//
//   buildCredsResolver  → after chQuota gate passes → inflight.Acquire
//   buildOnRequestComplete (every success/fail path) → inflight.Release
//
// State is in-memory only. Process restart resets every counter to 0
// — acceptable because in-flight is a transient signal; within a few
// seconds the counters re-converge to reality. Multi-replica
// deployments do see drift (each replica only counts its own stream),
// but for least_busy that's fine: each replica picks its own least
// loaded channel, which is the right answer for that replica's
// workload.

package router

import (
	"sync"

	"github.com/google/uuid"
)

// InflightCounter tracks how many requests are currently routed
// through each channel. Goroutine-safe.
type InflightCounter struct {
	mu     sync.RWMutex
	counts map[uuid.UUID]int64
}

// NewInflightCounter constructs an empty counter.
func NewInflightCounter() *InflightCounter {
	return &InflightCounter{counts: map[uuid.UUID]int64{}}
}

// Acquire bumps the counter. Returns the post-increment value (mostly
// for tests and debug logs).
func (c *InflightCounter) Acquire(id uuid.UUID) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[id]++
	return c.counts[id]
}

// Release decrements the counter. Floors at 0 — a Release without a
// matching Acquire (e.g. resolver failure path missing a defer) just
// stays at 0 rather than going negative.
func (c *InflightCounter) Release(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[id] > 0 {
		c.counts[id]--
		if c.counts[id] == 0 {
			// Free map slot — channels long-disabled don't carry state.
			delete(c.counts, id)
		}
	}
}

// Snapshot returns the current count for a channel. Zero if unknown.
func (c *InflightCounter) Snapshot(id uuid.UUID) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.counts[id]
}

// SnapshotAll returns a copy of every active counter — for /metrics
// or admin debug. Empty channels (count==0) are omitted by Release's
// cleanup, so this stays bounded by the number of currently in-flight
// channels rather than every channel ever seen.
func (c *InflightCounter) SnapshotAll() map[uuid.UUID]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[uuid.UUID]int64, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}
