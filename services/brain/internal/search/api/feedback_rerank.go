// Feedback rerank — closes the P1-1 / B-12 loop.
//
// search_feedback rows are written by the thumbs UI on every result
// verdict; before this file they were never read at query time. Now the
// search handler pulls the user's verdicts for the current (user, query)
// pair once per request and feeds them here to re-rank the page-bearing
// result lists (Wiki + Fused).
//
// Strategy — normalized additive bonus (chosen over hard pin/filter so
// the effect is graduated and reversible):
//
//	1. Normalize each list's scores to [0,1] by the list's own max score
//	   (scores are non-negative rank/sim values). This makes the bonus
//	   meaningful regardless of whether the list is RRF-fused (~0.02) or
//	   raw BM25 (~tens) — scale-independent.
//	2. Add +0.5 for an "up" verdict, −0.5 for "down". Half the unit
//	   range is enough to promote a mid-ranked upvoted page past an
//	   unvoted one without overriding a strongly-relevant top hit, and
//	   to demote a downvoted top page below the unvoted middle.
//	3. Stable-sort by adjusted score descending. Equal adjusted scores
//	   keep the engine's original order.
//
// Raw scores are returned unchanged — only order moves — and each moved
// row carries a Feedback badge so the UI can explain the ranking.
package api

import (
	"context"
	"sort"

	"github.com/google/uuid"
)

// feedbackScore is the per-item projection the rerank needs, decoupled
// from the concrete hit types (wikiHit vs fusedHit). pageID is "" for
// items without a page (web hits) — feedback never applies to those.
type feedbackScore struct {
	score  float64
	pageID string
}

// feedbackRerank returns a stable reordering (slice of original indices)
// of items after applying the user's verdicts as a normalized ±0.5
// bonus. verdicts maps page_id → "up"|"down". Empty verdicts or empty
// items → identity order.
func feedbackRerank(items []feedbackScore, verdicts map[string]string) []int {
	order := make([]int, len(items))
	for i := range items {
		order[i] = i
	}
	if len(verdicts) == 0 || len(items) == 0 {
		return order
	}
	max := 0.0
	for _, it := range items {
		if it.score > max {
			max = it.score
		}
	}
	if max <= 0 {
		max = 1 // all-zero list: normalize to 0, bonus still applies
	}
	adj := make([]float64, len(items))
	for i, it := range items {
		a := it.score / max
		switch verdicts[it.pageID] {
		case "up":
			a += 0.5
		case "down":
			a -= 0.5
		}
		adj[i] = a
	}
	sort.SliceStable(order, func(x, y int) bool {
		return adj[order[x]] > adj[order[y]]
	})
	return order
}

// reorderWiki applies feedback rerank to a Wiki hit list and stamps the
// Feedback badge on moved rows.
func reorderWiki(hits []wikiHit, verdicts map[string]string) []wikiHit {
	items := make([]feedbackScore, len(hits))
	for i, h := range hits {
		items[i] = feedbackScore{score: h.Score, pageID: h.PageID}
	}
	order := feedbackRerank(items, verdicts)
	out := make([]wikiHit, len(hits))
	for i, idx := range order {
		h := hits[idx]
		if sig := verdicts[h.PageID]; sig != "" {
			h.Feedback = sig
		}
		out[i] = h
	}
	return out
}

// reorderFused applies feedback rerank to a Fused hit list. page_id lives
// in Meta for wiki/vector/graph items; web items (no page_id) are
// untouched by the verdict map and keep their relative order.
func reorderFused(hits []fusedHit, verdicts map[string]string) []fusedHit {
	items := make([]feedbackScore, len(hits))
	for i, h := range hits {
		pid, _ := h.Meta["page_id"].(string)
		items[i] = feedbackScore{score: h.Score, pageID: pid}
	}
	order := feedbackRerank(items, verdicts)
	out := make([]fusedHit, len(hits))
	for i, idx := range order {
		h := hits[idx]
		if pid, _ := h.Meta["page_id"].(string); pid != "" {
			if sig := verdicts[pid]; sig != "" {
				h.Feedback = sig
			}
		}
		out[i] = h
	}
	return out
}

// loadFeedback reads the user's stored verdicts for one query. Returns a
// page_id → signal ("up"|"down") map; empty (not nil) when there are
// none. queryLower must already be lowercased — search_feedback keys on
// query_lower to fold case. A nil pool (search backend unconfigured) is
// not an error here; it just means no feedback to apply.
func (s *Server) loadFeedback(ctx context.Context, uid uuid.UUID, queryLower string) (map[string]string, error) {
	pool := s.feedbackPool()
	if pool == nil || queryLower == "" {
		return map[string]string{}, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT page_id, signal
		  FROM brain.search_feedback
		 WHERE user_id = $1 AND query_lower = $2
	`, uid, queryLower)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var (
			pid uuid.UUID
			sig string
		)
		if err := rows.Scan(&pid, &sig); err != nil {
			return nil, err
		}
		out[pid.String()] = sig
	}
	return out, rows.Err()
}
