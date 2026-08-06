// API-side context management — emits the Anthropic
// `context_management` directive so the SERVER auto-clears old tool
// uses / thinking blocks when input crosses a threshold.
//
// This is NOT a client-side compact pass; it's a request-body field
// the API consumes. The advantage: the server clears in the same
// turn as the request, so we never see a "prompt too long" error
// for cases the directive can handle. The cost: behaviour is
// model/version-specific and may change without notice — env vars
// gate every strategy so operators can disable individual paths
// when the API contract drifts.
//
// Two strategies, both opt-in via env:
//
//	clear_tool_uses_20250919  — drop old tool USE blocks (their
//	                             RESULTS stay). Useful when the
//	                             agent has long-running tools whose
//	                             input args are big (file
//	                             concatenations, big WebFetch URLs).
//	clear_thinking_20251015    — drop or shrink extended-thinking
//	                             blocks. Useful for thinking models
//	                             where prior-turn reasoning bloats
//	                             prefix.
//
// The directive is opt-in for two reasons:
//
//  1. Older Anthropic SDK versions reject unknown request fields,
//     so emitting unconditionally breaks model-relay callers running older
//     gateway code.
//  2. Cost / behaviour observability — operators want a clear
//     switch when debugging "why did the model forget".
package compact

import (
	"os"
	"strconv"
	"strings"
)

// API_DEFAULT_MAX_INPUT_TOKENS — the input-token line where the
// server starts clearing. Aligned with the client-side warning
// threshold so the user-visible cutoff feels consistent across
// the API and local pipelines.
const APIDefaultMaxInputTokens = 180_000

// API_DEFAULT_TARGET_INPUT_TOKENS — the goal post-clear input
// size. Server clears at least (max - target) tokens worth of
// matching blocks.
const APIDefaultTargetInputTokens = 40_000

// ToolsClearableResults — tool names whose RESULTS the server may
// clear (the directive's `clear_tool_inputs` field accepts a list
// of tool names). Read-only utility tools whose past output the
// model rarely needs verbatim.
var ToolsClearableResults = []string{
	"Bash", "BashOutput", "KillBash",
	"Glob", "Grep",
	"Read",
	"WebFetch", "WebSearch",
}

// ToolsClearableUses — tool names whose USE blocks the server may
// clear. Edit-shaped tools where the *result* (the new file state)
// is what matters; the input args (old/new strings) become noise
// once the file is committed to disk.
var ToolsClearableUses = []string{
	"Edit", "Write", "MultiEdit", "NotebookEdit",
}

// APIContextEditStrategy is one entry in the `context_management.edits`
// array. Two variants today; expressed as a flat struct with
// optional fields rather than a sum type because the API serialises
// it as JSON and Go's interface{} round-trip is more brittle.
type APIContextEditStrategy struct {
	// Type — discriminator. Known values:
	//   clear_tool_uses_20250919
	//   clear_thinking_20251015
	Type string `json:"type"`

	// Trigger — when the strategy fires. Only valid for
	// clear_tool_uses_20250919; key is "type":"input_tokens".
	Trigger *APIThreshold `json:"trigger,omitempty"`

	// Keep — what to keep when clearing. For clear_thinking, may be
	// the string "all" or a structured threshold; we model both
	// via interface{} since the API actually accepts either shape.
	Keep any `json:"keep,omitempty"`

	// ClearAtLeast — server tries to drop at least this many tokens
	// per clear pass. Only for clear_tool_uses_20250919.
	ClearAtLeast *APIThreshold `json:"clear_at_least,omitempty"`

	// ClearToolInputs — tool names (or true / false) whose inputs
	// the server may clear. Only for clear_tool_uses_20250919.
	ClearToolInputs []string `json:"clear_tool_inputs,omitempty"`

	// ExcludeTools — tool names to NEVER clear. Mutually exclusive
	// with ClearToolInputs in spirit; the API allows both for
	// power users.
	ExcludeTools []string `json:"exclude_tools,omitempty"`
}

// APIThreshold is the {"type": "input_tokens"|"thinking_turns",
// "value": N} shape the API uses for trigger / keep / clear_at_least.
type APIThreshold struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
}

// APIContextManagementConfig wraps the strategies for the
// `context_management` request field. Returned as a typed struct;
// adapters that target Anthropic JSON-marshal directly into the
// request body. nil means "don't emit the field" — every existing
// caller behaves identically.
type APIContextManagementConfig struct {
	Edits []APIContextEditStrategy `json:"edits"`
}

// APIContextOptions captures the per-turn context the strategy
// chooser needs.
type APIContextOptions struct {
	// HasThinking — whether the assistant turn had any thinking
	// blocks. Only when true is the clear_thinking strategy
	// considered.
	HasThinking bool

	// IsRedactThinkingActive — when the user enabled
	// redact-thinking, the API already emits placeholder blocks
	// with no content; clearing them is pointless and skipped.
	IsRedactThinkingActive bool

	// ClearAllThinking — set when the gap-since-last-turn is so
	// long the cache is dead anyway; tells the strategy to keep
	// only the last thinking turn instead of the model-policy
	// default.
	ClearAllThinking bool
}

// BuildAPIContextManagement composes the per-request config based on
// env-gated strategies and the supplied options. Returns nil when no
// strategy is enabled — call sites pass the result directly to the
// provider, which JSON-marshals nil away.
//
// Env vars:
//
//	USE_API_CLEAR_TOOL_RESULTS=1   enable clear_tool_uses (results)
//	USE_API_CLEAR_TOOL_USES=1      enable clear_tool_uses (uses)
//	API_MAX_INPUT_TOKENS=N         override threshold
//	API_TARGET_INPUT_TOKENS=N      override target
func BuildAPIContextManagement(opt APIContextOptions) *APIContextManagementConfig {
	var edits []APIContextEditStrategy

	// Thinking strategy: kept distinct from the tool strategies so
	// thinking-only sessions can opt in without the tool clearing
	// machinery, and vice versa.
	if opt.HasThinking && !opt.IsRedactThinkingActive {
		keep := any("all")
		if opt.ClearAllThinking {
			// API requires value ≥ 1; "1" preserves the most recent
			// thinking turn so a follow-up question still has at
			// least one prior reasoning block to anchor against.
			keep = APIThreshold{Type: "thinking_turns", Value: 1}
		}
		edits = append(edits, APIContextEditStrategy{
			Type: "clear_thinking_20251015",
			Keep: keep,
		})
	}

	// Tool strategies — gated by their own env flags. Both share the
	// same trigger / target tokens but emit different scope blocks.
	maxIn := apiEnvInt("API_MAX_INPUT_TOKENS", APIDefaultMaxInputTokens)
	target := apiEnvInt("API_TARGET_INPUT_TOKENS", APIDefaultTargetInputTokens)

	if envTruthy("USE_API_CLEAR_TOOL_RESULTS") {
		edits = append(edits, APIContextEditStrategy{
			Type:    "clear_tool_uses_20250919",
			Trigger: &APIThreshold{Type: "input_tokens", Value: maxIn},
			ClearAtLeast: &APIThreshold{
				Type: "input_tokens", Value: maxIn - target,
			},
			ClearToolInputs: ToolsClearableResults,
		})
	}

	if envTruthy("USE_API_CLEAR_TOOL_USES") {
		edits = append(edits, APIContextEditStrategy{
			Type:    "clear_tool_uses_20250919",
			Trigger: &APIThreshold{Type: "input_tokens", Value: maxIn},
			ClearAtLeast: &APIThreshold{
				Type: "input_tokens", Value: maxIn - target,
			},
			ExcludeTools: ToolsClearableUses,
		})
	}

	if len(edits) == 0 {
		return nil
	}
	return &APIContextManagementConfig{Edits: edits}
}

// apiEnvInt parses an env-var integer with a fallback default.
// Trims whitespace; rejects negative / zero values.
func apiEnvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
