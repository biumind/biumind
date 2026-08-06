// hookify handler — extracts "don't do X" rules from .local.md /
// CLAUDE.md / BIUMIND.md in the project + user home, then surfaces
// them at two events.
//
// Two event handlers:
//
//	UserPromptSubmit  — append the rule list to the prompt as
//	                    additionalContext so the model sees the
//	                    constraints at the start of each turn.
//	PreToolUse        — when the tool call's input matches one of
//	                    the rule patterns, block the call with the
//	                    rule text as the reason. The model can
//	                    then re-plan or ask the user.
//
// Rule extraction strategy: parse Markdown bullet items that contain
// a "negation trigger" (don't, do not, never, avoid). The matcher is
// the rest of the bullet text after the trigger. We index by
// significant tokens (lowercase words ≥ 4 chars) so a tool call's
// input matches if it contains every significant token from a rule.
// Conservative on purpose — false-positive blocks are worse than
// false-negative blocks, since the user can always reword a rule.

package bundled

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// rule is one extracted "don't do X" entry. Body is the human-
// readable text shown to the user when a hook fires; tokens is the
// significant-word set used for matching.
type rule struct {
	Body   string
	Tokens []string
}

var (
	bulletRE = regexp.MustCompile(`^[\s]*[-*][\s]+(.+)$`)
	// negationRE matches the trigger words that mark a rule. Word
	// boundaries on both sides so "donut" doesn't trigger "don".
	negationRE = regexp.MustCompile(`(?i)\b(don'?t|do not|never|avoid)\b`)
)

// loadRules walks the rule sources and returns every extracted rule.
// Sources searched, in order:
//
//	<cwd>/.local.md          (project-local; usually gitignored)
//	<cwd>/CLAUDE.md          (project memory)
//	<cwd>/BIUMIND.md         (biu-native project memory)
//	~/.biumind/.local.md     (user-global)
//
// Missing files are silent skips. Returns an empty slice when no
// rules are found anywhere — the hook then becomes a no-op so the
// user's experience is identical to "hookify uninstalled".
func loadRules(cwd string) []rule {
	var sources []string
	if cwd != "" {
		sources = append(sources,
			filepath.Join(cwd, ".local.md"),
			filepath.Join(cwd, "CLAUDE.md"),
			filepath.Join(cwd, "BIUMIND.md"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		sources = append(sources, filepath.Join(home, ".biumind", ".local.md"))
	}

	var out []rule
	for _, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, extractRules(string(data))...)
	}
	return out
}

// extractRules parses a markdown body and yields each negation
// bullet as a rule. Indented bullets (continuation lines) merge
// into the parent. Rules without significant tokens are dropped —
// "don't" alone has no matcher and would block every tool call.
func extractRules(body string) []rule {
	var out []rule
	for _, line := range strings.Split(body, "\n") {
		m := bulletRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := m[1]
		neg := negationRE.FindStringIndex(text)
		if neg == nil {
			continue
		}
		// Rule body keeps the original phrasing including the trigger
		// so the user sees their own words back when the hook fires.
		body := strings.TrimSpace(text)
		// Match tokens are everything AFTER the trigger word and BEFORE
		// any rationale separator (em-dash, full-width em-dash, colon,
		// "because"). The user's intent in "don't run bun install —
		// use pnpm" is that `bun install` is the matcher and `use
		// pnpm` is the suggestion; a literal token-match on the full
		// trailing string would require "pnpm" in the tool input,
		// which isn't what the user meant.
		after := strings.TrimSpace(text[neg[1]:])
		after = truncateAtRationale(after)
		tokens := significantTokens(after)
		if len(tokens) == 0 {
			continue
		}
		out = append(out, rule{Body: body, Tokens: tokens})
	}
	return out
}

// truncateAtRationale strips the suggestion / explanation half of
// a rule. Splits on the first occurrence of: em-dash (—), en-dash
// (–), regular " - ", " : ", " because ", " instead ". Returns the
// rule body up to but not including the separator. The full original
// rule text is preserved in rule.Body for user-facing display; this
// truncation only affects matcher tokens.
func truncateAtRationale(s string) string {
	for _, sep := range []string{
		" — ", " – ", " - ",
		" : ", ": ",
		" because ", " instead ",
	} {
		if i := strings.Index(s, sep); i >= 0 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// hookifyStopWords are common English words that pollute the matcher
// when they appear in a rule. Without filtering, "the" and "with"
// would force the rule's input to also contain those words, which
// nearly every tool invocation does — producing false blocks. The
// list is deliberately tight; technical tokens (`bun`, `git`, `rm`,
// `cp`) stay so short CLI names participate in the match.
var hookifyStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "can": true, "you": true, "use": true, "any": true,
	"all": true, "with": true, "from": true, "this": true, "that": true,
	"into": true, "your": true, "have": true, "should": true,
}

// significantTokens returns lowercase words from s, with punctuation
// stripped and stop words filtered. The 2-char floor accommodates
// tools like "rm" and "cp"; the stop-word list keeps "the", "for",
// "with" out of the match set so rules don't accidentally block
// every command containing prepositions.
func significantTokens(s string) []string {
	var out []string
	for _, raw := range strings.Fields(s) {
		// Strip ascii punctuation — quotes / backticks / commas.
		t := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '/' || r == '-' || r == '_' || r == '.' {
				return r
			}
			return -1
		}, raw)
		t = strings.ToLower(t)
		if len(t) < 2 {
			continue
		}
		if hookifyStopWords[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// hookifyUserPrompt is the UserPromptSubmit handler. Loads rules
// fresh on each invocation so users editing their .local.md
// mid-session see updates immediately.
func hookifyUserPrompt(ctx context.Context, payload []byte) (hooks.Decision, error) {
	cwd, _ := os.Getwd()
	rules := loadRules(cwd)
	if len(rules) == 0 {
		return hooks.Decision{}, nil
	}
	var b strings.Builder
	b.WriteString("# Active project rules (from hookify)\n\n")
	b.WriteString("The following rules are extracted from local memory files. ")
	b.WriteString("Apply them to every tool call in this turn — the hookify ")
	b.WriteString("PreToolUse hook will also block matching invocations.\n\n")
	for _, r := range rules {
		b.WriteString("- ")
		b.WriteString(r.Body)
		b.WriteByte('\n')
	}
	return hooks.Decision{AdditionalContext: b.String()}, nil
}

// hookifyPreTool is the PreToolUse handler. Inspects the tool input
// JSON for any string field that contains every significant token of
// a rule; on match, blocks with that rule as the reason.
//
// Payload shape: at minimum `{ "tool_name": "Bash", "input": {...} }`.
// We walk every string value in `input` recursively. Other payload
// fields are ignored.
func hookifyPreTool(ctx context.Context, payload []byte) (hooks.Decision, error) {
	cwd, _ := os.Getwd()
	rules := loadRules(cwd)
	if len(rules) == 0 {
		return hooks.Decision{}, nil
	}
	var msg struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		// Bad payload shape isn't our problem — pass through.
		return hooks.Decision{}, nil
	}
	haystack := flattenStrings(msg.Input)
	for _, r := range rules {
		if matchesAllTokens(haystack, r.Tokens) {
			return hooks.Decision{
				Block:  true,
				Reason: "blocked by hookify rule: " + r.Body,
			}, nil
		}
	}
	return hooks.Decision{}, nil
}

// flattenStrings walks a JSON-decoded structure and returns every
// string value joined with spaces (so token-set membership tests are
// O(1) per token). Non-string leaves are stringified — `1234` could
// match `"1234"` if a rule mentioned that number.
func flattenStrings(v any) string {
	var b strings.Builder
	flatten(&b, v)
	return strings.ToLower(b.String())
}

func flatten(b *strings.Builder, v any) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteByte(' ')
	case []any:
		for _, child := range t {
			flatten(b, child)
		}
	case map[string]any:
		for _, child := range t {
			flatten(b, child)
		}
	default:
		// Numbers / bools — stringify via fmt indirectly.
		// We don't bother because rules tend to be about commands
		// and paths, not numeric matches.
	}
}

// matchesAllTokens reports whether haystack contains the rule's
// tokens with anchor-plus-majority semantics:
//
//   - every token of length ≥ 4 (an "anchor") must appear in
//     haystack — these are the rule's content-bearing words.
//   - of the short tokens (2–3 chars), at least half must appear.
//     Short tokens carry CLI names like `bun` / `rm` / `cp`; we
//     want them to influence the match without strictly requiring
//     every auxiliary verb the user wrote ("run", "push to") to
//     also appear in the tool input.
//
// When a rule has no anchors (e.g. "don't rm cp"), every short
// token is required — no anchor means we don't have enough signal
// for half-match heuristics.
func matchesAllTokens(haystack string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	var anchors, shorts []string
	for _, t := range tokens {
		if len(t) >= 4 {
			anchors = append(anchors, t)
		} else {
			shorts = append(shorts, t)
		}
	}
	for _, a := range anchors {
		if !strings.Contains(haystack, a) {
			return false
		}
	}
	if len(anchors) == 0 {
		// Conservative path: every short token must match.
		for _, s := range shorts {
			if !strings.Contains(haystack, s) {
				return false
			}
		}
		return true
	}
	if len(shorts) == 0 {
		return true
	}
	hits := 0
	for _, s := range shorts {
		if strings.Contains(haystack, s) {
			hits++
		}
	}
	// Require strictly more than half (rounded up) so 2 shorts
	// need 1 hit, 3 need 2, 4 need 2.
	return hits*2 >= len(shorts)
}
