// channelquota — per-channel RPM / TPM gate that cuts off a channel
// before its upstream key gets pushed over its tier (OpenAI Tier 1 =
// 500 RPM, etc). Without this, a saturated channel returns 429 from
// the real upstream, which costs an RTT, increments failure_count, and
// risks pushing the channel into auto_disabled even though the key is
// fundamentally healthy.
//
// Why a separate limiter (not the shared quota.Limiter SDK):
//   - SDK limiter freezes its specs map at construction; we need to
//     add/remove entries when admin edits rpm_limit or status flips.
//   - Channel quotas are global (all users share one OpenAI key's
//     budget) — simpler key model than user-scoped hub.rpm.{plan}.
//   - Loading state from the channels table is a periodic poll, not
//     hot-path code; keeping it next to router/ matches its only
//     consumer.
//
// Window model: fixed-window, same shape as quota.inMemoryLimiter.
// Reset on first request after window.end. Sliding-window would smooth
// the edge but the gate is strictly best-effort (real upstream still
// enforces; we're just early-降级), so the simpler model is fine.
//
// Soft TPM: TPM is checked AFTER the request completes (token count
// only known then). Future requests on the same channel see the count
// and may get rejected. Mirrors the user-side hub.tpm semantic in
// services/model-relay/internal/api/messages.go.

package router

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// ErrChannelQuotaExhausted is returned by ChannelQuota.AcquireRPM when
// the per-channel budget is full. Resolvers add the channel to Exclude
// and retry.
var ErrChannelQuotaExhausted = errors.New("router: channel quota exhausted")

// ChannelQuota gates per-channel RPM/TPM. Construct one per process.
// Goroutine-safe.
type ChannelQuota struct {
	mu sync.Mutex

	// rpmLimit / tpmLimit per channel. 0 == no limit (caller should
	// just skip the channel from the limiter entirely, but we tolerate
	// 0 to avoid map-thrash on toggle).
	rpmLimit map[uuid.UUID]int64
	tpmLimit map[uuid.UUID]int64

	// fixed window — count + end time. Reset on first request after
	// end. Both maps share the same window length (1 min).
	rpmWin map[uuid.UUID]*qwin
	tpmWin map[uuid.UUID]*qwin

	now func() time.Time
}

type qwin struct {
	count int64
	end   time.Time
}

// NewChannelQuota constructs an empty limiter. SetChannel(...) entries
// are added one at a time; production callers usually wrap a periodic
// ReloadFromList ticker that pulls the active channels and re-syncs.
func NewChannelQuota() *ChannelQuota {
	return &ChannelQuota{
		rpmLimit: map[uuid.UUID]int64{},
		tpmLimit: map[uuid.UUID]int64{},
		rpmWin:   map[uuid.UUID]*qwin{},
		tpmWin:   map[uuid.UUID]*qwin{},
		now:      time.Now,
	}
}

// SetChannel registers (or updates) limits for one channel. Limits at
// 0 are stored as 0 — the gate then becomes a no-op for that field.
func (q *ChannelQuota) SetChannel(ch *registry.Channel) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rpmLimit[ch.ID] = int64(ch.RPMLimit)
	q.tpmLimit[ch.ID] = int64(ch.TPMLimit)
}

// RemoveChannel drops the channel from the limiter. Callers usually
// hit this when a channel transitions to disabled / deleted.
func (q *ChannelQuota) RemoveChannel(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.rpmLimit, id)
	delete(q.tpmLimit, id)
	delete(q.rpmWin, id)
	delete(q.tpmWin, id)
}

// ReloadFromList does a wholesale replace: anything in `channels` is
// kept (limits updated to current values), anything not in the list is
// dropped. Caller's snapshot is the source of truth, so admin edits
// propagate as soon as the next reload tick fires.
//
// Window state is preserved across reloads — important so editing
// rpm_limit doesn't reset the in-flight count.
func (q *ChannelQuota) ReloadFromList(channels []registry.Channel) {
	q.mu.Lock()
	defer q.mu.Unlock()
	keep := make(map[uuid.UUID]struct{}, len(channels))
	for _, ch := range channels {
		keep[ch.ID] = struct{}{}
		q.rpmLimit[ch.ID] = int64(ch.RPMLimit)
		q.tpmLimit[ch.ID] = int64(ch.TPMLimit)
	}
	for id := range q.rpmLimit {
		if _, ok := keep[id]; !ok {
			delete(q.rpmLimit, id)
			delete(q.tpmLimit, id)
			delete(q.rpmWin, id)
			delete(q.tpmWin, id)
		}
	}
}

// AcquireRPM is the pre-flight gate. Reserves 1 RPM for the channel;
// returns ErrChannelQuotaExhausted when over budget. Also peeks TPM —
// if last window already over budget, refuses pre-flight too.
//
// Channels with no spec / rpm_limit==0 always pass.
func (q *ChannelQuota) AcquireRPM(id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	rlim := q.rpmLimit[id]
	tlim := q.tpmLimit[id]
	t := q.now()

	// TPM peek — over budget rejects without committing RPM.
	if tlim > 0 {
		if w := q.tpmWin[id]; w != nil && t.Before(w.end) && w.count >= tlim {
			return ErrChannelQuotaExhausted
		}
	}

	// RPM acquire (n=1).
	if rlim > 0 {
		w := q.rpmWin[id]
		if w == nil || !t.Before(w.end) {
			w = &qwin{end: t.Add(time.Minute)}
			q.rpmWin[id] = w
		}
		if w.count+1 > rlim {
			return ErrChannelQuotaExhausted
		}
		w.count++
	}
	return nil
}

// RecordTokens accumulates tokens used by the just-completed request.
// Soft accounting: never errors, never blocks. Future AcquireRPM on
// the same channel may reject if tlim is now reached.
//
// Negative tokens are ignored (probe failures may report 0).
func (q *ChannelQuota) RecordTokens(id uuid.UUID, n int64) {
	if n <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.tpmLimit[id] == 0 {
		return // no limit configured
	}
	t := q.now()
	w := q.tpmWin[id]
	if w == nil || !t.Before(w.end) {
		w = &qwin{end: t.Add(time.Minute)}
		q.tpmWin[id] = w
	}
	w.count += n
}

// RefundRPM returns one previously-reserved RPM unit. Used when
// AcquireRPM succeeded but the request never reached the upstream
// (e.g. translate_request_failed). Keeps the gate accurate so a buggy
// adaptor doesn't quietly burn budget.
func (q *ChannelQuota) RefundRPM(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if w, ok := q.rpmWin[id]; ok && w.count > 0 {
		w.count--
	}
}

// Stats — for observability / debug.
type Stats struct {
	RPMLimit     int64
	RPMUsed      int64
	RPMResetIn   time.Duration
	TPMLimit     int64
	TPMUsed      int64
	TPMResetIn   time.Duration
}

func (q *ChannelQuota) Stats(id uuid.UUID) Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.now()
	s := Stats{
		RPMLimit: q.rpmLimit[id],
		TPMLimit: q.tpmLimit[id],
	}
	if w := q.rpmWin[id]; w != nil && t.Before(w.end) {
		s.RPMUsed = w.count
		s.RPMResetIn = w.end.Sub(t)
	}
	if w := q.tpmWin[id]; w != nil && t.Before(w.end) {
		s.TPMUsed = w.count
		s.TPMResetIn = w.end.Sub(t)
	}
	return s
}
