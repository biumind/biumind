// Effective context window + tiered thresholds.
//
// The intuition: a model's nominal context window (e.g. 200K for
// Opus) is bigger than what you can actually USE because the model
// also needs output budget, and compact summaries themselves take
// up to 20K of output. Tiered buffers carve the window into named
// regions so the engine / status line / REPL can give the user
// calibrated signals long before things fail.
//
// Tier layout (subtracted from the model's nominal window):
//
//	max_output_for_summary  → effectiveContextWindow
//	  - AUTOCOMPACT_BUFFER  → autoCompactThreshold (start summarising here)
//	  - WARNING_BUFFER      → warningThreshold     (UI urgency tier)
//	  - ERROR_BUFFER        → errorThreshold       (next call will likely fail)
//	  - MANUAL_BUFFER       → blockingLimit        (refuse new prompts)
//
// All buffers are ABSOLUTE token counts (not percentages) because
// the model's max_output is fixed and the buffers protect against
// specific failure modes — a percentage of the window doesn't
// shrink/grow proportionally with those failures.
//
// Environment overrides keep the conventional env-var names so
// users porting from Claude Code keep their muscle memory:
//
//	CLAUDE_CODE_AUTO_COMPACT_WINDOW   — clamp the nominal window
//	CLAUDE_AUTOCOMPACT_PCT_OVERRIDE   — set autocompact threshold
//	                                     to N% of effective window
//	CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE — override the blocking limit
//	DISABLE_COMPACT                   — auto-compact off (manual only)

package compact

import (
	"os"
	"strconv"
	"strings"
)

// MaxOutputTokensForSummary caps how much output budget we reserve
// when computing effectiveContextWindow. Empirically the p99.99 of
// production summary outputs is 17,387 tokens; 20K leaves a small
// margin without giving up too much usable window.
const MaxOutputTokensForSummary = 20_000

// Buffer constants — all in absolute tokens.
const (
	// AutoCompactBufferTokens — gap between autocompact threshold
	// and the effective window. Triggers compact this far from the
	// hard limit so the SUMMARISE call (which itself adds tokens)
	// has room to land.
	AutoCompactBufferTokens = 13_000

	// WarningThresholdBufferTokens — how far before the autocompact
	// threshold the UI starts showing "context filling" warnings.
	// Same as ERROR; the two names track distinct UI tiers, though
	// biu currently maps both to compact warnings (PP12).
	WarningThresholdBufferTokens = 20_000
	ErrorThresholdBufferTokens   = 20_000

	// ManualCompactBufferTokens — gap between effective window and
	// the blocking limit. New prompts are rejected when usage
	// crosses (effectiveWindow - this), forcing the user to
	// /compact or /clear before continuing.
	ManualCompactBufferTokens = 3_000
)

// MaxConsecutiveAutoCompactFailures stops the engine from looping
// on a broken provider. After this many compact attempts in a row
// fail, auto-compact disables until the next successful turn.
const MaxConsecutiveAutoCompactFailures = 3

// ModelContextWindow returns the model's nominal context window
// in input tokens. The map is conservative — when a model isn't
// recognised we fall back to 200_000, the floor across the modern
// Claude line, so unknown models still get reasonable thresholds
// instead of accidentally tripping zero-budget compact loops.
//
// IDs match Anthropic's `<vendor>-<family>-<rev>` naming. Aliases
// without the date suffix (e.g. "claude-opus-4-7") map to the same
// window as the dated variant.
func ModelContextWindow(model string) int {
	id := strings.ToLower(model)
	switch {
	case strings.Contains(id, "opus-4"):
		return 200_000
	case strings.Contains(id, "sonnet-4-6"),
		strings.Contains(id, "sonnet-4-5"):
		// Sonnet 4.5/4.6 ship with the 1M extended-context beta.
		// We default to the standard 200K because the beta requires
		// an opt-in header the engine doesn't always set; SDK callers
		// pass --window 1_000_000 to override.
		return 200_000
	case strings.Contains(id, "sonnet-4"):
		return 200_000
	case strings.Contains(id, "haiku-4"):
		return 200_000
	case strings.Contains(id, "sonnet-3-7"),
		strings.Contains(id, "sonnet-3-5"),
		strings.Contains(id, "haiku-3-5"):
		return 200_000
	default:
		return 200_000
	}
}

// EffectiveContextWindow returns the usable input-token budget after
// reserving room for the summary output. Reads
// CLAUDE_CODE_AUTO_COMPACT_WINDOW to let users clamp the window for
// testing or to simulate older models.
func EffectiveContextWindow(model string) int {
	window := ModelContextWindow(model)
	if v := os.Getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed < window {
			window = parsed
		}
	}
	reserved := MaxOutputTokensForSummary
	// If a future per-model max-output table arrives, reservation
	// becomes `min(MaxOutputTokensForSummary, modelMaxOutput)`. For
	// now every modern Claude max_output is well above 20K so the
	// flat constant is correct.
	return window - reserved
}

// AutoCompactThreshold is the input-token usage at which auto
// compact kicks in. Defaults to (effectiveWindow - AutoCompactBuffer);
// override with CLAUDE_AUTOCOMPACT_PCT_OVERRIDE to set N% of
// effectiveWindow (clamped to never exceed the buffer-based default,
// so the override only TIGHTENS the trigger).
func AutoCompactThreshold(model string) int {
	eff := EffectiveContextWindow(model)
	bufferBased := eff - AutoCompactBufferTokens

	if v := os.Getenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"); v != "" {
		if pct, err := strconv.ParseFloat(v, 64); err == nil && pct > 0 && pct <= 100 {
			pctBased := int(float64(eff) * (pct / 100))
			if pctBased < bufferBased {
				return pctBased
			}
		}
	}
	return bufferBased
}

// IsAutoCompactEnabled reports whether the auto-compact path is
// active. False when DISABLE_COMPACT=1 — manual /compact still
// works, but the engine won't trigger compact on its own.
func IsAutoCompactEnabled() bool {
	return !envTruthy("DISABLE_COMPACT")
}

// envTruthy reports whether the env var holds a truthy value:
// 1 / true / yes / on (case-insensitive). Empty string is not
// truthy.
func envTruthy(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// TokenWarningState is the calibrated assessment of how close the
// session is to its limits. UI layers consume this to render the
// status line / system note tier.
type TokenWarningState struct {
	// PercentLeft is 0–100. 0 means autoCompact threshold reached;
	// negative usage clamps to 0.
	PercentLeft int

	// AboveWarningThreshold — usage crossed (autoCompactThreshold -
	// WarningThresholdBufferTokens). UI shows "context filling".
	AboveWarningThreshold bool

	// AboveErrorThreshold — usage crossed the urgency tier; the next
	// LLM call has a real chance of returning prompt-too-long.
	AboveErrorThreshold bool

	// AboveAutoCompactThreshold — auto compact will fire on the next
	// turn (or has already fired this turn).
	AboveAutoCompactThreshold bool

	// AtBlockingLimit — usage past the manual-compact buffer; new
	// prompts should be refused until the user runs /compact or
	// /clear. Last line of defence before the model rejects.
	AtBlockingLimit bool
}

// CalculateWarning maps usage + model to the tiered warning state.
// Pass model="" for the default 200K window.
func CalculateWarning(tokenUsage int, model string) TokenWarningState {
	autoThreshold := AutoCompactThreshold(model)
	threshold := autoThreshold
	if !IsAutoCompactEnabled() {
		threshold = EffectiveContextWindow(model)
	}

	percentLeft := 0
	if threshold > 0 {
		raw := (threshold - tokenUsage) * 100 / threshold
		if raw > 0 {
			percentLeft = raw
		}
	}

	warningT := threshold - WarningThresholdBufferTokens
	errorT := threshold - ErrorThresholdBufferTokens

	effective := EffectiveContextWindow(model)
	defaultBlocking := effective - ManualCompactBufferTokens
	blocking := defaultBlocking
	if v := os.Getenv("CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			blocking = parsed
		}
	}

	return TokenWarningState{
		PercentLeft:               percentLeft,
		AboveWarningThreshold:     tokenUsage >= warningT,
		AboveErrorThreshold:       tokenUsage >= errorT,
		AboveAutoCompactThreshold: IsAutoCompactEnabled() && tokenUsage >= autoThreshold,
		AtBlockingLimit:           tokenUsage >= blocking,
	}
}
