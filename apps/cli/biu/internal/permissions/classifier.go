// Package permissions semantic-prompt classifier.
//
// The allowedPrompts feature lets the model batch-request approval
// for *categories* of action up front ("run tests", "install
// dependencies") instead of asking per-call. biu ships a small
// deterministic heuristic — fast, no extra LLM call per tool.
//
// Algorithm (case-insensitive throughout):
//
//   1. Tokenise prompt + command on whitespace + punctuation.
//   2. Drop stop-words (`run`, `the`, `all`, etc).
//   3. Stem each remaining prompt token to its first 4 chars
//      ("tests" → "test", "dependencies" → "depe"). Tokens shorter
//      than 4 chars are kept whole.
//   4. Match if every stemmed prompt token appears as substring of
//      the lowercased command.
//
// Conservative bias: we'd rather miss a fuzzy match (and ask the
// user) than allow an unintended command. That's why every prompt
// keyword must hit, not "any". Single-keyword prompts ("test",
// "deploy") give the model an easy lever to widen the match.

package permissions

import (
	"strings"
)

// matchPromptToCommand reports whether `command` plausibly fulfils
// the natural-language `prompt` description. Used by Decide to
// honour ExitPlanMode's allowedPrompts.
//
// Empty prompt or command is never a match (avoid foot-gun where a
// bug stages a blank prompt and ends up auto-allowing everything).
func matchPromptToCommand(prompt, command string) bool {
	prompt = strings.TrimSpace(prompt)
	command = strings.TrimSpace(command)
	if prompt == "" || command == "" {
		return false
	}
	keys := stemmedKeywords(prompt)
	if len(keys) == 0 {
		// Prompt was all stop-words — refuse rather than allow.
		return false
	}
	low := strings.ToLower(command)
	for _, k := range keys {
		if !strings.Contains(low, k) {
			return false
		}
	}
	return true
}

// stemmedKeywords lower-cases, splits, drops stop words, and prefix-
// stems the remaining tokens. Exposed (lowercase) for tests.
func stemmedKeywords(s string) []string {
	s = strings.ToLower(s)
	raw := strings.FieldsFunc(s, func(r rune) bool {
		// Tokenise on anything that isn't a letter, digit, or one of
		// a few "stays in command" punctuation marks. Hyphens stay
		// glued so flags like `--no-cache` survive.
		return !(r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9'))
	})
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if t == "" || stopWords[t] {
			continue
		}
		// Drop pure-digit tokens — almost always noise (e.g. "step 1
		// install deps" shouldn't require literal "1" in the command).
		if isDigits(t) {
			continue
		}
		out = append(out, stem(t))
	}
	return out
}

func stem(t string) string {
	if len(t) <= 4 {
		return t
	}
	return t[:4]
}

func isDigits(t string) bool {
	for _, r := range t {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// stopWords is intentionally small. Bias: keep words that *carry
// intent* in the keywords list. "test", "build", "deploy" are
// content; "the", "and", "all" are noise.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "any": true, "are": true,
	"as": true, "at": true, "be": true, "by": true, "do": true,
	"for": true, "from": true, "in": true, "into": true, "is": true,
	"it": true, "of": true, "on": true, "or": true, "out": true,
	"over": true, "run": true, "running": true, "than": true,
	"that": true, "the": true, "then": true, "this": true, "to": true,
	"with": true, "without": true,
	// Vague action verbs — usually accompanied by a specific noun
	// that survives, so dropping them tightens the match.
	"all": true, "execute": true, "perform": true,
}
