// Package promptsuggest provides input-time prompt suggestions for
// the REPL — the user pauses mid-typing or hits a hotkey, biu
// surfaces 1-3 candidate completions ranked by likelihood +
// recency.
//
// The prompt-suggest design is structural rather than LLM-driven:
// fuzzy match against history + slash catalog, with no API calls.
// The "speculation" variant (LLM next-utterance prediction) is
// opt-in via the same Summariser interface compact / extractor /
// agentsummary use, so wiring controls whether to spend tokens on
// it. The feature is gated by an env var (BIU_PROMPT_SUGGEST=0 to
// disable).
//
// Three suggestion sources, in order of preference:
//
//   1. Slash catalog — when the input starts with `/` (or close,
//      with typo tolerance), suggest matching slash commands.
//   2. History fuzzy — recent user-prompts containing the typed
//      substring (case-insensitive).
//   3. Speculation — optional LLM call producing a free-form
//      next-utterance candidate when the other two are empty.

package promptsuggest

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// MaxSuggestions caps the number of suggestions a single Suggest
// call returns. The REPL UI shows them as a dropdown; more than
// 3 is clutter.
const MaxSuggestions = 3

// Suggestion is one candidate the UI may render.
type Suggestion struct {
	// Body is the text that replaces (or completes) the user's
	// current input when accepted.
	Body string

	// Source is "slash" / "history" / "speculation". UI may colour-
	// code or filter on this.
	Source string

	// Score is 0.0–1.0; higher = more relevant. Ranked descending
	// in the returned slice. Within the same source the score
	// follows the source's natural order (slash: prefix length;
	// history: recency * substring match; speculation: LLM
	// confidence proxy).
	Score float64
}

// Sources is a typed lookup so callers can disable specific paths
// without wiring N booleans. Pass a nil Sources to use defaults
// (all enabled).
type Sources struct {
	Slash       []string // slash command names from the catalog
	History     []state.Message
	Speculation Speculator // optional LLM speculator
}

// Speculator is the LLM-driven next-utterance predictor. nil
// disables the speculation branch entirely. Contract: given
// recent context, return one short candidate.
type Speculator interface {
	Speculate(ctx context.Context, history []state.Message, partial string) (string, error)
}

// IsEnabled returns whether prompt suggestions should run this
// session. Default true; BIU_PROMPT_SUGGEST=0 / false / off disables.
func IsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("BIU_PROMPT_SUGGEST")))
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// Suggest builds the ranked candidate list for the supplied partial
// input. ctx is used only by the speculation branch; the slash +
// history paths are pure CPU.
//
// Returns at most MaxSuggestions items, descending by Score. Empty
// input returns empty result (no suggestions for a blank prompt).
func Suggest(ctx context.Context, partial string, src Sources) []Suggestion {
	if !IsEnabled() {
		return nil
	}
	partial = strings.TrimSpace(partial)
	if partial == "" {
		return nil
	}

	var out []Suggestion

	// Slash branch — only when partial starts with `/` (or "/" + ε).
	if strings.HasPrefix(partial, "/") {
		out = append(out, slashSuggestions(partial, src.Slash)...)
	}

	// History branch — substring match across recent user turns,
	// most-recent first.
	out = append(out, historySuggestions(partial, src.History)...)

	// Speculation — only when the other two yielded nothing useful.
	// Speculation costs an API call; we don't want to fire it when
	// a cheap structural match already covered the user.
	if len(out) == 0 && src.Speculation != nil {
		if s, err := src.Speculation.Speculate(ctx, src.History, partial); err == nil &&
			strings.TrimSpace(s) != "" {
			out = append(out, Suggestion{
				Body:   strings.TrimSpace(s),
				Source: "speculation",
				Score:  0.5, // neutral — caller can re-rank if needed
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > MaxSuggestions {
		out = out[:MaxSuggestions]
	}
	return out
}

// slashSuggestions ranks slash-catalog entries by prefix match.
// Score formula: matched-chars / longest-name-among-candidates
// (so "/c" matches /clear /compact /copy /cost equally; "/co" gives
// /compact /copy /cost). Catalog is passed as a string slice rather
// than the typed SlashCmd to avoid an import cycle with the repl
// package.
func slashSuggestions(partial string, catalog []string) []Suggestion {
	if len(catalog) == 0 {
		return nil
	}
	q := strings.ToLower(partial)
	var hits []Suggestion
	for _, name := range catalog {
		ln := strings.ToLower(name)
		if !strings.HasPrefix(ln, q) {
			continue
		}
		// Score: 0.7 baseline + up-to-0.3 prefix-completeness bonus.
		// The baseline puts slash hits above history's max (0.7),
		// reflecting the action-deterministic nature of catalog
		// matches (the user typed `/`, they want a slash).
		// 1.0 = exact match (q == name); 0.7 = q is just `/`.
		ratio := float64(len(q)) / float64(len(ln))
		if ratio > 1 {
			ratio = 1
		}
		score := 0.7 + 0.3*ratio
		hits = append(hits, Suggestion{
			Body:   name,
			Source: "slash",
			Score:  score,
		})
	}
	return hits
}

// historySuggestions returns recent user-prompts containing partial
// (case-insensitive). Most-recent matches score highest, and
// substring-position closer to the start scores higher within the
// same recency bucket.
func historySuggestions(partial string, history []state.Message) []Suggestion {
	q := strings.ToLower(partial)
	var hits []Suggestion
	maxRecency := 0.6 // history caps below slash-prefix exact match
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role != state.RoleUser {
			continue
		}
		body := concatUserText(m.Content)
		if body == "" {
			continue
		}
		idx := strings.Index(strings.ToLower(body), q)
		if idx < 0 {
			continue
		}
		// Recency: index 0 (newest) → 1.0; older messages decay.
		recency := maxRecency
		if i < len(history)-10 {
			recency = maxRecency * 0.5
		}
		// Position bonus: substring at start of body is more
		// likely the user's same intent recurring vs random
		// substring deep in a long prompt.
		positionBonus := 0.0
		if idx == 0 {
			positionBonus = 0.1
		}
		hits = append(hits, Suggestion{
			Body:   strings.TrimSpace(body),
			Source: "history",
			Score:  recency + positionBonus,
		})
	}
	return hits
}

// concatUserText flattens the text content blocks from a user
// message. Tool-result / image blocks are ignored — completing
// based on a tool blob is wrong.
func concatUserText(blocks []state.ContentBlock) string {
	var b strings.Builder
	for _, c := range blocks {
		if c.Type == state.ContentText {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
