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
