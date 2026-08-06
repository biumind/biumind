// Package quota — generic rate / budget gates.
//
// Use cases at BiuMind:
//   - Relay: per-virtual-key requests-per-minute + tokens-per-minute.
//     Refused requests must NEVER hit upstream (would still cost money).
//   - Sandbox: per-owner concurrent + daily-create caps. Refused
//     requests must NEVER spawn a container.
//   - Brain.Search: per-IP query rate to keep abuse off the public side.
//
// Design:
//   - Quotas are scoped by (Bucket, Key). Bucket is the quota name
//     ("hub.rpm", "sandbox.daily"); Key is the subject (virtual key id,
//     owner id, IP address).
//   - Each Bucket has a [Spec]: window duration + max count + units
//     (requests / tokens / megabytes / whatever).
//   - Pre-flight: `CheckAndReserve(bucket, key, n)` returns whether
//     `n` units fit in the current window. If yes, increments + returns
//     headers; if no, returns the violation.
//   - Optional: `Refund(bucket, key, n)` lets callers return reservation
//     when downstream errored before consuming the quota.
//
// We default to an in-memory implementation. A Postgres-backed limiter
// for cross-replica counters lives in [biu/quota/pg] (TBD when we deploy
// >1 model-relay replica — in-process is correct for single-replica MVP).
package quota

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Spec — fixed-window budget. Production may want sliding windows for
// smoother behaviour; we'll add SpecKind later if it bites.
type Spec struct {
	Window time.Duration // e.g. 1 * time.Minute, 24 * time.Hour
	Limit  int64         // max units per window
	Unit   string        // "requests" / "tokens" / "bytes" — tag only
}

// Decision — what CheckAndReserve returns. Always populates Limit /
// Remaining / Reset, even on Allow=false, so the API layer can build
// X-RateLimit-* headers without re-reading Spec.
type Decision struct {
	Allow     bool
	Limit     int64
	Remaining int64
	Reset     time.Time // when the current window ends
	Bucket    string
}

// Headers — convenience for the http middleware. Returns the standard
// IETF draft fields (Limit, Remaining, Reset as unix timestamp).
func (d Decision) Headers() map[string]string {
	if d.Limit == 0 {
		return nil
	}
	return map[string]string{
		"X-RateLimit-Limit":     fmt.Sprintf("%d", d.Limit),
		"X-RateLimit-Remaining": fmt.Sprintf("%d", maxInt64(0, d.Remaining)),
		"X-RateLimit-Reset":     fmt.Sprintf("%d", d.Reset.Unix()),
	}
}

// Limiter — what services depend on.
type Limiter interface {
	// CheckAndReserve atomically tests if `n` units are available in
	// (bucket, key)'s current window and reserves them. n=0 is treated
	// as a "peek without spending". Unknown bucket is allowed (treated
	// as no limit) so callers can disable specific gates by leaving
	// them unconfigured.
	CheckAndReserve(bucket, key string, n int64) Decision
	// Refund returns previously-reserved units. Used when a request
	// failed AFTER reservation but BEFORE consuming the resource.
	Refund(bucket, key string, n int64)
	// Snapshot reads the current usage without modification.
	Snapshot(bucket, key string) Decision
}

// ─── In-memory implementation ─────────────────────────────

type inMemoryLimiter struct {
	specs   map[string]Spec
	mu      sync.Mutex
	buckets map[bucketKey]*window
	now     func() time.Time
}

type bucketKey struct{ bucket, key string }

type window struct {
	count int64
	end   time.Time
}

// NewInMemoryLimiter — most callers use this.
//
// `specs` keyed by bucket name. Buckets not present in the map are
// treated as "no limit" — Lookup returns Allow=true with Limit=0.
func NewInMemoryLimiter(specs map[string]Spec) Limiter {
	return newInMemoryLimiter(specs, time.Now)
}

func newInMemoryLimiter(specs map[string]Spec, now func() time.Time) *inMemoryLimiter {
	if specs == nil {
		specs = map[string]Spec{}
	}
	if now == nil {
		now = time.Now
	}
	return &inMemoryLimiter{
		specs:   specs,
		buckets: map[bucketKey]*window{},
		now:     now,
	}
}

func (l *inMemoryLimiter) CheckAndReserve(bucket, key string, n int64) Decision {
	spec, ok := l.specs[bucket]
	if !ok {
		// No spec configured → no limit. Caller's upstream still runs.
		return Decision{Allow: true, Bucket: bucket}
	}
	t := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	bk := bucketKey{bucket: bucket, key: key}
	w, exists := l.buckets[bk]
	if !exists || !t.Before(w.end) {
		w = &window{end: t.Add(spec.Window)}
		l.buckets[bk] = w
	}
	if w.count+n > spec.Limit {
		return Decision{
			Allow:     false,
			Limit:     spec.Limit,
			Remaining: spec.Limit - w.count,
			Reset:     w.end,
			Bucket:    bucket,
		}
	}
	w.count += n
	return Decision{
		Allow:     true,
		Limit:     spec.Limit,
		Remaining: spec.Limit - w.count,
		Reset:     w.end,
		Bucket:    bucket,
	}
}

func (l *inMemoryLimiter) Refund(bucket, key string, n int64) {
	if n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.buckets[bucketKey{bucket: bucket, key: key}]
	if !ok {
		return
	}
	w.count -= n
	if w.count < 0 {
		w.count = 0
	}
}

func (l *inMemoryLimiter) Snapshot(bucket, key string) Decision {
	spec, ok := l.specs[bucket]
	if !ok {
		return Decision{Allow: true, Bucket: bucket}
	}
	t := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, exists := l.buckets[bucketKey{bucket: bucket, key: key}]
	if !exists || !t.Before(w.end) {
		return Decision{
			Allow:     true,
			Limit:     spec.Limit,
			Remaining: spec.Limit,
			Reset:     t.Add(spec.Window),
			Bucket:    bucket,
		}
	}
	return Decision{
		Allow:     w.count < spec.Limit,
		Limit:     spec.Limit,
		Remaining: spec.Limit - w.count,
		Reset:     w.end,
		Bucket:    bucket,
	}
}

// ─── Errors ───────────────────────────────────────────────

var ErrLimitExceeded = errors.New("quota: limit exceeded")

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
