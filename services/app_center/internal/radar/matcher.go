// Matcher — pure logic, no DB. Given a batch of candidates and a
// set of rules, returns the (rule, candidate) pairs that pass the
// match predicate. Cooldown is checked separately by the caller
// because it requires a DB lookup.

package radar

import (
	"context"
	"strings"
)

// MatchOne returns true when the candidate satisfies the rule's
// match_any/match_all/exclude predicates and the source filter.
// Pure — no DB, no I/O.
func MatchOne(rule *Rule, cand *Candidate) bool {
	if !rule.Enabled {
		return false
	}
	if !sourceAllows(rule.Sources, cand.Source) {
		return false
	}
	title := strings.ToLower(cand.Title)
	for _, kw := range rule.Exclude {
		if kw == "" {
			continue
		}
		if strings.Contains(title, strings.ToLower(kw)) {
			return false
		}
	}
	if len(rule.MatchAny) > 0 {
		ok := false
		for _, kw := range rule.MatchAny {
			if kw == "" {
				continue
			}
			if strings.Contains(title, strings.ToLower(kw)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(rule.MatchAll) > 0 {
		for _, kw := range rule.MatchAll {
			if kw == "" {
				continue
			}
			if !strings.Contains(title, strings.ToLower(kw)) {
				return false
			}
		}
	}
	// Empty keyword + empty semantic = degenerate "match everything";
	// reject so an empty rule doesn't fire-hose. The CRUD layer
	// rejects it at create time too as defense-in-depth.
	if len(rule.MatchAny) == 0 && len(rule.MatchAll) == 0 && rule.SemanticQuery == "" {
		return false
	}

	// M4 semantic fallback path: when the embedding worker isn't yet
	// wired (schedule: lands with M5 once /v1/embeddings is provisioned)
	// and SemanticEmbedding is nil, derive match terms from the natural-
	// language query and apply them as match_any. The user gets useful
	// semantic-style behaviour today without waiting for cosine. Once
	// SemanticEmbedding is populated, the matcher swaps to true cosine
	// (TODO: implement when the embedding pipeline lands).
	if rule.SemanticQuery != "" && len(rule.MatchAny) == 0 && len(rule.MatchAll) == 0 {
		terms := tokenizeSemanticQuery(rule.SemanticQuery)
		for _, kw := range terms {
			if strings.Contains(title, strings.ToLower(kw)) {
				return true
			}
		}
		return false
	}
	return true
}

// tokenizeSemanticQuery splits a free-text intent into match terms.
// CJK chars are emitted in 2-char windows (so "EU AI 监管" yields
// ["EU", "AI", "监管"] not ["监", "管"]); Latin words are split on
// whitespace + punctuation. Stop words are dropped.
func tokenizeSemanticQuery(s string) []string {
	stop := map[string]bool{
		"的": true, "了": true, "和": true, "与": true, "或": true, "在": true,
		"是": true, "都": true, "也": true, "等": true, "any": true, "all": true,
		"the": true, "a": true, "an": true, "of": true, "or": true, "and": true,
		"我": true, "想": true, "要": true,
	}
	out := []string{}
	cur := strings.Builder{}
	flush := func() {
		v := strings.TrimSpace(cur.String())
		cur.Reset()
		if v == "" || stop[strings.ToLower(v)] {
			return
		}
		out = append(out, v)
	}
	cjkRun := []rune{}
	for _, r := range s {
		isCJK := (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF)
		isLatin := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		switch {
		case isCJK:
			if cur.Len() > 0 {
				flush()
			}
			cjkRun = append(cjkRun, r)
		case isLatin:
			if len(cjkRun) > 0 {
				out = appendCJKBigrams(out, cjkRun, stop)
				cjkRun = nil
			}
			cur.WriteRune(r)
		default:
			if cur.Len() > 0 {
				flush()
			}
			if len(cjkRun) > 0 {
				out = appendCJKBigrams(out, cjkRun, stop)
				cjkRun = nil
			}
		}
	}
	if cur.Len() > 0 {
		flush()
	}
	if len(cjkRun) > 0 {
		out = appendCJKBigrams(out, cjkRun, stop)
	}
	// Dedup preserving order.
	seen := map[string]bool{}
	uniq := make([]string, 0, len(out))
	for _, t := range out {
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		uniq = append(uniq, t)
	}
	return uniq
}

func appendCJKBigrams(out []string, runs []rune, stop map[string]bool) []string {
	if len(runs) == 1 {
		s := string(runs)
		if !stop[s] {
			out = append(out, s)
		}
		return out
	}
	for i := 0; i < len(runs)-1; i++ {
		bi := string(runs[i : i+2])
		if !stop[bi] {
			out = append(out, bi)
		}
	}
	// Always emit individual chars as fallback when bigrams + 1
	// (covers single-char tokens too).
	if len(runs) <= 3 {
		for _, r := range runs {
			s := string(r)
			if !stop[s] {
				out = append(out, s)
			}
		}
	}
	return out
}

func sourceAllows(allowed []string, src string) bool {
	if len(allowed) == 0 {
		return false
	}
	for _, s := range allowed {
		if s == "*" || s == src {
			return true
		}
	}
	return false
}

// scopeAllows enforces tenant boundaries — RSS candidates must come
// from a feed in the rule's scope; board candidates skip this check
// because boards are global.
func scopeAllows(rule *Rule, cand *Candidate) bool {
	if cand.OwnerScope == "" {
		return true // global candidate (board)
	}
	return rule.Scope == cand.OwnerScope && rule.ScopeID == cand.OwnerScopeID
}

// MatchBatch evaluates every (rule, candidate) pair and returns the
// hits that pass the predicate AND scope check. Caller layers on
// the cooldown check.
func MatchBatch(_ context.Context, rules []*Rule, candidates []Candidate) []Hit {
	hits := make([]Hit, 0)
	for ci := range candidates {
		c := &candidates[ci]
		for _, r := range rules {
			if !scopeAllows(r, c) {
				continue
			}
			if !MatchOne(r, c) {
				continue
			}
			hits = append(hits, Hit{
				RuleID:       r.ID,
				Source:       c.Source,
				Title:        c.Title,
				URL:          c.URL,
				TitleHash:    c.TitleHash,
				RuleSnapshot: *r,
			})
		}
	}
	return hits
}
