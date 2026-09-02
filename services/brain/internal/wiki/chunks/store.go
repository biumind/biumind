// Package chunks data access for brain.wiki_chunks.
//
// The store is the connection point between three subsystems:
//
//	wiki/api  → ReplacePage when blocks change (re-chunk + insert)
//	embed worker → ClaimUnembedded + SetEmbeddings (drain pending)
//	search/vector → ANN (cosine top-k under project + ownership)
//
// All inserts go through ReplacePage to keep the (page_id, ord) ordering
// dense and idempotent: an upsert on a page first deletes that page's
// existing chunks then bulk-inserts the new ones in one transaction.
// This avoids the partial-update class of bug where blocks-deleted-since
// remained as dangling chunks.
package chunks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// Row mirrors a brain.wiki_chunks row. Embedding is omitted because no
// caller currently needs to read vectors back; pulling them out of pgvector
// is also expensive (full vector serialisation) so a separate accessor
// can be added when needed.
type Row struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	PageID      uuid.UUID
	BlockID     *uuid.UUID
	Ord         int
	Text        string
	HeadingPath string
	TokenCount  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ReplacePage atomically replaces all chunks for `pageID` with `chunks`.
// Use this after a page's blocks change so chunks track the live content
// without producing stale rows. `projectID` must match the page's
// project; the caller (wiki API) is the authority on that, but we add a
// trivial guard so a misuse doesn't silently desynchronise the foreign key.
//
// Returns the number of rows inserted (zero when chunks is empty — a
// page with no text content has no chunks, which is fine).
func (s *Store) ReplacePage(ctx context.Context, projectID, pageID uuid.UUID, chunks []Chunk) (int, error) {
	if pageID == uuid.Nil || projectID == uuid.Nil {
		return 0, fmt.Errorf("project_id and page_id required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM brain.wiki_chunks WHERE page_id = $1`, pageID); err != nil {
		return 0, fmt.Errorf("delete prior chunks: %w", err)
	}
	if len(chunks) == 0 {
		return 0, tx.Commit(ctx)
	}
	// Bulk insert — one round trip beats per-row Exec by an order of
	// magnitude on pages with dozens of chunks. We render values inline
	// with placeholders rather than COPY so failures surface as ordinary
	// SQL errors (and the row count stays stable).
	const cols = 7
	args := make([]any, 0, len(chunks)*cols)
	values := make([]string, 0, len(chunks))
	for i, c := range chunks {
		base := i * cols
		values = append(values, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7))
		args = append(args, projectID, pageID, c.BlockID, c.Ord, c.Text, c.HeadingPath, c.TokenCount)
	}
	q := `INSERT INTO brain.wiki_chunks
	      (project_id, page_id, block_id, ord, text, heading_path, token_count)
	      VALUES ` + strings.Join(values, ",")
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("insert chunks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// CountForPage returns the number of chunk rows linked to `pageID`.
// Used by tests + observability; not on any hot path.
func (s *Store) CountForPage(ctx context.Context, pageID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM brain.wiki_chunks WHERE page_id = $1`, pageID).
		Scan(&n)
	return n, err
}

// ─── Embedding backfill ─────────────────────────────────────────

// MaxEmbedAttempts bounds how many times the embed worker retries one
// chunk before treating it as a poison pill (bad input the provider
// always rejects). Mirrors the partial-index predicate in
// migrations/00008_embed_retry.sql — change both together.
const MaxEmbedAttempts = 5

// Pending is the projection the embed worker needs.
type Pending struct {
	ID   uuid.UUID
	Text string
	// Attempts is how many times embedding this chunk has already
	// failed. The worker uses it to log the moment a chunk crosses
	// MaxEmbedAttempts and becomes a skipped poison pill.
	Attempts int
}

// ClaimUnembedded grabs up to `batch` chunks that still need an embedding,
// locking them with FOR UPDATE SKIP LOCKED so multiple brain replicas don't
// double-embed the same row. Chunks that already failed MaxEmbedAttempts
// times (poison pills) are excluded — they stay NULL-embedded forever and
// are observable via CountEmbedExhausted. Caller MUST commit the returned
// tx via SetEmbeddings (or rollback) so locks release.
func (s *Store) ClaimUnembedded(ctx context.Context, batch int) ([]Pending, pgx.Tx, error) {
	if batch <= 0 {
		batch = 32
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	q := fmt.Sprintf(`
		SELECT id, COALESCE(heading_path || E'\n\n', '') || text, embed_attempts
		FROM brain.wiki_chunks
		WHERE embedding IS NULL
		  AND embed_attempts < %d
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, MaxEmbedAttempts)
	rows, err := tx.Query(ctx, q, batch)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Text, &p.Attempts); err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return out, tx, nil
}

// MarkEmbedFailures increments embed_attempts for chunks whose embed
// attempt failed, inside the claim transaction (the commit happens in
// SetEmbeddings). Once a row reaches MaxEmbedAttempts it stops being
// reclaimed — see ClaimUnembedded.
//
// updated_at is deliberately NOT bumped: findStalePages compares block
// vs chunk updated_at to decide re-chunking, and touching it here would
// mask a genuinely stale page from the rechunk pass.
func (s *Store) MarkEmbedFailures(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE brain.wiki_chunks
		   SET embed_attempts = embed_attempts + 1
		 WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return fmt.Errorf("mark embed failures: %w", err)
	}
	return nil
}

// SetEmbeddings writes the embeddings produced for a batch and commits
// the transaction returned by ClaimUnembedded. Vectors must match the
// pgvector column dimension.
func (s *Store) SetEmbeddings(ctx context.Context, tx pgx.Tx, vecs map[uuid.UUID][]float32) error {
	defer func() { _ = tx.Rollback(ctx) }() // safe no-op after Commit
	for id, v := range vecs {
		if len(v) == 0 {
			continue
		}
		_, err := tx.Exec(ctx,
			`UPDATE brain.wiki_chunks
			    SET embedding = $1::vector, updated_at = now()
			  WHERE id = $2`,
			vectorLiteral(v), id)
		if err != nil {
			return fmt.Errorf("set embedding %s: %w", id, err)
		}
	}
	return tx.Commit(ctx)
}

// CountUnembedded reports how many chunks still need an embedding.
// Used by /healthz and tests to assert worker progress. Note this
// includes poison-pill rows (embed_attempts exhausted) — use
// CountEmbedExhausted to break that subset out.
func (s *Store) CountUnembedded(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM brain.wiki_chunks WHERE embedding IS NULL`).
		Scan(&n)
	return n, err
}

// CountEmbedExhausted reports chunks the embed worker has given up on
// (embed_attempts >= MaxEmbedAttempts, embedding still NULL) — the
// poison-pill backlog permanently excluded from reclaim. Observability
// only; the worker logs it when the claim queue runs dry.
func (s *Store) CountEmbedExhausted(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*) FROM brain.wiki_chunks
		  WHERE embedding IS NULL AND embed_attempts >= %d`,
		MaxEmbedAttempts)).Scan(&n)
	return n, err
}

// ─── ANN search ─────────────────────────────────────────────────

// SearchInput parameters for ANN cosine top-k.
type SearchInput struct {
	OwnerID        uuid.UUID  // restricts to projects owned by this user
	ProjectID      *uuid.UUID // optional narrowing
	QueryEmbedding []float32  // required
	Limit          int        // default 50; cap 200
	// MaxDistance lets callers cull weak matches before they pollute
	// fusion. Cosine distance ranges [0, 2]; 0.7 ≈ "weakly related".
	// Zero ⇒ default 0.7.
	MaxDistance float64
}

// Hit one ANN result. Score is `1 - cosine_distance` so higher is more
// similar — matches the convention BM25/SearxNG return with.
type Hit struct {
	ChunkID     string
	PageID      string
	BlockID     string // empty when chunk has no block_id (page-level)
	ProjectID   string
	Title       string
	HeadingPath string // section breadcrumb carried on the chunk row
	Snippet     string
	Score       float64
	UpdatedAt   time.Time
	TokenCount  int
}

// Search runs cosine ANN against brain.wiki_chunks, joining pages for
// title + ownership filtering. Returns hits ordered by similarity DESC.
func (s *Store) Search(ctx context.Context, in SearchInput) ([]Hit, error) {
	if len(in.QueryEmbedding) == 0 {
		return nil, fmt.Errorf("query embedding required")
	}
	if in.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("owner_id required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	maxDist := in.MaxDistance
	if maxDist <= 0 {
		maxDist = 0.7
	}
	vec := vectorLiteral(in.QueryEmbedding)

	// $1=qvec $2=owner $3=project (nullable) $4=max_dist $5=limit
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.page_id, c.block_id, c.project_id,
		       coalesce(p.title, '') AS title,
		       c.text, c.heading_path, c.token_count, p.updated_at,
		       (c.embedding <=> $1::vector) AS dist
		FROM brain.wiki_chunks c
		JOIN brain.pages p    ON p.id  = c.page_id
		JOIN brain.projects pr ON pr.id = c.project_id
		WHERE c.embedding IS NOT NULL
		  AND p.deleted_at IS NULL
		  AND pr.owner_id = $2
		  AND ($3::uuid IS NULL OR c.project_id = $3)
		  AND (c.embedding <=> $1::vector) <= $4
		ORDER BY c.embedding <=> $1::vector ASC
		LIMIT $5
	`, vec, in.OwnerID, nullableUUID(in.ProjectID), maxDist, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Hit, 0, limit)
	for rows.Next() {
		var (
			h           Hit
			cid         uuid.UUID
			pageID      uuid.UUID
			blockID     *uuid.UUID
			projID      uuid.UUID
			text        string
			headingPath string
			tokenCnt    int
			dist        float64
		)
		if err := rows.Scan(&cid, &pageID, &blockID, &projID,
			&h.Title, &text, &headingPath, &tokenCnt, &h.UpdatedAt, &dist); err != nil {
			return nil, err
		}
		h.ChunkID = cid.String()
		h.PageID = pageID.String()
		if blockID != nil {
			h.BlockID = blockID.String()
		}
		h.ProjectID = projID.String()
		h.HeadingPath = headingPath
		h.TokenCount = tokenCnt
		h.Score = 1.0 - dist
		// Trim snippet: 200 chars is enough to disambiguate a hit in UI;
		// full text stays in chunk row for callers that need it.
		snippet := text
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		h.Snippet = snippet
		out = append(out, h)
	}
	return out, rows.Err()
}

// ─── Helpers ────────────────────────────────────────────────────

// vectorLiteral renders a []float32 as the textual format pgvector
// understands: `[v1,v2,...]`. Same approach as memory/store/store.go so
// we don't depend on a pgvector-specific Go binding.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 9)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func nullableUUID(u *uuid.UUID) any {
	if u == nil || *u == uuid.Nil {
		return nil
	}
	return *u
}
