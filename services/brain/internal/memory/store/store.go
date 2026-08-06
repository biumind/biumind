// Package store is the data access layer for Brain.Memory.
//
// Memories are durable, kind-typed, project-scoped facts the agent
// should remember across sessions. Three kinds:
//
//	recall     — facts the user asked the agent to remember
//	preference — formatting / tone / process choices
//	habit      — inferred recurring patterns (renamed from "skill"
//	             in 2026-05; "skill" stays as a deprecated input alias
//	             until 2026-08-25). See docs/BiuMind-Skills-Design.md
//	             §11 for why.
//
// Embeddings are optional: the ingest worker (in this package's
// `worker.go`) backfills them asynchronously. Recall blends three
// signals when an embedding is provided:
//
//	semantic = 1 - (embedding <=> $query_vec)   (cosine similarity)
//	lexical  = 0.5 if lower(content) LIKE %q%   (sub-string match)
//	context  = salience + 30-day recency decay
//
// score = semantic + lexical + context, ORDER BY score DESC. When
// the query embedding is nil (provider not configured), the semantic
// term drops out and Recall behaves like the v1 lexical-only path.
package store

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

const (
	KindRecall     = "recall"
	KindPreference = "preference"
	KindHabit      = "habit"

	// KindSkill is the legacy alias for KindHabit.
	//
	// Deprecated: use KindHabit. The kind was renamed in 2026-05 when
	// the runtime.skills subsystem landed; the two senses of "skill"
	// became unsafe to share across services, telemetry, and Cedar
	// policies. Writes carrying "skill" are silently rewritten to
	// "habit" via NormalizeKind. Remove on 2026-08-25.
	KindSkill = "skill"
)

// ValidKind reports whether k is an accepted *input* kind. It includes
// the deprecated "skill" alias; use NormalizeKind to canonicalise to
// the value that actually persists.
func ValidKind(k string) bool {
	switch k {
	case KindRecall, KindPreference, KindHabit, KindSkill:
		return true
	}
	return false
}

// NormalizeKind maps an input kind to its canonical persisted value.
// The second return is true when the input used a deprecated alias —
// callers (typically the API layer) should emit a one-shot warning so
// clients have time to migrate before the alias is removed.
func NormalizeKind(k string) (canonical string, deprecated bool) {
	if k == KindSkill {
		return KindHabit, true
	}
	return k, false
}

type Memory struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	OwnerID        uuid.UUID
	Kind           string
	Content        string
	Salience       float32
	LastAccessedAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Score is populated by Recall; otherwise zero.
	Score float32
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// ─── Store / list / delete ──────────────────────────────────────

type StoreInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Kind      string
	Content   string
	// Salience defaults to 0.5 when zero.
	Salience float32
}

func (s *Store) Create(ctx context.Context, in StoreInput) (*Memory, error) {
	if in.ProjectID == uuid.Nil || in.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("project_id and owner_id required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, fmt.Errorf("content required")
	}
	kind := in.Kind
	if kind == "" {
		kind = KindRecall
	}
	if !ValidKind(kind) {
		return nil, fmt.Errorf("invalid kind %q", kind)
	}
	// Rewrite deprecated "skill" → "habit" before it hits the CHECK.
	// The API layer is the right place to log the deprecation; the
	// store layer just guarantees the persisted value is canonical.
	kind, _ = NormalizeKind(kind)
	salience := in.Salience
	if salience == 0 {
		salience = 0.5
	}
	m := &Memory{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.memories (project_id, owner_id, kind, content, salience)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, owner_id, kind, content, salience,
		          last_accessed_at, created_at, updated_at
	`, in.ProjectID, in.OwnerID, kind, in.Content, salience).Scan(
		&m.ID, &m.ProjectID, &m.OwnerID, &m.Kind, &m.Content, &m.Salience,
		&m.LastAccessedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}
	return m, nil
}

type ListInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Kind      string // empty = all kinds
	Limit     int
}

// List returns memories ordered by recency. Salience tie-breaks.
func (s *Store) List(ctx context.Context, in ListInput) ([]*Memory, error) {
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{in.ProjectID, in.OwnerID, limit}
	q := `
		SELECT id, project_id, owner_id, kind, content, salience,
		       last_accessed_at, created_at, updated_at
		FROM brain.memories
		WHERE project_id = $1 AND owner_id = $2`
	if in.Kind != "" {
		if !ValidKind(in.Kind) {
			return nil, fmt.Errorf("invalid kind %q", in.Kind)
		}
		k, _ := NormalizeKind(in.Kind)
		args = append(args, k)
		q += ` AND kind = $4`
	}
	q += ` ORDER BY last_accessed_at DESC, salience DESC LIMIT $3`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m := &Memory{}
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.OwnerID, &m.Kind, &m.Content,
			&m.Salience, &m.LastAccessedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM brain.memories WHERE id = $1 AND owner_id = $2`,
		id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Recall ─────────────────────────────────────────────────────

type RecallInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Query     string
	// QueryEmbedding is the embedding of Query, computed by the
	// caller (the API layer keeps the embedder so the store stays
	// transport-agnostic). When non-nil and ≥1 row in the project has
	// `embedding IS NOT NULL`, semantic similarity dominates ranking;
	// lexical match boosts the score; recency tie-breaks. When nil,
	// behaviour matches the v1 lexical-only path.
	QueryEmbedding []float32
	Kind           string
	Limit          int
}

// Recall ranks memories by a hybrid score:
//
//	score = semantic + lexical + salience + recency_boost
//
// Rows must satisfy AT LEAST ONE of:
//   - embedding similarity to QueryEmbedding ≥ 0.3 (i.e. cosine_distance ≤ 0.7)
//   - case-insensitive substring match
//
// so we don't return entirely-unrelated memories.
func (s *Store) Recall(ctx context.Context, in RecallInput) ([]*Memory, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("query required")
	}
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	pattern := "%" + strings.ToLower(in.Query) + "%"

	// Lexical-only fast path when no query embedding is supplied —
	// preserves the v1 behaviour for environments without an embedder.
	if len(in.QueryEmbedding) == 0 {
		return s.recallLexical(ctx, in, pattern, limit)
	}

	vec := vectorLiteral(in.QueryEmbedding)
	// $1=project $2=owner $3=pattern $4=qvec $5=limit ($6=kind)
	args := []any{in.ProjectID, in.OwnerID, pattern, vec, limit}
	q := `
		SELECT id, project_id, owner_id, kind, content, salience,
		       last_accessed_at, created_at, updated_at,
		       (
		         COALESCE(1.0 - (embedding <=> $4::vector), 0.0)
		         + CASE WHEN lower(content) LIKE $3 THEN 0.5 ELSE 0 END
		         + salience
		         + GREATEST(0,
		             1.0 - EXTRACT(EPOCH FROM (now() - last_accessed_at))
		                   / (30.0 * 86400.0))
		       )::real AS score
		FROM brain.memories
		WHERE project_id = $1 AND owner_id = $2
		  AND (
		        lower(content) LIKE $3
		     OR (embedding IS NOT NULL AND (embedding <=> $4::vector) <= 0.7)
		      )`
	if in.Kind != "" {
		if !ValidKind(in.Kind) {
			return nil, fmt.Errorf("invalid kind %q", in.Kind)
		}
		k, _ := NormalizeKind(in.Kind)
		args = append(args, k)
		q += ` AND kind = $6`
	}
	q += ` ORDER BY score DESC, last_accessed_at DESC LIMIT $5`

	return s.runRecall(ctx, q, args)
}

func (s *Store) recallLexical(ctx context.Context, in RecallInput, pattern string, limit int) ([]*Memory, error) {
	args := []any{in.ProjectID, in.OwnerID, pattern, limit}
	q := `
		SELECT id, project_id, owner_id, kind, content, salience,
		       last_accessed_at, created_at, updated_at,
		       (salience
		        + GREATEST(0,
		            1.0 - EXTRACT(EPOCH FROM (now() - last_accessed_at))
		                  / (30.0 * 86400.0)))::real AS score
		FROM brain.memories
		WHERE project_id = $1 AND owner_id = $2
		  AND lower(content) LIKE $3`
	if in.Kind != "" {
		if !ValidKind(in.Kind) {
			return nil, fmt.Errorf("invalid kind %q", in.Kind)
		}
		k, _ := NormalizeKind(in.Kind)
		args = append(args, k)
		q += ` AND kind = $5`
	}
	q += ` ORDER BY score DESC, last_accessed_at DESC LIMIT $4`
	return s.runRecall(ctx, q, args)
}

func (s *Store) runRecall(ctx context.Context, query string, args []any) ([]*Memory, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	var ids []uuid.UUID
	for rows.Next() {
		m := &Memory{}
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.OwnerID, &m.Kind, &m.Content,
			&m.Salience, &m.LastAccessedAt, &m.CreatedAt, &m.UpdatedAt,
			&m.Score); err != nil {
			return nil, err
		}
		out = append(out, m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		_, _ = s.pool.Exec(ctx,
			`UPDATE brain.memories SET last_accessed_at = now()
			   WHERE id = ANY($1)`, ids)
	}
	return out, nil
}

// vectorLiteral renders a []float32 as the textual format pgvector
// understands: `[v1,v2,...]`. We pass it as text and cast with `::vector`
// so we don't depend on a pgvector-specific Go binding.
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

// ─── Embedding backfill (worker) ────────────────────────────────

type PendingMemory struct {
	ID      uuid.UUID
	Content string
}

// ClaimUnembedded selects up to `batch` memories that still need an
// embedding and locks them with FOR UPDATE SKIP LOCKED so multiple
// brain replicas can run the worker concurrently without producing
// duplicate embedding work. Caller MUST commit the returned tx (via
// SetEmbeddings) so the locks release.
func (s *Store) ClaimUnembedded(ctx context.Context, batch int) ([]PendingMemory, pgx.Tx, error) {
	if batch <= 0 {
		batch = 32
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, content FROM brain.memories
		WHERE embedding IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, batch)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	defer rows.Close()
	var out []PendingMemory
	for rows.Next() {
		var p PendingMemory
		if err := rows.Scan(&p.ID, &p.Content); err != nil {
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

// SetEmbeddings writes the embeddings produced for a batch and commits
// the transaction returned by ClaimUnembedded. Vectors must match the
// pgvector column dimension; mismatches raise a Postgres-side error.
func (s *Store) SetEmbeddings(ctx context.Context, tx pgx.Tx, vecs map[uuid.UUID][]float32) error {
	defer func() { _ = tx.Rollback(ctx) }() // safe no-op after Commit
	for id, v := range vecs {
		if len(v) == 0 {
			continue
		}
		_, err := tx.Exec(ctx,
			`UPDATE brain.memories
			    SET embedding = $1::vector, updated_at = now()
			  WHERE id = $2`,
			vectorLiteral(v), id)
		if err != nil {
			return fmt.Errorf("set embedding %s: %w", id, err)
		}
	}
	return tx.Commit(ctx)
}

// CountUnembedded reports how many memories still need an embedding.
// Used by the worker for /healthz reporting and by tests for sync.
func (s *Store) CountUnembedded(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM brain.memories WHERE embedding IS NULL`).
		Scan(&n)
	return n, err
}

// Get returns a single memory, ownership-checked.
func (s *Store) Get(ctx context.Context, owner, id uuid.UUID) (*Memory, error) {
	m := &Memory{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, owner_id, kind, content, salience,
		       last_accessed_at, created_at, updated_at
		FROM brain.memories
		WHERE id = $1 AND owner_id = $2
	`, id, owner).Scan(
		&m.ID, &m.ProjectID, &m.OwnerID, &m.Kind, &m.Content, &m.Salience,
		&m.LastAccessedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}
