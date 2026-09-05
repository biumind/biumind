// Package vector wraps brain.wiki_chunks ANN cosine search as the third
// retrieval path that feeds RRF fusion.
//
// Why a thin wrapper over chunks.Store.Search:
//
//   - api/api.go (the search HTTP server) shouldn't import the chunks
//     persistence package directly — keeps the handler layer focused on
//     transport concerns.
//   - The contract here mirrors bm25.Searcher and searxng.Client (Search
//     returning a per-source Hit struct with a Score) so api.go can
//     branch on availability and fuse uniformly.
//   - Embedding is the caller's responsibility: vector retrieval needs a
//     query vector but search/api already owns the embed.Embedder it'd
//     use for memory recall. Doing the embed call inside this package
//     would duplicate that wiring.
package vector

import (
	"context"
	"sort"
	"time"

	"github.com/biumind/biumind/services/brain/internal/wiki/chunks"
	"github.com/google/uuid"
)

// Hit is the projection api/api.go needs to render a vector search row.
// Mirrors bm25.Hit field-for-field where it makes sense; ChunkID is
// vector-specific and lets the UI deep-link to the source slice.
type Hit struct {
	ChunkID    string
	Kind       string // always "chunk"
	PageID     string
	BlockID    string
	ProjectID  string
	Title      string
	Snippet    string
	Score      float64 // cosine similarity (0..1, higher = closer)
	UpdatedAt  time.Time
	TokenCount int
}

// Searcher is the public façade around chunks.Store.Search.
type Searcher struct {
	store *chunks.Store
}

func New(s *chunks.Store) *Searcher { return &Searcher{store: s} }

type SearchOptions struct {
	OwnerID        uuid.UUID
	ProjectID      *uuid.UUID
	QueryEmbedding []float32
	Limit          int
	// MaxDistance is forwarded to chunks.Store.Search; zero ⇒ default
	// 0.7 cosine distance (≈ 0.3 similarity floor).
	MaxDistance float64
}

// Search returns ranked vector hits. Empty input or zero-length
// embedding return (nil, nil) — callers treat absent vector results as
// "third path unavailable" and fall back to BM25 + web fusion only.
func (s *Searcher) Search(ctx context.Context, opt SearchOptions) ([]Hit, error) {
	if s == nil || s.store == nil || len(opt.QueryEmbedding) == 0 {
		return nil, nil
	}
	rows, err := s.store.Search(ctx, chunks.SearchInput{
		OwnerID:        opt.OwnerID,
		ProjectID:      opt.ProjectID,
		QueryEmbedding: opt.QueryEmbedding,
		Limit:          opt.Limit,
		MaxDistance:    opt.MaxDistance,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(rows))
	for _, r := range rows {
		out = append(out, Hit{
			ChunkID:    r.ChunkID,
			Kind:       "chunk",
			PageID:     r.PageID,
			BlockID:    r.BlockID,
			ProjectID:  r.ProjectID,
			Title:      r.Title,
			Snippet:    r.Snippet,
			Score:      r.Score,
			UpdatedAt:  r.UpdatedAt,
			TokenCount: r.TokenCount,
		})
	}
	return out, nil
}

// OverFetchLimit returns the chunk-window size the ANN query should use
// so page-level aggregation has enough candidates to work with: the
// caller-visible limit ×3, floored at 30. Mirrors reference/llm_wiki
// embedding.ts searchByEmbedding (Math.max(topK * 3, 30)) — a bare
// top-K chunk window starves recall when several chunks of the same
// page crowd out other pages.
func OverFetchLimit(limit int) int {
	n := limit * 3
	if n < 30 {
		n = 30
	}
	return n
}

// pageTailWeight is the weight applied to the non-best chunks of a page
// when blending the page score. 0.3 is empirical (reference/llm_wiki);
// adjust with real data.
const pageTailWeight = 0.3

// CollapsePages aggregates chunk-level hits to page granularity. Each
// page's score is its best chunk score plus a bounded tail contribution:
//
//	pageScore = max + min(pageTailWeight × Σ(other chunk scores), max(0, 1 − max))
//
// so a page with two good chunks outranks a page with one equally-good
// chunk, while many weak chunks can't drown a single strong one (the
// cap keeps pageScore ≤ 1). The best chunk stays as the representative
// row (ChunkID/BlockID/Snippet deep-link). Pages sort by blended score
// descending and the result is truncated to limit. Input order is
// preserved as the tie-breaker. Hits with an empty PageID pass through
// ungrouped (defensive — the chunks table always sets page_id).
func CollapsePages(hits []Hit, limit int) []Hit {
	if len(hits) == 0 || limit <= 0 {
		return nil
	}
	type page struct {
		rep  Hit     // best chunk, Score replaced by blended score
		rest float64 // sum of the other chunks' scores
	}
	pages := map[string]*page{}
	order := make([]string, 0, len(hits))
	var loose []Hit
	for _, h := range hits {
		if h.PageID == "" {
			loose = append(loose, h)
			continue
		}
		p, ok := pages[h.PageID]
		if !ok {
			pages[h.PageID] = &page{rep: h}
			order = append(order, h.PageID)
			continue
		}
		if h.Score > p.rep.Score {
			p.rest += p.rep.Score
			p.rep = h
		} else {
			p.rest += h.Score
		}
	}
	out := make([]Hit, 0, len(pages)+len(loose))
	for _, id := range order {
		p := pages[id]
		headroom := 1 - p.rep.Score
		if headroom < 0 {
			headroom = 0
		}
		tail := p.rest * pageTailWeight
		if tail > headroom {
			tail = headroom
		}
		p.rep.Score += tail
		out = append(out, p.rep)
	}
	// Loose (page-less) hits rank by score alongside collapsed pages.
	// Stable sort: first-sighting order breaks score ties.
	out = append(out, loose...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
