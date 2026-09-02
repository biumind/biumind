// Package reviews owns the brain.review_items table — the unified
// "automated wiki audit" queue. dedup is the first producer (P2-D);
// lint / sweep / merge / suggestion follow.
//
// The store layer here is intentionally generic: it doesn't know what
// "dedup" means. Detection logic lives in dedup.go (and future
// lint.go, sweep.go ...) and writes through Upsert; the store just
// keeps the table sane and offers list / status-transition primitives.
package reviews

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

const (
	KindDedup         = "dedup"
	KindLint          = "lint"
	KindSweep         = "sweep"
	KindMerge         = "merge"
	KindSuggestion    = "suggestion"
	KindContradiction = "contradiction"

	StatusOpen      = "open"
	StatusResolved  = "resolved"
	StatusDismissed = "dismissed"
)

func ValidKind(k string) bool {
	switch k {
	case KindDedup, KindLint, KindSweep, KindMerge, KindSuggestion, KindContradiction:
		return true
	}
	return false
}

func ValidStatus(s string) bool {
	switch s {
	case StatusOpen, StatusResolved, StatusDismissed:
		return true
	}
	return false
}

// IsTerminal reports whether the status is an end state. Detection
// workers skip rows in terminal states so a user-resolved dedup
// suggestion doesn't keep getting re-flagged.
func IsTerminal(s string) bool {
	return s == StatusResolved || s == StatusDismissed
}

type Item struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	OwnerID     uuid.UUID
	Kind        string
	Status      string
	Title       string
	Description string
	PageIDs     []uuid.UUID
	Payload     map[string]any
	DedupeKey   string
	ResolvedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// UpsertInput is the create-or-skip-on-conflict input. The
// dedupe_key drives the UNIQUE collision: re-running a detection on
// stable inputs writes nothing the second time. Status / page_ids /
// payload on existing rows stay untouched.
type UpsertInput struct {
	ProjectID   uuid.UUID
	OwnerID     uuid.UUID
	Kind        string
	Title       string
	Description string
	PageIDs     []uuid.UUID
	Payload     map[string]any
	DedupeKey   string
}

// Upsert returns (item, created). created=false means the row already
// existed (status may be open OR terminal — workers should check).
func (s *Store) Upsert(ctx context.Context, in UpsertInput) (*Item, bool, error) {
	if in.ProjectID == uuid.Nil || in.OwnerID == uuid.Nil {
		return nil, false, fmt.Errorf("project_id and owner_id required")
	}
	if !ValidKind(in.Kind) {
		return nil, false, fmt.Errorf("invalid kind %q", in.Kind)
	}
	if strings.TrimSpace(in.DedupeKey) == "" {
		return nil, false, fmt.Errorf("dedupe_key required")
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	pageIDs := in.PageIDs
	if pageIDs == nil {
		pageIDs = []uuid.UUID{}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	// First check whether the dedupe_key already exists. We do this
	// instead of ON CONFLICT DO UPDATE because we want the create
	// vs. existing distinction — callers (workers, metrics) care about
	// "actually new finding" rates.
	existing := &Item{}
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, owner_id, kind, status, title, description,
		       page_ids, payload, dedupe_key, resolved_at, created_at, updated_at
		FROM brain.review_items WHERE dedupe_key = $1
	`, in.DedupeKey).Scan(
		&existing.ID, &existing.ProjectID, &existing.OwnerID,
		&existing.Kind, &existing.Status, &existing.Title, &existing.Description,
		&existing.PageIDs, &existing.Payload, &existing.DedupeKey,
		&existing.ResolvedAt, &existing.CreatedAt, &existing.UpdatedAt,
	)
	if err == nil {
		// Row exists — return as-is.
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, false, cerr
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	created := &Item{}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.review_items
		    (project_id, owner_id, kind, title, description,
		     page_ids, payload, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, project_id, owner_id, kind, status, title, description,
		          page_ids, payload, dedupe_key, resolved_at, created_at, updated_at
	`, in.ProjectID, in.OwnerID, in.Kind, in.Title, in.Description,
		pageIDs, payload, in.DedupeKey).Scan(
		&created.ID, &created.ProjectID, &created.OwnerID,
		&created.Kind, &created.Status, &created.Title, &created.Description,
		&created.PageIDs, &created.Payload, &created.DedupeKey,
		&created.ResolvedAt, &created.CreatedAt, &created.UpdatedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert review_item: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return created, true, nil
}

// ListInput parameters for List.
type ListInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Kind      string // empty = all kinds
	Status    string // empty = all (typically callers want "open")
	Limit     int
}

// List returns review items for one project, newest first.
// Caller must enforce project ownership before calling (the store
// only filters by project_id; owner_id check stays at the API layer).
func (s *Store) List(ctx context.Context, in ListInput) ([]*Item, error) {
	if in.ProjectID == uuid.Nil {
		return nil, fmt.Errorf("project_id required")
	}
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{in.ProjectID, limit}
	q := `
		SELECT id, project_id, owner_id, kind, status, title, description,
		       page_ids, payload, dedupe_key, resolved_at, created_at, updated_at
		FROM brain.review_items
		WHERE project_id = $1`
	if in.Kind != "" {
		if !ValidKind(in.Kind) {
			return nil, fmt.Errorf("invalid kind %q", in.Kind)
		}
		args = append(args, in.Kind)
		q += fmt.Sprintf(" AND kind = $%d", len(args))
	}
	if in.Status != "" {
		if !ValidStatus(in.Status) {
			return nil, fmt.Errorf("invalid status %q", in.Status)
		}
		args = append(args, in.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	q += " ORDER BY created_at DESC LIMIT $2"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it := &Item{}
		if err := rows.Scan(
			&it.ID, &it.ProjectID, &it.OwnerID, &it.Kind, &it.Status,
			&it.Title, &it.Description, &it.PageIDs, &it.Payload,
			&it.DedupeKey, &it.ResolvedAt, &it.CreatedAt, &it.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RuleCount is one row of a per-rule histogram.
type RuleCount struct {
	Kind   string
	RuleID string
	Count  int
}

// CountOpenByRule groups OPEN review items by (kind, payload->>'rule_id')
// for one project. Used by the cleanup dashboard to render summary
// cards ("12 个孤儿页面 / 3 个空页面 / 5 条死链").
//
// rule_id lives inside payload, not as its own column — payload is
// where lint/sweep stash all the rule-specific fields. We extract it
// here so the UI doesn't need a second round-trip per group.
func (s *Store) CountOpenByRule(ctx context.Context, projectID uuid.UUID) ([]RuleCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, COALESCE(payload->>'rule_id', '') AS rule_id, COUNT(*)
		FROM brain.review_items
		WHERE project_id = $1
		  AND status = 'open'
		GROUP BY kind, rule_id
		ORDER BY kind, rule_id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleCount
	for rows.Next() {
		var rc RuleCount
		if err := rows.Scan(&rc.Kind, &rc.RuleID, &rc.Count); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// Get returns one row by id. Owner check stays at the API layer.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Item, error) {
	it := &Item{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, owner_id, kind, status, title, description,
		       page_ids, payload, dedupe_key, resolved_at, created_at, updated_at
		FROM brain.review_items WHERE id = $1
	`, id).Scan(
		&it.ID, &it.ProjectID, &it.OwnerID, &it.Kind, &it.Status,
		&it.Title, &it.Description, &it.PageIDs, &it.Payload,
		&it.DedupeKey, &it.ResolvedAt, &it.CreatedAt, &it.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

// SetStatus transitions a review to resolved/dismissed. Idempotent:
// terminal-to-same-terminal returns nil. Cross-terminal transitions
// (resolved → dismissed) are rejected as a likely API misuse.
func (s *Store) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if !ValidStatus(status) || status == StatusOpen {
		return fmt.Errorf("status must be resolved or dismissed")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.review_items
		   SET status = $2,
		       resolved_at = now(),
		       updated_at = now()
		 WHERE id = $1 AND status = 'open'
	`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either not found, or already terminal. Disambiguate so the
		// API layer can return 404 vs 409 cleanly.
		var cur string
		if qerr := s.pool.QueryRow(ctx,
			`SELECT status FROM brain.review_items WHERE id = $1`, id,
		).Scan(&cur); qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return qerr
		}
		if cur == status {
			return nil
		}
		return fmt.Errorf("review already %s", cur)
	}
	return nil
}

// IDByDedupeKey returns the row id for a given dedupe_key, or
// uuid.Nil when no row matches. Used by the page-merger flow to
// auto-resolve the relevant dedup review without exposing the row's
// internal scan (which would race with concurrent worker writes).
func (s *Store) IDByDedupeKey(ctx context.Context, key string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM brain.review_items WHERE dedupe_key = $1`, key).
		Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

// CountOpen returns the open-review count for a project. Used by UI
// badges and by the dedup worker to decide whether to skip a project
// (e.g. cap the open queue at N to avoid swamping the user).
func (s *Store) CountOpen(ctx context.Context, projectID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM brain.review_items
		 WHERE project_id = $1 AND status = 'open'
	`, projectID).Scan(&n)
	return n, err
}

// ListOpenByKinds returns open items for one project filtered to the
// given kinds, oldest first (deterministic processing order for the
// lint worker's auto-resolve pass — P2 #20 ①).
func (s *Store) ListOpenByKinds(ctx context.Context, projectID uuid.UUID, kinds []string) ([]*Item, error) {
	if projectID == uuid.Nil {
		return nil, fmt.Errorf("project_id required")
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	for _, k := range kinds {
		if !ValidKind(k) {
			return nil, fmt.Errorf("invalid kind %q", k)
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, owner_id, kind, status, title, description,
		       page_ids, payload, dedupe_key, resolved_at, created_at, updated_at
		FROM brain.review_items
		WHERE project_id = $1 AND status = 'open' AND kind = ANY($2)
		ORDER BY created_at
	`, projectID, kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it := &Item{}
		if err := rows.Scan(
			&it.ID, &it.ProjectID, &it.OwnerID, &it.Kind, &it.Status,
			&it.Title, &it.Description, &it.PageIDs, &it.Payload,
			&it.DedupeKey, &it.ResolvedAt, &it.CreatedAt, &it.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
