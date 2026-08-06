// Time-based microcompact — clear old tool results when the gap
// since the last assistant response is so long the server-side
// prompt cache has certainly expired.
//
// The intuition: Anthropic's prompt cache TTL is 1h. After 60+
// minutes idle, the next request will rebuild the entire prefix
// from scratch anyway. Pre-emptively clearing old tool result
// content shrinks the request body — same model outputs, smaller
// bill, lower latency on the cold-start request.
//
// Trigger: per-turn check of (now - lastAssistantTimestamp). When
// gap > GapThresholdMinutes AND TimeBasedMCConfig.Enabled, replace
// old `tool_result` content blocks (everything except the most
// recent KeepRecent results) with a sentinel. Tool IDs and tool_use
// pairing stay intact — only the content payload shrinks.
//
// Configuration sourced from env vars rather than a feature-flag
// service; biu doesn't ship one, so env is the portable substitute.

package compact

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// TimeBasedMCClearedMessage — the sentinel placed in cleared tool
// results. The model sees this string in place of the original
// content and treats it as already-known history. The literal is
// kept stable so shared transcripts stay readable across versions.
const TimeBasedMCClearedMessage = "[Old tool result content cleared]"

// TimeBasedMCConfig tunes the time-based clearing pass. Default
// disabled — operators opt in via env or by passing a non-zero
// config to MicroOptions.TimeBased.
type TimeBasedMCConfig struct {
	// Enabled — master switch. False makes the time-based pass a
	// no-op regardless of other fields.
	Enabled bool

	// GapThresholdMinutes — clear when (now - lastAssistantTime)
	// crosses this many minutes. The 60-minute default guarantees
	// the server's 1h cache is definitely expired.
	GapThresholdMinutes int

	// KeepRecent — keep the last N tool results untouched. Floor
	// at 1 in the implementation: clearing every tool result leaves
	// the model with no working context, which is worse than the
	// extra rebuild cost.
	KeepRecent int
}

// DefaultTimeBasedMC returns the conservative defaults: disabled,
// 60min gap, keep last 5. Operators flip Enabled=true via env or
// configuration to opt in.
func DefaultTimeBasedMC() TimeBasedMCConfig {
	return TimeBasedMCConfig{
		Enabled:             false,
		GapThresholdMinutes: 60,
		KeepRecent:          5,
	}
}

// LoadTimeBasedMCFromEnv reads BIU_TIME_BASED_MC_* env vars and
// returns the resulting config (with sensible fallbacks for
// missing / malformed values).
//
//	BIU_TIME_BASED_MC=1                  → enable
//	BIU_TIME_BASED_MC_GAP_MIN=30         → custom gap threshold
//	BIU_TIME_BASED_MC_KEEP_RECENT=3      → custom keep count
//
// Why env: portable substitute for a feature-flag service without
// dragging an analytics dep into biu. Tests override via t.Setenv.
func LoadTimeBasedMCFromEnv() TimeBasedMCConfig {
	cfg := DefaultTimeBasedMC()
	if envTruthy("BIU_TIME_BASED_MC") {
		cfg.Enabled = true
	}
	if v := os.Getenv("BIU_TIME_BASED_MC_GAP_MIN"); v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && parsed > 0 {
			cfg.GapThresholdMinutes = parsed
		}
	}
	if v := os.Getenv("BIU_TIME_BASED_MC_KEEP_RECENT"); v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && parsed >= 1 {
			cfg.KeepRecent = parsed
		}
	}
	return cfg
}

// TimeBasedTrigger is the result of evaluating whether time-based
// clearing should fire. nil means "skip"; non-nil carries the
// computed gap so callers can log / surface why they chose to
// clear.
type TimeBasedTrigger struct {
	GapMinutes float64
	Config     TimeBasedMCConfig
}

// EvaluateTimeBasedTrigger reports whether the current message
// history + clock pass the threshold for time-based clearing.
// Returns nil when:
//
//   - config disabled
//   - no assistant message in history (fresh session)
//   - gap < threshold
//
// The `now` parameter is injected so tests can drive deterministic
// gaps. Callers in production pass time.Now().
func EvaluateTimeBasedTrigger(messages []state.Message, cfg TimeBasedMCConfig, now time.Time) *TimeBasedTrigger {
	if !cfg.Enabled {
		return nil
	}
	last := lastAssistantTime(messages)
	if last.IsZero() {
		return nil
	}
	gapMins := now.Sub(last).Minutes()
	if gapMins < float64(cfg.GapThresholdMinutes) {
		return nil
	}
	return &TimeBasedTrigger{GapMinutes: gapMins, Config: cfg}
}

// lastAssistantTime returns the CreatedAt of the most recent
// assistant message, or zero time when none exists.
func lastAssistantTime(messages []state.Message) time.Time {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == state.RoleAssistant && !messages[i].CreatedAt.IsZero() {
			return messages[i].CreatedAt
		}
	}
	return time.Time{}
}

// ApplyTimeBasedMC clears old tool_result content in messages
// when the trigger fires. Mutates the message slice in place
// (matches micro.go's Apply convention). Returns the count of
// tool results that were cleared.
//
// Algorithm:
//
//  1. Collect every tool_use_id that has a paired tool_result block.
//  2. Mark the most-recent KeepRecent ids as "keep".
//  3. For every tool_result block whose id is NOT in keep, replace
//     its text content with TimeBasedMCClearedMessage. Other content
//     types pass through unchanged.
//
// Idempotent — already-cleared blocks are skipped, preventing
// double-counting on consecutive calls.
func ApplyTimeBasedMC(messages []state.Message, trigger *TimeBasedTrigger) int {
	if trigger == nil {
		return 0
	}
	cfg := trigger.Config

	// Pass 1: collect every tool_use_id that has a tool_result.
	var toolIDs []string
	seen := map[string]bool{}
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type != state.ContentToolResult {
				continue
			}
			if block.ToolUseID == "" || seen[block.ToolUseID] {
				continue
			}
			seen[block.ToolUseID] = true
			toolIDs = append(toolIDs, block.ToolUseID)
		}
	}
	if len(toolIDs) == 0 {
		return 0
	}

	keep := cfg.KeepRecent
	if keep < 1 {
		keep = 1
	}
	if keep > len(toolIDs) {
		// Nothing to clear — every tool result is in the keep window.
		return 0
	}

	// "keep" is the trailing window; everything before is clearable.
	clearSet := map[string]bool{}
	cutoff := len(toolIDs) - keep
	for i := 0; i < cutoff; i++ {
		clearSet[toolIDs[i]] = true
	}

	// Pass 2: rewrite content blocks in place.
	cleared := 0
	for mi := range messages {
		for bi := range messages[mi].Content {
			b := &messages[mi].Content[bi]
			if b.Type != state.ContentToolResult {
				continue
			}
			if !clearSet[b.ToolUseID] {
				continue
			}
			if b.Text == TimeBasedMCClearedMessage {
				continue // already cleared on a prior pass
			}
			b.Text = TimeBasedMCClearedMessage
			cleared++
		}
	}
	return cleared
}
