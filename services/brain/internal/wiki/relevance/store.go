// Persistence + lookup for brain.page_relevance.
package relevance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// Related is one row joined with the neighbour page's title for UI.
type Related struct {
	OtherPageID uuid.UUID
	Title       string
	Score       float32
	Signals     map[string]float32
}

// ListRelated returns the top-K related pages for `pageID`, ordered by
// score DESC. Signals are decoded into a typed map for callers that
// want to render per-signal contribution badges.
func (s *Store) ListRelated(ctx context.Context, pageID uuid.UUID, limit int) ([]Related, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// UNION ALL over the two index directions then ORDER BY + LIMIT.
	// pgvector / btree planner picks the right index per branch
	// because each branch has a column-equality predicate the index
	// matches exactly.
	rows, err := s.pool.Query(ctx, `
		SELECT other_id, title, score, signals
		FROM (
		  SELECT pr.page_b AS other_id, p.title, pr.score, pr.signals
		    FROM brain.page_relevance pr
		    JOIN brain.pages p ON p.id = pr.page_b
		   WHERE pr.page_a = $1 AND p.deleted_at IS NULL
		  UNION ALL
		  SELECT pr.page_a AS other_id, p.title, pr.score, pr.signals
		    FROM brain.page_relevance pr
		    JOIN brain.pages p ON p.id = pr.page_a
		   WHERE pr.page_b = $1 AND p.deleted_at IS NULL
		) u
		ORDER BY score DESC
		LIMIT $2
	`, pageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Related
	for rows.Next() {
		var (
			r       Related
			signals []byte
		)
		if err := rows.Scan(&r.OtherPageID, &r.Title, &r.Score, &signals); err != nil {
			return nil, err
		}
		var raw map[string]float64
		_ = json.Unmarshal(signals, &raw)
		r.Signals = make(map[string]float32, len(raw))
		for k, v := range raw {
			r.Signals[k] = float32(v)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceProject atomically replaces every relevance row for one
// project with `pairs`. Worker invokes this after a fresh ScoreAll —
// stale rows from removed pages or shrunk wikilink graphs vanish in
// the same transaction that lands the new ones.
func (s *Store) ReplaceProject(ctx context.Context, projectID uuid.UUID, pairs []PairScore) (int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM brain.page_relevance WHERE project_id = $1`, projectID); err != nil {
		return 0, fmt.Errorf("delete prior relevance: %w", err)
	}
	if len(pairs) == 0 {
		return 0, tx.Commit(ctx)
	}

	// Bulk insert with VALUES placeholders — one round trip beats
	// per-row Exec on dense projects.
	const cols = 5
	args := make([]any, 0, len(pairs)*cols)
	values := make([]string, 0, len(pairs))
	for i, p := range pairs {
		base := i * cols
		values = append(values, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d::jsonb)",
			base+1, base+2, base+3, base+4, base+5))
		signals, _ := json.Marshal(p.Signals)
		args = append(args, projectID, p.PageA, p.PageB, p.Score, signals)
	}
	q := `INSERT INTO brain.page_relevance
	      (project_id, page_a, page_b, score, signals)
	      VALUES ` + strings.Join(values, ",")
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("insert relevance: %w", err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
