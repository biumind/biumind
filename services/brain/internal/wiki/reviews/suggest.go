// Suggested fix targets for dead wikilinks (P2 #20 ②).
//
// Chosen approach: trigram Jaccard similarity computed in Go over the
// project's live title list — the same similarity family pg_trgm uses,
// but evaluated in-memory because the lint worker already holds the full
// title list for the project. Cost comparison against the alternatives:
//
//   - pgvector ANN over wiki_chunks: needs an embedding for the dead
//     target text, which means a model-relay call per dead link per
//     scan (I6-compliant but slow and priced), plus chunk embeddings
//     may lag page renames by a re-embed cycle.
//   - pg_trgm SQL similarity(): correct but one extra DB round trip per
//     dead target, and pages.title carries no trigram index today.
//   - in-memory trigram Jaccard (this file): zero DB/LLM cost,
//     deterministic, unit-testable, and titles are exactly the namespace
//     wikilinks resolve against.
package reviews

import "strings"

// suggestMinSimilarity gates what becomes a suggested_target. 0.5 on
// trigram Jaccard is deliberately strict: a bad suggestion is worse
// than none (apply-fix rewrites real content on one click).
const suggestMinSimilarity = 0.5

// suggestContainBonus lifts candidates where one string contains the
// other ("机器学习" dead, "机器学习入门" live) — trigram Jaccard alone
// undervalues short-target containment.
const suggestContainBonus = 0.25

// SuggestLinkTarget returns the live page title closest to a dead
// wikilink target, or "" when nothing clears the threshold. `target`
// is expected already normalised (lowercased + trimmed, the same form
// dead_wikilink stores in target_normalised); titles are original-case
// and the winner is returned in its original case so the rewrite lands
// on the canonical title text. Titles equal to the target are skipped
// (that title existing would mean the link isn't dead).
func SuggestLinkTarget(target string, titles []string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" || len(titles) == 0 {
		return ""
	}
	targetGrams := trigrams(target)
	best := ""
	bestScore := suggestMinSimilarity
	for _, t := range titles {
		tNorm := strings.ToLower(strings.TrimSpace(t))
		if tNorm == "" || tNorm == target {
			continue
		}
		score := jaccard(targetGrams, trigrams(tNorm))
		if strings.Contains(tNorm, target) || strings.Contains(target, tNorm) {
			score += suggestContainBonus
		}
		// Strictly-greater keeps the earliest title on ties →
		// deterministic given the worker passes a sorted list.
		if score > bestScore {
			bestScore = score
			best = strings.TrimSpace(t)
		}
	}
	return best
}

// trigrams returns the set of character trigrams of s, space-padded on
// both sides (pg_trgm convention) so short strings still produce a
// usable set.
func trigrams(s string) map[string]struct{} {
	r := []rune("  " + s + " ")
	out := make(map[string]struct{}, len(r))
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])] = struct{}{}
	}
	if len(out) == 0 && len(r) > 0 {
		out[string(r)] = struct{}{}
	}
	return out
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for g := range a {
		if _, ok := b[g]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
