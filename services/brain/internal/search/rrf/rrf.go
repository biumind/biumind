// Package rrf implements Reciprocal Rank Fusion for combining ranked lists.
//
//	score(d) = Σ_lists  1 / (k + rank_in_list(d))
//
// k=60 is the value from the original RRF paper (Cormack 2009).
package rrf

import "sort"

// Result is one ranked item.
type Result struct {
	ID    string
	Score float64
	Meta  map[string]any
}

// Fuse merges N ranked lists into a single ranked list using RRF.
//
//	k   smoothing constant; default 60 if 0
//	max optional cap on returned items; 0 = unlimited
func Fuse(lists [][]Result, k int, max int) []Result {
	if k <= 0 {
		k = 60
	}
	scores := map[string]float64{}
	meta := map[string]map[string]any{}
	for _, list := range lists {
		for rank, r := range list {
			scores[r.ID] += 1.0 / float64(k+rank+1)
			if r.Meta != nil && meta[r.ID] == nil {
				meta[r.ID] = r.Meta
			}
		}
	}
	out := make([]Result, 0, len(scores))
	for id, s := range scores {
		out = append(out, Result{ID: id, Score: s, Meta: meta[id]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}
