// PostgreSQL store for rss.watch_rules + rss.watch_hits. Schema is
// in services/app_center/migrations/00006_rss_schema.sql.

package radar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

// rssHitSummary is a re-export of rss.HitSummary so the SDK is the
// single source of truth for the join projection. Kept as a type alias
// (not just a redeclaration) so additions in the SDK are picked up
// automatically.
type rssHitSummary = rss.HitSummary

var (
	ErrNotFound  = errors.New("radar: not found")
	ErrEmptyRule = errors.New("radar: rule must have at least one match_any/match_all keyword")
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const ruleCols = `id, scope, scope_id, name, match_any, match_all, exclude,
	sources, on_hit_badge, on_hit_notify, cooldown_sec, enabled,
	created_at, updated_at,
	COALESCE(semantic_query, ''),
	COALESCE(semantic_threshold, 0.78),
	COALESCE(actions, '[]'::jsonb)`

func scanRule(r pgx.Rows) (*Rule, error) {
	var ru Rule
	if err := r.Scan(
		&ru.ID, &ru.Scope, &ru.ScopeID, &ru.Name,
		&ru.MatchAny, &ru.MatchAll, &ru.Exclude,
		&ru.Sources, &ru.OnHitBadge, &ru.OnHitNotify,
		&ru.CooldownSec, &ru.Enabled,
		&ru.CreatedAt, &ru.UpdatedAt,
		&ru.SemanticQuery, &ru.SemanticThreshold, &ru.Actions,
	); err != nil {
		return nil, err
	}
	return &ru, nil
}

// ─── rules CRUD ───────────────────────────────────────────────────

type CreateRuleInput struct {
	Scope             string
	ScopeID           string
	Name              string
	MatchAny          []string
	MatchAll          []string
	Exclude           []string
	Sources           []string
	OnHitBadge        string
	OnHitNotify       []string
	CooldownSec       int
	SemanticQuery     string  // M4: free-text intent
	SemanticThreshold float32 // M4: cosine threshold (default 0.78)
	Actions           []byte  // M4: jsonb action recipe array
}

func (s *Store) CreateRule(ctx context.Context, in CreateRuleInput) (*Rule, error) {
	// M4: a rule is valid as long as it has SOMETHING — keyword OR
	// semantic query.
	if len(in.MatchAny) == 0 && len(in.MatchAll) == 0 && in.SemanticQuery == "" {
		return nil, ErrEmptyRule
	}
	if in.Sources == nil {
		in.Sources = []string{"*"}
	}
	if in.OnHitBadge == "" {
		in.OnHitBadge = "warn"
	}
	if in.CooldownSec <= 0 {
		in.CooldownSec = 1800
	}
	if in.OnHitNotify == nil {
		in.OnHitNotify = []string{}
	}
	if in.MatchAny == nil {
		in.MatchAny = []string{}
	}
	if in.MatchAll == nil {
		in.MatchAll = []string{}
	}
	if in.Exclude == nil {
		in.Exclude = []string{}
	}
	if in.SemanticThreshold == 0 {
		in.SemanticThreshold = 0.78
	}
	if len(in.Actions) == 0 {
		in.Actions = []byte("[]")
	}

	rows, err := s.pool.Query(ctx, `
		INSERT INTO rss.watch_rules
			(scope, scope_id, name, match_any, match_all, exclude,
			 sources, on_hit_badge, on_hit_notify, cooldown_sec,
			 semantic_query, semantic_threshold, actions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
		        NULLIF($11,''), $12, $13)
		RETURNING `+ruleCols,
		in.Scope, in.ScopeID, in.Name,
		in.MatchAny, in.MatchAll, in.Exclude,
		in.Sources, in.OnHitBadge, in.OnHitNotify, in.CooldownSec,
		in.SemanticQuery, in.SemanticThreshold, in.Actions)
	if err != nil {
		return nil, fmt.Errorf("radar: create rule: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("radar: create rule: no row")
	}
	return scanRule(rows)
}

func (s *Store) ListRules(ctx context.Context, scope, scopeID string) ([]*Rule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+ruleCols+`
		  FROM rss.watch_rules
		 WHERE scope=$1 AND scope_id=$2
		 ORDER BY created_at DESC`, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("radar: list rules: %w", err)
	}
	defer rows.Close()
	out := make([]*Rule, 0)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListEnabledRulesAll returns every enabled rule across all scopes.
// Used by the matcher fan-out path: rules table is small (well under
// O(1k) per deployment) so a full scan is faster than a per-scope
// query inside a hot loop.
func (s *Store) ListEnabledRulesAll(ctx context.Context) ([]*Rule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+ruleCols+`
		  FROM rss.watch_rules
		 WHERE enabled = true`)
	if err != nil {
		return nil, fmt.Errorf("radar: list all rules: %w", err)
	}
	defer rows.Close()
	out := make([]*Rule, 0)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRule(ctx context.Context, id uuid.UUID) (*Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM rss.watch_rules WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("radar: get rule: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanRule(rows)
}

type UpdateRuleInput struct {
	Name              *string
	MatchAny          *[]string
	MatchAll          *[]string
	Exclude           *[]string
	Sources           *[]string
	OnHitBadge        *string
	OnHitNotify       *[]string
	CooldownSec       *int
	Enabled           *bool
	SemanticQuery     *string
	SemanticThreshold *float32
	Actions           *[]byte
}

func (s *Store) UpdateRule(ctx context.Context, scope, scopeID string, id uuid.UUID, in UpdateRuleInput) (*Rule, error) {
	// Build the SET list dynamically. Tedious but clear; alternatives
	// (struct + reflect) hide the exact SQL.
	setParts := []string{"updated_at = now()"}
	args := []any{id, scope, scopeID}
	add := func(col string, v any) {
		args = append(args, v)
		setParts = append(setParts, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Name != nil {
		add("name", *in.Name)
	}
	if in.MatchAny != nil {
		add("match_any", *in.MatchAny)
	}
	if in.MatchAll != nil {
		add("match_all", *in.MatchAll)
	}
	if in.Exclude != nil {
		add("exclude", *in.Exclude)
	}
	if in.Sources != nil {
		add("sources", *in.Sources)
	}
	if in.OnHitBadge != nil {
		add("on_hit_badge", *in.OnHitBadge)
	}
	if in.OnHitNotify != nil {
		add("on_hit_notify", *in.OnHitNotify)
	}
	if in.CooldownSec != nil {
		add("cooldown_sec", *in.CooldownSec)
	}
	if in.Enabled != nil {
		add("enabled", *in.Enabled)
	}
	if in.SemanticQuery != nil {
		v := *in.SemanticQuery
		if v == "" {
			add("semantic_query", nil)
		} else {
			add("semantic_query", v)
		}
	}
	if in.SemanticThreshold != nil {
		add("semantic_threshold", *in.SemanticThreshold)
	}
	if in.Actions != nil {
		add("actions", *in.Actions)
	}
	q := `UPDATE rss.watch_rules SET ` + joinComma(setParts) +
		` WHERE id=$1 AND scope=$2 AND scope_id=$3 RETURNING ` + ruleCols
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("radar: update rule: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanRule(rows)
}

func (s *Store) DeleteRule(ctx context.Context, scope, scopeID string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rss.watch_rules WHERE id=$1 AND scope=$2 AND scope_id=$3`,
		id, scope, scopeID)
	if err != nil {
		return fmt.Errorf("radar: delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── hits write + cooldown ────────────────────────────────────────

// FilterCooldown drops hits whose (rule_id, title_hash) was last
// fired within the rule's cooldown window. Returns the surviving
// subset preserved in original order.
func (s *Store) FilterCooldown(ctx context.Context, hits []Hit) ([]Hit, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		var lastAt *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT MAX(hit_at) FROM rss.watch_hits
			 WHERE rule_id=$1 AND title_hash=$2`,
			h.RuleID, h.TitleHash,
		).Scan(&lastAt)
		if err != nil {
			return nil, fmt.Errorf("radar: cooldown query: %w", err)
		}
		if lastAt != nil && time.Since(*lastAt) < time.Duration(h.RuleSnapshot.CooldownSec)*time.Second {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// WriteHits batch-inserts hits and returns them with their assigned
// IDs + hit_at timestamps populated. Errors abort the whole batch.
func (s *Store) WriteHits(ctx context.Context, hits []Hit) ([]Hit, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("radar: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	out := make([]Hit, len(hits))
	for i, h := range hits {
		err := tx.QueryRow(ctx, `
			INSERT INTO rss.watch_hits
				(rule_id, source, title, url, title_hash)
			VALUES ($1,$2,$3,$4,$5)
			RETURNING id, hit_at`,
			h.RuleID, h.Source, h.Title, h.URL, h.TitleHash,
		).Scan(&out[i].ID, &out[i].HitAt)
		if err != nil {
			return nil, fmt.Errorf("radar: insert hit: %w", err)
		}
		out[i].RuleID = h.RuleID
		out[i].Source = h.Source
		out[i].Title = h.Title
		out[i].URL = h.URL
		out[i].TitleHash = h.TitleHash
		out[i].RuleSnapshot = h.RuleSnapshot
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("radar: commit: %w", err)
	}
	return out, nil
}

// MarkHitNotified flips the notified flag after a successful dispatch.
// Failure to flip (notify succeeded, DB write failed) is non-critical
// — the next dispatch loop will redo the notify; the user might see
// a duplicate but that's acceptable vs. dropping the alert.
func (s *Store) MarkHitNotified(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE rss.watch_hits SET notified=true WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("radar: mark notified: %w", err)
	}
	return nil
}

// ─── hits queries (UI) ────────────────────────────────────────────

const hitCols = `h.id, h.rule_id, h.hit_at, h.source, h.title,
	COALESCE(h.url, ''), h.title_hash, h.notified,
	COALESCE(h.read_at, '0001-01-01'::timestamptz)`

type ListHitsOpts struct {
	RuleID     uuid.UUID // zero = all rules in scope
	UnreadOnly bool
	Limit      int
}

// ListHitsWithRule returns the SDK projection joining hits with their
// originating rule's name + on_hit_badge so the timeline UI doesn't
// need a second round-trip per row.
func (s *Store) ListHitsWithRule(ctx context.Context, scope, scopeID string, opts ListHitsOpts) ([]*rssHitSummary, error) {
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 100
	}
	q := `SELECT h.id, h.rule_id, h.hit_at, h.source, h.title,
	             COALESCE(h.url, ''), h.notified, (h.read_at IS NOT NULL) AS read,
	             r.name, r.on_hit_badge
	         FROM rss.watch_hits h
	         JOIN rss.watch_rules r ON r.id = h.rule_id
	        WHERE r.scope = $1 AND r.scope_id = $2`
	args := []any{scope, scopeID}
	if opts.RuleID != uuid.Nil {
		args = append(args, opts.RuleID)
		q += fmt.Sprintf(" AND h.rule_id = $%d", len(args))
	}
	if opts.UnreadOnly {
		q += ` AND h.read_at IS NULL`
	}
	args = append(args, opts.Limit)
	q += fmt.Sprintf(` ORDER BY h.hit_at DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("radar: list hits: %w", err)
	}
	defer rows.Close()
	out := make([]*rssHitSummary, 0)
	for rows.Next() {
		var h rssHitSummary
		if err := rows.Scan(
			&h.ID, &h.RuleID, &h.HitAt, &h.Source, &h.Title,
			&h.URL, &h.Notified, &h.Read, &h.RuleName, &h.HitSeverity,
		); err != nil {
			return nil, err
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

func (s *Store) ListHits(ctx context.Context, scope, scopeID string, opts ListHitsOpts) ([]*Hit, error) {
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 100
	}
	q := `SELECT ` + hitCols + `
	         FROM rss.watch_hits h
	         JOIN rss.watch_rules r ON r.id = h.rule_id
	        WHERE r.scope = $1 AND r.scope_id = $2`
	args := []any{scope, scopeID}
	if opts.RuleID != uuid.Nil {
		args = append(args, opts.RuleID)
		q += fmt.Sprintf(" AND h.rule_id = $%d", len(args))
	}
	if opts.UnreadOnly {
		q += ` AND h.read_at IS NULL`
	}
	args = append(args, opts.Limit)
	q += fmt.Sprintf(` ORDER BY h.hit_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("radar: list hits: %w", err)
	}
	defer rows.Close()
	out := make([]*Hit, 0)
	for rows.Next() {
		var h Hit
		if err := rows.Scan(
			&h.ID, &h.RuleID, &h.HitAt, &h.Source, &h.Title,
			&h.URL, &h.TitleHash, &h.Notified, &h.ReadAt,
		); err != nil {
			return nil, err
		}
		if h.ReadAt.Year() < 2 {
			h.ReadAt = time.Time{}
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

func (s *Store) MarkHitRead(ctx context.Context, scope, scopeID string, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE rss.watch_hits SET read_at = now()
		  FROM rss.watch_rules r
		 WHERE rss.watch_hits.rule_id = r.id
		   AND rss.watch_hits.id = $1
		   AND r.scope = $2 AND r.scope_id = $3`,
		id, scope, scopeID)
	if err != nil {
		return fmt.Errorf("radar: mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UnreadCount returns total unread hits across all rules in scope.
func (s *Store) UnreadCount(ctx context.Context, scope, scopeID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		  FROM rss.watch_hits h
		  JOIN rss.watch_rules r ON r.id = h.rule_id
		 WHERE r.scope = $1 AND r.scope_id = $2
		   AND h.read_at IS NULL`,
		scope, scopeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("radar: unread count: %w", err)
	}
	return n, nil
}

// UnreadMaxSeverity returns the highest severity of any unread hit's
// originating rule. Empty when there are no unread hits.
func (s *Store) UnreadMaxSeverity(ctx context.Context, scope, scopeID string) (string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT r.on_hit_badge
		  FROM rss.watch_hits h
		  JOIN rss.watch_rules r ON r.id = h.rule_id
		 WHERE r.scope = $1 AND r.scope_id = $2
		   AND h.read_at IS NULL`, scope, scopeID)
	if err != nil {
		return "", fmt.Errorf("radar: unread severity: %w", err)
	}
	defer rows.Close()
	worst := ""
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		worst = MaxSeverity(worst, s)
	}
	return worst, rows.Err()
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
