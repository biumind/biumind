// Package awaysummary generates the "while you were away" recap —
// a 1-3 sentence reminder shown when the user comes back to the
// REPL after an idle period.
//
// Two pieces of work:
//
//   1. Idle detection — track when the user last interacted; expose
//      a ShouldFire predicate that callers (REPL on every render
//      loop) consult.
//   2. Summary generation — when the predicate fires, run a small
//      LLM call over recent history + the SessionMemory snapshot
//      and return a short paragraph.
//
// Architecture:
//
//   - LLM call goes through the same Summariser interface compact /
//     extractor / agentsummary use, so wiring picks the cheapest
//     model available (Haiku-class) without this package importing
//     a provider.
//   - Recent-history truncation uses a 30-message window —
//     enough to recover the thread, not so much that long sessions
//     produce huge prompts.

package awaysummary

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// DefaultIdleThreshold is how long the REPL must sit untouched
// before the away-summary becomes appropriate. ~5 minutes is the
// sweet spot: below that the user remembers anyway; above 30
// minutes the LLM call rate-limits.
const DefaultIdleThreshold = 5 * time.Minute

// RecentMessageWindow caps how many trailing messages we feed into
// the summary prompt (30 messages).
const RecentMessageWindow = 30

// Summariser is the LLM-call surface — same shape compact /
// sessionmemory / agentsummary use, so wiring supplies one provider
// shared across all four.
type Summariser interface {
	Summarise(ctx context.Context, messages []state.Message, instruction string) (string, error)
}

// Tracker keeps the "last user activity" timestamp + handles the
// fire-once-per-idle-period contract. ShouldFire returns true at
// most once per idle period; subsequent calls return false until
// the user is active again, then idle again.
type Tracker struct {
	threshold time.Duration
	mu        sync.Mutex
	lastActive time.Time
	armed      bool // true when we haven't fired for the current idle period
	now        func() time.Time
}

// NewTracker returns a fresh tracker with the supplied idle
// threshold. Pass 0 for the default. Tests inject a now() function
// to drive deterministic timelines.
func NewTracker(threshold time.Duration) *Tracker {
	if threshold <= 0 {
		threshold = DefaultIdleThreshold
	}
	t := &Tracker{
		threshold: threshold,
		now:       time.Now,
	}
	t.MarkActive()
	return t
}

// SetClock is a test-only override letting fakes drive elapsed
// time deterministically. Not exposed in production paths.
func (t *Tracker) SetClock(fn func() time.Time) {
	if t == nil || fn == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = fn
}

// MarkActive records that the user just interacted (typed a key,
// hit enter, etc). Re-arms the fire-once gate so the next idle
// period can produce another recap.
func (t *Tracker) MarkActive() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastActive = t.now()
	t.armed = true
}

// ShouldFire returns true when (a) idle threshold has been crossed
// AND (b) we haven't already fired for this idle period. Caller
// should immediately schedule a Generate call when true.
func (t *Tracker) ShouldFire() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.armed {
		return false
	}
	if t.now().Sub(t.lastActive) < t.threshold {
		return false
	}
	t.armed = false // disarm; re-armed on next MarkActive
	return true
}

// Idle returns the elapsed time since last MarkActive. Useful for
// status-line "idle 12m" displays without driving a fire.
func (t *Tracker) Idle() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.now().Sub(t.lastActive)
}

// Generate produces the recap text. Returns "" when:
//
//   - history is empty (no thread to recap)
//   - summer is nil (LLM not wired)
//   - context cancelled / model errored
//
// memory is the optional SessionMemory body for broader context;
// pass empty string when no session memory subsystem is wired.
func Generate(ctx context.Context, summer Summariser, history []state.Message, memory string) (string, error) {
	if summer == nil || len(history) == 0 {
		return "", nil
	}
	recent := history
	if len(recent) > RecentMessageWindow {
		recent = recent[len(recent)-RecentMessageWindow:]
	}
	prompt := buildPrompt(memory)
	resp, err := summer.Summarise(ctx, recent, prompt)
	if err != nil {
		// Cancellation is the silent path — caller doesn't want a
		// noisy error when the user resumed before the LLM call
		// completed.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(resp), nil
}

// buildPrompt is the instruction template. Embeds
// the SessionMemory body when supplied so the recap can reach
// beyond the 30-message window for the high-level task description.
func buildPrompt(memory string) string {
	var b strings.Builder
	memory = strings.TrimSpace(memory)
	if memory != "" {
		b.WriteString("Session memory (broader context):\n")
		b.WriteString(memory)
		b.WriteString("\n\n")
	}
	b.WriteString("The user stepped away and is coming back. Write exactly 1-3 short sentences. ")
	b.WriteString("Start by stating the high-level task — what they are building or debugging, not implementation details. ")
	b.WriteString("Next: the concrete next step. Skip status reports and commit recaps.")
	return b.String()
}
