// Package planhint analyses each user prompt against a heuristic
// keyword list and, when the prompt looks like it's about to drive a
// large change, returns a system-note suggesting `EnterPlanMode`
// before the model dives in.
//
// The intent is to turn plan mode from a thing users have to
// remember into a thing the system suggests when it's likely useful.
//
// Defaults intentionally biased toward false negatives. False
// positives (nagging the user to plan a small change) are more
// annoying than false negatives (missing a planning opportunity), so
// the trigger word list is short and case-folded.

package planhint

import (
	"strings"
)

// DefaultKeywords is the seed trigger list. English + Chinese
// variants because biu's user base spans both. Case-insensitive
// match; whole-word check is left to caller (a substring hit on
// "refactor" inside "refactoring" is intentional).
var DefaultKeywords = []string{
	// English: large-change verbs
	"refactor",
	"rewrite",
	"redesign",
	"migrate",
	"overhaul",
	"restructure",
	"port to",
	"port the",
	"replace the",
	"implement the entire",
	"build the entire",
	"big change",
	// Chinese: 重构, 重写, 重新设计, 迁移, 整套, 整体, 大改
	"重构",
	"重写",
	"重新设计",
	"迁移",
	"整套",
	"整体",
	"大改",
}

// Suggestion is the result of Analyse. Empty Note means "no
// suggestion" — caller injects nothing.
type Suggestion struct {
	// Note is the markdown-shaped system message body, or "" when
	// the prompt didn't trip any heuristic.
	Note string
	// MatchedKeyword is what triggered the hint. Empty when Note is.
	MatchedKeyword string
}

// Analyser holds the trigger list. Construct via New so default
// keywords are baked in when the caller passes nothing.
type Analyser struct {
	keywords []string
	enabled  bool
}

// New returns an Analyser. When `keywords` is empty, the package's
// DefaultKeywords are used. `enabled=false` short-circuits Analyse —
// callers wire this from a settings flag so users can opt out.
func New(enabled bool, keywords []string) *Analyser {
	if len(keywords) == 0 {
		keywords = DefaultKeywords
	}
	// Lowercase once so Analyse stays cheap.
	low := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		low = append(low, strings.ToLower(k))
	}
	return &Analyser{keywords: low, enabled: enabled}
}

// Analyse scans `prompt` for any trigger keyword. Returns the first
// match (in keyword-list order, not prompt order) so the suggestion
// stays deterministic across runs.
//
// The note string is markdown-light, designed to drop straight into
// a `<system-note>` block:
//
//   This task looks like it'll touch a lot of code (matched
//   "refactor"). Consider EnterPlanMode first — read + plan, then
//   ExitPlanMode with a concrete plan + allowedPrompts.
func (a *Analyser) Analyse(prompt string) Suggestion {
	if a == nil || !a.enabled || strings.TrimSpace(prompt) == "" {
		return Suggestion{}
	}
	low := strings.ToLower(prompt)
	for _, k := range a.keywords {
		if strings.Contains(low, k) {
			return Suggestion{
				Note: buildNote(k),
				MatchedKeyword: k,
			}
		}
	}
	return Suggestion{}
}

// Enabled exposes the gate so callers can short-circuit upstream.
func (a *Analyser) Enabled() bool {
	return a != nil && a.enabled
}

// buildNote renders the user-visible suggestion. Centralised so the
// engine + REPL surface identical wording.
func buildNote(matched string) string {
	return "This task looks like a large or systemic change " +
		"(matched \"" + matched + "\"). Consider running `EnterPlanMode` " +
		"first to scope the work, then `ExitPlanMode` with a concrete " +
		"plan + allowedPrompts the user can approve in one shot. " +
		"You can also dispatch the built-in Plan sub-agent via " +
		"the `Agent` tool with `subagent_type=\"Plan\"` for deeper " +
		"architectural research."
}
