// Small helpers shared across the engine: tool-spec construction,
// hook reason fallback, stderr indirection for tests, and the
// drift-threshold normaliser. All trivial — kept out of engine.go to
// keep that file focused on the public surface.

package engine

import (
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// planDriftThreshold normalises the user-facing 0/-1 sentinels.
//
//	0  → 1 (any drift surfaces — sane default)
//	<0 → math.MaxInt (observe but never surface)
//	else → as-is.
func planDriftThreshold(in int) int {
	if in < 0 {
		return 1<<31 - 1
	}
	if in == 0 {
		return 1
	}
	return in
}

// stderrOrDevNull returns os.Stderr; the indirection keeps the
// usage-logger error path mockable in tests without pulling in
// global state. Today it's just a passthrough.
func stderrOrDevNull() *os.File {
	return os.Stderr
}

// hookReasonOr returns the hook's stated decision reason, falling
// back to the first non-empty stderr / fallback string.
func hookReasonOr(r *hooks.Result, fallback string) string {
	if r == nil {
		return fallback
	}
	if r.Decision.Reason != "" {
		return r.Decision.Reason
	}
	if r.Stderr != "" {
		return r.Stderr
	}
	return fallback
}

// buildToolSpecs converts the registry into the wire shape the LLM
// expects. Description is asked from the tool with nil input —
// every current implementation ignores the input (every signature
// uses `_ map[string]any`), and passing nil saves one empty-map
// allocation per tool per Submit. Dynamic descriptions
// (per-input) land in a future phase when we wire user settings
// into the catalog announcement; that change MUST audit every
// Description impl for nil-tolerance before flipping the contract.
//
// Deferred-tool filtering (P20.51 Phase 2): tools implementing
// Deferrable.ShouldDefer() = true are EXCLUDED from the wire-level
// catalog unless the supplied selection set has unlocked them. nil
// selection set ⇒ keep everything (back-compat with tests / pre-Phase-2
// callers).
func buildToolSpecs(reg ToolRegistry, sel *DeferredSelection) []ToolSpec {
	var tools []Tool
	if sel == nil {
		tools = reg.List()
	} else {
		tools = FilterDeferredCatalog(reg, sel)
	}
	out := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolSpec{
			Name:        t.Name(),
			Description: t.Description(nil),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}
