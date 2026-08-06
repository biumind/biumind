// Context-window helpers for Claude models.
//
// Trimmed to the subset biu actually needs: pick a window for a model
// name, compute % used from a recent usage triple (input + cache_read
// + cache_create).
//
// We don't reach for the Anthropic capabilities API or the 1M-context
// betas here — biu treats every Claude 4.x as 200K by default. The
// `[1m]` suffix opt-in matches Claude Code's syntax so users coming
// from it don't have to relearn.

package cost

import "strings"

// DefaultContextWindow is the fallback for models we don't recognise.
// 200K is the floor for the entire Claude 4.x family today.
const DefaultContextWindow = 200_000

// ExtendedContextWindow is the 1M tier — engaged via the `[1m]`
// suffix on the model ID.
const ExtendedContextWindow = 1_000_000

// ContextWindowForModel returns the input-token cap for the given
// model ID. Case-insensitive; trims the `[1m]` suffix after using it
// as the 1M opt-in trigger.
func ContextWindowForModel(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return DefaultContextWindow
	}
	// `[1m]` suffix forces the 1M tier regardless of model. Consumers
	// passing untrimmed names from settings still hit the right cap.
	if strings.HasSuffix(m, "[1m]") {
		return ExtendedContextWindow
	}
	// Future: per-model overrides go here. Today the entire Claude
	// 4.x family is 200K so a single fall-through suffices.
	return DefaultContextWindow
}

// ContextUsage describes the most-recent turn's input-side token
// consumption. All three fields count toward the input window — the
// Anthropic API charges separately for cache reads / writes, but the
// context window is a single budget across all of them.
type ContextUsage struct {
	InputTokens       int
	CacheReadTokens   int
	CacheCreateTokens int
}

// Total is the sum of all three input-side counters — the number we
// compare against ContextWindowForModel.
func (u ContextUsage) Total() int {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreateTokens
}

// ContextPercentages reports how much of the window the latest turn
// consumed. Used for status-line rendering and auto-compact triggers.
type ContextPercentages struct {
	Used      int // 0–100
	Remaining int // 0–100, == 100 - Used
}

// ContextPercent computes the used / remaining pair from a usage
// triple + a model. Clamped to [0, 100] so a momentary tracker
// over-count (e.g. stale cache read accounting after compact) can't
// produce >100% which would confuse status-line renderers.
//
// Returns the zero value when usage.Total() is zero — the caller
// should render "—" rather than 0% to distinguish "not yet measured"
// from "genuinely empty".
func ContextPercent(usage ContextUsage, model string) ContextPercentages {
	if usage.Total() == 0 {
		return ContextPercentages{}
	}
	window := ContextWindowForModel(model)
	if window <= 0 {
		return ContextPercentages{}
	}
	pct := (usage.Total() * 100) / window
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return ContextPercentages{Used: pct, Remaining: 100 - pct}
}
