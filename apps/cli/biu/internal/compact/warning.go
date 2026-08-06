// Compact warning state + level enum.
//
// Compact fires automatically when InputTokens crosses the
// ThresholdRatio (default 0.7). Without warning, the user has no
// signal that a compact is approaching — they just see a sudden
// pause + summary mid-session. This file adds two pre-fire
// thresholds and tracks which have already been emitted so the
// engine doesn't spam the same warning every turn.
//
// Two levels:
//
//	LevelInfo    fires once when usage crosses LevelInfoRatio
//	             (≈ 0.50 by default — early heads-up).
//	LevelUrgent  fires once when usage crosses LevelUrgentRatio
//	             (≈ 0.85 by default — compact is imminent).
//
// Both default ratios sit BELOW the autoCompact threshold (0.7) on
// purpose: LevelInfo at 0.50 gives a long lead time, LevelUrgent at
// 0.85 ONLY fires when the user has overridden the default
// ThresholdRatio higher (e.g. 0.9). With the default 0.7 firing
// threshold, LevelUrgent is effectively unreachable — we keep it for
// power users who push the threshold up to extract more context.
//
// State is reset by Reset() after a successful compact, so the
// next cycle gets fresh warnings.
//
// This package stays transport-agnostic: WarningState produces a
// (Level, fired) decision; the engine wraps it into a stream event
// (engine.CompactWarningEvent) so the REPL can render a status note.

package compact

import "sync"

// WarningLevel discriminates the two pre-compact severity tiers.
type WarningLevel int

const (
	// LevelInfo — early heads-up. The conversation has crossed
	// LevelInfoRatio; user can /compact manually now to control the
	// summary or just keep going.
	LevelInfo WarningLevel = iota
	// LevelUrgent — compact is about to fire automatically. Used
	// when the firing threshold is configured higher than the
	// default; informs the user the next turn might compact.
	LevelUrgent
)

// String returns a human-readable level name for telemetry / log
// rendering.
func (l WarningLevel) String() string {
	switch l {
	case LevelInfo:
		return "info"
	case LevelUrgent:
		return "urgent"
	default:
		return "unknown"
	}
}

// Default warning thresholds. Configurable via WarningOptions when
// the engine instantiates a WarningState.
const (
	DefaultLevelInfoRatio   = 0.50
	DefaultLevelUrgentRatio = 0.85
)

// WarningOptions tunes the per-level thresholds. Zero values fall
// back to the Default* constants. MaxTokens is the budget used to
// compute the absolute thresholds; should match the Auto.opt.MaxTokens
// the engine uses so info / firing triggers stay in lockstep.
type WarningOptions struct {
	MaxTokens        int
	LevelInfoRatio   float64
	LevelUrgentRatio float64
}

// WarningState tracks which warning levels have already fired for
// the current compact cycle. Concurrent-safe so engine code can
// query from any goroutine.
//
// Watermark semantics: the cost.Tracker counts tokens monotonically
// across a session — after a compact reduces the active context,
// the tracker's InputTokens keeps growing because cost shouldn't
// "forget" past spend. To compute *current cycle* usage, the state
// records a watermark (Reset() snapshots the latest seen total) and
// Maybe() compares (used - watermark) to the threshold. Without
// this, every turn after the first compact would re-fire warnings
// on the cumulative session count.
type WarningState struct {
	opt       WarningOptions
	mu        sync.Mutex
	seen      map[WarningLevel]bool
	watermark int // tokens already accounted for in prior cycles
}

// NewWarningState returns a fresh state. Defaults are applied to
// the Options.
func NewWarningState(opt WarningOptions) *WarningState {
	if opt.LevelInfoRatio <= 0 {
		opt.LevelInfoRatio = DefaultLevelInfoRatio
	}
	if opt.LevelUrgentRatio <= 0 {
		opt.LevelUrgentRatio = DefaultLevelUrgentRatio
	}
	return &WarningState{
		opt:  opt,
		seen: map[WarningLevel]bool{},
	}
}

// Maybe reports the highest unfired warning level for the supplied
// token usage, or (0, false) when nothing should emit. Records the
// emission so a second call at the same or lower level returns
// (0, false) — warnings are one-shot per cycle.
//
// Crossing LevelUrgent implicitly marks LevelInfo as seen too —
// jumping past Info means the user already knows usage is high.
func (w *WarningState) Maybe(usedTokens int) (WarningLevel, bool) {
	if w == nil || w.opt.MaxTokens == 0 {
		return 0, false
	}
	w.mu.Lock()
	cycleTokens := usedTokens - w.watermark
	if cycleTokens < 0 {
		// Watermark drifted ahead of cumulative usage (cost tracker
		// reset, provider race, test fixture). Adopt usedTokens as
		// the new effective base — equivalent to "this is a fresh
		// cycle starting from now".
		w.watermark = 0
		cycleTokens = usedTokens
	}
	w.mu.Unlock()
	ratio := float64(cycleTokens) / float64(w.opt.MaxTokens)

	var level WarningLevel
	switch {
	case ratio >= w.opt.LevelUrgentRatio:
		level = LevelUrgent
	case ratio >= w.opt.LevelInfoRatio:
		level = LevelInfo
	default:
		return 0, false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[level] {
		return 0, false
	}
	// Mark the chosen level + every level below as seen, so a
	// subsequent call at LevelInfo after we've already fired
	// LevelUrgent doesn't re-emit.
	for l := LevelInfo; l <= level; l++ {
		w.seen[l] = true
	}
	return level, true
}

// Reset clears all seen levels and snapshots the cumulative token
// count as the new watermark. Engine calls this after a successful
// compact so the next cycle's threshold check is relative to
// post-compact usage rather than session-cumulative.
//
// usedTokens should be the cost.Tracker's InputTokens at the
// moment of compact completion — same value Maybe() will see
// going forward, so the first turn after compact starts the new
// cycle at zero.
func (w *WarningState) Reset(usedTokens int) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = map[WarningLevel]bool{}
	w.watermark = usedTokens
}

// MaxTokens returns the configured budget — engine uses this to
// build the absolute "X / Y tokens" line on the warning event.
func (w *WarningState) MaxTokens() int {
	if w == nil {
		return 0
	}
	return w.opt.MaxTokens
}
