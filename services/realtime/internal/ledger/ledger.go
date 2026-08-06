// Package ledger keeps a short rolling window of recent events per topic
// to support SSE Last-Event-ID resume.
//
// MVP: in-memory ring buffer per topic, default 1h TTL.
// v1.5: switch to Postgres event_ledger table for multi-replica consistency.
package ledger

import (
	"sync"
	"time"
)

type Event struct {
	ID      string // ULID
	Topic   string
	Kind    string
	Payload []byte // pre-serialized JSON
	TraceID string
	TS      time.Time
}

// Ledger is goroutine-safe.
type Ledger struct {
	mu        sync.RWMutex
	byTopic   map[string]*ring
	retention time.Duration
	maxPer    int
}

func New(retention time.Duration, maxPer int) *Ledger {
	if maxPer <= 0 {
		maxPer = 256
	}
	return &Ledger{
		byTopic:   make(map[string]*ring),
		retention: retention,
		maxPer:    maxPer,
	}
}

// Append stores an event for replay.
func (l *Ledger) Append(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.byTopic[e.Topic]
	if !ok {
		r = newRing(l.maxPer)
		l.byTopic[e.Topic] = r
	}
	r.add(e)
}

// Replay returns events for the given topics with id strictly greater than sinceID.
// Empty sinceID returns nothing (caller must already have a snapshot).
func (l *Ledger) Replay(topics []string, sinceID string) []Event {
	if sinceID == "" {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	cutoff := time.Now().Add(-l.retention)
	var out []Event
	for _, t := range topics {
		r := l.byTopic[t]
		if r == nil {
			continue
		}
		for _, e := range r.snapshot() {
			if e.TS.Before(cutoff) {
				continue
			}
			if e.ID > sinceID {
				out = append(out, e)
			}
		}
	}
	// Sort by ID ascending (ULID is monotonic).
	sortByID(out)
	return out
}

// IsBeyondRetention reports whether sinceID is older than the oldest event
// currently retained for any of the given topics. Returns true only when:
//  1. at least one of the topics has events in the ring, AND
//  2. sinceID is strictly less than the minimum retained event id (i.e.
//     the client is asking to resume from a position that's already aged
//     out — they likely missed events in the gap).
//
// Returns false when:
//   - sinceID is empty (caller must guard upstream),
//   - none of the topics has any retained events (no gap to assert),
//   - sinceID >= min retained id (client is up-to-date or asking for a
//     future id).
//
// v2-6 desync 4009 use-case: after replay, sse handler emits a system
// "desync" frame so the client can drop its cursor and full-refresh.
func (l *Ledger) IsBeyondRetention(topics []string, sinceID string) bool {
	if sinceID == "" {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	cutoff := time.Now().Add(-l.retention)
	for _, t := range topics {
		r := l.byTopic[t]
		if r == nil {
			continue
		}
		var minID string
		for _, e := range r.snapshot() {
			if e.TS.Before(cutoff) {
				continue
			}
			if minID == "" || e.ID < minID {
				minID = e.ID
			}
		}
		if minID != "" && sinceID < minID {
			return true
		}
	}
	return false
}

// GC removes expired events. Call periodically.
func (l *Ledger) GC() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.retention)
	removed := 0
	for topic, r := range l.byTopic {
		removed += r.dropBefore(cutoff)
		if r.size() == 0 {
			delete(l.byTopic, topic)
		}
	}
	return removed
}

func (l *Ledger) Size() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	total := 0
	for _, r := range l.byTopic {
		total += r.size()
	}
	return total
}

// ─── ring buffer ───────────────────────────────────────────

type ring struct {
	buf  []Event
	head int // next write position
	full bool
}

func newRing(cap int) *ring { return &ring{buf: make([]Event, cap)} }

func (r *ring) add(e Event) {
	r.buf[r.head] = e
	r.head = (r.head + 1) % len(r.buf)
	if r.head == 0 {
		r.full = true
	}
}

func (r *ring) snapshot() []Event {
	if !r.full {
		out := make([]Event, r.head)
		copy(out, r.buf[:r.head])
		return out
	}
	out := make([]Event, len(r.buf))
	copy(out, r.buf[r.head:])
	copy(out[len(r.buf)-r.head:], r.buf[:r.head])
	return out
}

func (r *ring) dropBefore(cutoff time.Time) int {
	snap := r.snapshot()
	kept := snap[:0]
	dropped := 0
	for _, e := range snap {
		if e.TS.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if dropped == 0 {
		return 0
	}
	// rebuild buffer
	for i := range r.buf {
		r.buf[i] = Event{}
	}
	r.head = 0
	r.full = false
	for _, e := range kept {
		r.add(e)
	}
	return dropped
}

func (r *ring) size() int {
	if r.full {
		return len(r.buf)
	}
	return r.head
}

func sortByID(s []Event) {
	// insertion sort — N is small (replays bounded by maxPer * topicCount)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].ID > s[j].ID; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
