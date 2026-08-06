// Package bm25 wraps Postgres tsvector ranking with the biumind_zhcn config.
package bm25

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Hit struct {
	ID        string // page id or block id
	Kind      string // "page" / "block"
	PageID    string // for blocks: their page; for pages: same as ID
	ProjectID string
	Title     string
	Snippet   string
	Score     float64
	UpdatedAt time.Time
}

type Searcher struct {
	Pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Searcher { return &Searcher{Pool: p} }

// SearchOptions controls scope.
type SearchOptions struct {
	ProjectID     *uuid.UUID // nil = all projects owned by user (caller filters)
	OwnerID       uuid.UUID  // restricts to projects owned by this user
	IncludeBlocks bool       // search blocks alongside pages
	Limit         int        // per kind; default 50
}

// Search returns ranked hits across pages (and optionally blocks).
// Uses websearch_to_tsquery for ergonomic input parsing.
func (s *Searcher) Search(ctx context.Context, query string, opt SearchOptions) ([]Hit, error) {
	if query = strings.TrimSpace(query); query == "" {
		return nil, nil
	}
	if opt.Limit <= 0 {
		opt.Limit = 50
	}

	hits := make([]Hit, 0, opt.Limit*2)

	// Pages
	pageRows, err := s.Pool.Query(ctx, `
		SELECT p.id, p.project_id, p.title, p.updated_at,
		       ts_rank_cd(p.tsv, websearch_to_tsquery('biumind_zhcn', $1)) AS rank
		FROM brain.pages p
		JOIN brain.projects pr ON pr.id = p.project_id
		WHERE p.deleted_at IS NULL
		  AND p.tsv @@ websearch_to_tsquery('biumind_zhcn', $1)
		  AND ($2::uuid IS NULL OR p.project_id = $2)
		  AND ($3::uuid IS NULL OR pr.owner_id = $3)
		ORDER BY rank DESC
		LIMIT $4
	`, query, opt.ProjectID, nullableUUID(opt.OwnerID), opt.Limit)
	if err != nil {
		return nil, err
	}
	for pageRows.Next() {
		var h Hit
		var pid uuid.UUID
		if err := pageRows.Scan(&h.ID, &pid, &h.Title, &h.UpdatedAt, &h.Score); err != nil {
			pageRows.Close()
			return nil, err
		}
		h.Kind = "page"
		h.PageID = h.ID
		h.ProjectID = pid.String()
		hits = append(hits, h)
	}
	pageRows.Close()

	if !opt.IncludeBlocks {
		return hits, nil
	}

	blockRows, err := s.Pool.Query(ctx, `
		SELECT b.id, b.page_id, p.project_id, coalesce(p.title, '') AS title,
		       coalesce(b.content->>'text', '') AS snippet,
		       b.updated_at,
		       ts_rank_cd(b.tsv, websearch_to_tsquery('biumind_zhcn', $1)) AS rank
		FROM brain.blocks b
		JOIN brain.pages p ON p.id = b.page_id
		JOIN brain.projects pr ON pr.id = p.project_id
		WHERE b.deleted_at IS NULL
		  AND p.deleted_at IS NULL
		  AND b.tsv @@ websearch_to_tsquery('biumind_zhcn', $1)
		  AND ($2::uuid IS NULL OR p.project_id = $2)
		  AND ($3::uuid IS NULL OR pr.owner_id = $3)
		ORDER BY rank DESC
		LIMIT $4
	`, query, opt.ProjectID, nullableUUID(opt.OwnerID), opt.Limit)
	if err != nil {
		return hits, nil // best-effort: pages already returned
	}
	for blockRows.Next() {
		var h Hit
		var pageID, pid uuid.UUID
		if err := blockRows.Scan(&h.ID, &pageID, &pid, &h.Title, &h.Snippet, &h.UpdatedAt, &h.Score); err != nil {
			blockRows.Close()
			return hits, nil
		}
		h.Kind = "block"
		h.PageID = pageID.String()
		h.ProjectID = pid.String()
		if len(h.Snippet) > 200 {
			h.Snippet = h.Snippet[:200] + "…"
		}
		hits = append(hits, h)
	}
	blockRows.Close()
	return hits, nil
}

func nullableUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}
