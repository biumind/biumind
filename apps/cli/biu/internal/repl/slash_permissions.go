// /permissions slash — show the active permission rules + the mode.
//
// Surfaces three things:
//
//   * Current permission mode (default / acceptEdits / plan / bypass)
//   * Allow / Deny / Ask rules grouped by source (user / project /
//     local settings.json), in evaluation order
//   * Quick-edit hint pointing at the settings files so users can
//     persist changes without leaving the REPL
//
// Read-only — actually mutating rules requires editing settings.json
// directly (then `/reload` picks them up). The slash is a window
// into state, not a CLI for it; the layered settings model has too
// many edge cases to round-trip via a one-line slash command.

package repl

import (
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

// handlePermissions renders the active permission state. parts is
// the slash + args slice; only `parts[0]` is honoured today (no
// sub-commands), but the signature stays array-shaped for parity
// with sibling slash handlers.
func (m model) handlePermissions(parts []string) string {
	if m.engine == nil {
		return "/permissions: permission context not wired in this REPL " +
			"(headless / SDK mode)"
	}
	pCtx := m.engine.Permissions()
	if pCtx == nil {
		return "/permissions: engine has no permission context"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "permission mode: %s\n", pCtx.Mode())
	if explain := modeExplanation(pCtx.Mode()); explain != "" {
		fmt.Fprintf(&b, "  (%s)\n", explain)
	}

	// Working directories block — lists originalCwd first then every
	// additional dir tagged with its source.
	if extras := pCtx.AdditionalDirectories(); len(extras) > 0 || pCtx.OriginalCwd() != "" {
		fmt.Fprintf(&b, "\nworking directories (%d):\n", 1+len(extras))
		if cwd := pCtx.OriginalCwd(); cwd != "" {
			fmt.Fprintf(&b, "  [cwd]            %s\n", cwd)
		}
		for _, d := range extras {
			fmt.Fprintf(&b, "  [%-14s] %s\n", string(d.Source), d.Path)
		}
	}

	for _, behavior := range []permissions.Behavior{
		permissions.BehaviorAllow,
		permissions.BehaviorDeny,
		permissions.BehaviorAsk,
	} {
		rules := pCtx.AllRules(behavior)
		if len(rules) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%d rule%s):\n",
			strings.ToUpper(string(behavior)),
			len(rules), pluralS(len(rules)))
		// Group by source for readability — same source wins +
		// keeps user / project / local visually distinct.
		grouped := groupRulesBySource(rules)
		for _, src := range sourcesInDisplayOrder(grouped) {
			fmt.Fprintf(&b, "  [%s]\n", src)
			for _, r := range grouped[src] {
				fmt.Fprintf(&b, "    %s\n", r.Value.String())
			}
		}
	}

	b.WriteString("\nedit ~/.biumind/settings.json (user) or " +
		"<project>/.biumind/settings.json (project) — " +
		"`/reload` re-reads after edits.\n")
	b.WriteString("workspace dirs: `/add-dir <path>` (session) · `/add-dir <path> --remember` (settings.local) · `/remove-dir <path>`")
	return b.String()
}

// modeExplanation returns a one-line description of what each mode
// means. Helps users who haven't read the docs understand why a
// tool was / wasn't gated.
func modeExplanation(mode permissions.Mode) string {
	switch mode {
	case permissions.ModeDefault:
		return "ask before destructive ops; allow read / safe writes"
	case permissions.ModeAcceptEdits:
		return "auto-approve file edits; ask only for shell / network"
	case permissions.ModePlan:
		return "plan-only; refuse every mutating tool until ExitPlanMode"
	case permissions.ModeBypass:
		return "ALL tools approved without ask — DANGEROUS, use with care"
	}
	return ""
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// groupRulesBySource bins rules by their Source. Returned map
// preserves slice order within each source (the AllRules contract).
func groupRulesBySource(rules []permissions.Rule) map[permissions.Source][]permissions.Rule {
	out := map[permissions.Source][]permissions.Rule{}
	for _, r := range rules {
		out[r.Source] = append(out[r.Source], r)
	}
	return out
}

// sourcesInDisplayOrder returns sources in the canonical display
// order (user → project → local → CLI overrides → policy → other).
func sourcesInDisplayOrder(grouped map[permissions.Source][]permissions.Rule) []permissions.Source {
	preferred := []permissions.Source{
		permissions.SrcUserSettings,
		permissions.SrcProjectSettings,
		permissions.SrcLocalSettings,
		permissions.SrcCLIArg,
	}
	out := make([]permissions.Source, 0, len(grouped))
	seen := map[permissions.Source]bool{}
	for _, s := range preferred {
		if _, ok := grouped[s]; ok {
			out = append(out, s)
			seen[s] = true
		}
	}
	// Catch any sources we haven't enumerated (forward-compat for
	// new layers added later).
	for s := range grouped {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}
