// ModelRepo is the CRUD surface for model_relay.models.
//
// Two non-trivial spots:
//
//  1. Capabilities and UpstreamRef are jsonb columns; we marshal/unmarshal
//     in-package so callers see typed Go structs.
//
//  2. ListVisibleTo is the runtime hot path used by ModelResolver to
//     filter "what models can plan=X user with groups=[...] see". It's
//     here rather than in resolver.go because it's a SQL concern (one
//     query with min_plan + group join).

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelRepo struct {
	pool *pgxpool.Pool
}

type ModelFilter struct {
	Status  EntityStatus
	Family  string
	MinPlan Plan
	Search  string // ILIKE on code or display_name

	// Mode 过滤 (P4 follow-up F1). 单值 = 等于; 逗号分隔多值用 IN.
	// 空 = 全部 mode. 例: "image_generation" / "image_generation,video_generation"
	Mode string

	// 分页. Limit<=0 表示不分页 (兼容老调用方,如 cache.go 的全量 warm-up).
	// Offset 仅在 Limit>0 时生效.
	Limit  int
	Offset int
}

// splitModes 把 ModelFilter.Mode 字段拆成数组。空 Mode -> 空数组 (= 全部 mode)。
func (f ModelFilter) splitModes() []string {
	modes := []string{}
	if f.Mode != "" {
		for _, m := range strings.Split(f.Mode, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				modes = append(modes, m)
			}
		}
	}
	return modes
}

func (r *ModelRepo) List(ctx context.Context, f ModelFilter) ([]Model, error) {
	modes := f.splitModes()
	// LIMIT/OFFSET 用 NULLIF 把 0 当成无限 (cache warm-up 那种全量场景).
	q := `
		SELECT id, code, display_name, family, context_window, max_output,
		       capabilities, min_plan, status, sort_order,
		       upstream_ref, manual_override, routing_strategy,
		       mode, pricing_strategy, dispatch_mode,
		       fallback_models, is_default_chat,
		       created_at, updated_at
		FROM model_relay.models
		WHERE ($1 = '' OR status   = $1)
		  AND ($2 = '' OR family   = $2)
		  AND ($3 = '' OR min_plan = $3)
		  AND ($4 = '' OR code ILIKE '%' || $4 || '%' OR display_name ILIKE '%' || $4 || '%')
		  AND (cardinality($5::text[]) = 0 OR mode = ANY($5::text[]))
		ORDER BY sort_order ASC, code ASC
		LIMIT NULLIF($6::int, 0) OFFSET $7
	`
	limit := f.Limit
	if limit < 0 {
		limit = 0
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := r.pool.Query(ctx, q,
		string(f.Status), f.Family, string(f.MinPlan), f.Search, modes, limit, offset)
	if err != nil {
		return nil, translateErr("models.list", err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// Count 返回与 List 同样过滤条件下的总条数, 不受 Limit/Offset 影响.
// 用于分页 UI 的 total 显示.
func (r *ModelRepo) Count(ctx context.Context, f ModelFilter) (int, error) {
	modes := f.splitModes()
	const q = `
		SELECT COUNT(*) FROM model_relay.models
		WHERE ($1 = '' OR status   = $1)
		  AND ($2 = '' OR family   = $2)
		  AND ($3 = '' OR min_plan = $3)
		  AND ($4 = '' OR code ILIKE '%' || $4 || '%' OR display_name ILIKE '%' || $4 || '%')
		  AND (cardinality($5::text[]) = 0 OR mode = ANY($5::text[]))
	`
	var n int
	if err := r.pool.QueryRow(ctx, q,
		string(f.Status), f.Family, string(f.MinPlan), f.Search, modes).Scan(&n); err != nil {
		return 0, translateErr("models.count", err)
	}
	return n, nil
}

func (r *ModelRepo) Get(ctx context.Context, id uuid.UUID) (*Model, error) {
	const q = `
		SELECT id, code, display_name, family, context_window, max_output,
		       capabilities, min_plan, status, sort_order,
		       upstream_ref, manual_override, routing_strategy,
		       mode, pricing_strategy, dispatch_mode,
		       fallback_models, is_default_chat,
		       created_at, updated_at
		FROM model_relay.models WHERE id = $1
	`
	rows, err := r.pool.Query(ctx, q, id)
	if err != nil {
		return nil, translateErr("models.get", err)
	}
	defer rows.Close()
	out, err := scanModels(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("models.get: %w", ErrNotFound)
	}
	return &out[0], nil
}

func (r *ModelRepo) GetByCode(ctx context.Context, code string) (*Model, error) {
	const q = `
		SELECT id, code, display_name, family, context_window, max_output,
		       capabilities, min_plan, status, sort_order,
		       upstream_ref, manual_override, routing_strategy,
		       mode, pricing_strategy, dispatch_mode,
		       fallback_models, is_default_chat,
		       created_at, updated_at
		FROM model_relay.models WHERE code = $1
	`
	rows, err := r.pool.Query(ctx, q, code)
	if err != nil {
		return nil, translateErr("models.get_by_code", err)
	}
	defer rows.Close()
	out, err := scanModels(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("models.get_by_code: %w", ErrNotFound)
	}
	return &out[0], nil
}

// ListVisibleTo is the resolver hot path: returns active models the
// user can see given (plan, accessible group ids). At MVP all users
// are members of the 'default' group implicitly, so the typical call
// passes []uuid.UUID{DefaultGroupID}.
//
// The "user can see model X" predicate:
//
//	X.status = 'active'
//	AND plan_rank(user_plan)   >= plan_rank(X.min_plan)
//	AND EXISTS (
//	  SELECT 1 FROM model_group_bindings b
//	  WHERE b.model_id = X.id AND b.group_id = ANY($groups)
//	)
//
// plan_rank is encoded inline via CASE so we don't need a function.
func (r *ModelRepo) ListVisibleTo(ctx context.Context, userPlan Plan, accessibleGroups []uuid.UUID) ([]Model, error) {
	q := `
		SELECT m.id, m.code, m.display_name, m.family, m.context_window, m.max_output,
		       m.capabilities, m.min_plan, m.status, m.sort_order,
		       m.upstream_ref, m.manual_override, m.routing_strategy,
		       m.mode, m.pricing_strategy, m.dispatch_mode,
		       m.fallback_models, m.is_default_chat,
		       m.created_at, m.updated_at
		FROM model_relay.models m
		WHERE m.status = 'active'
		  AND CASE m.min_plan WHEN 'free' THEN 0 WHEN 'pro' THEN 1 WHEN 'team' THEN 2 ELSE 99 END
		    <= CASE $1::text   WHEN 'free' THEN 0 WHEN 'pro' THEN 1 WHEN 'team' THEN 2 ELSE -1 END
		  AND EXISTS (
		    SELECT 1 FROM model_relay.model_group_bindings b
		    WHERE b.model_id = m.id AND b.group_id = ANY($2::uuid[])
		  )
		ORDER BY m.sort_order ASC, m.code ASC
	`
	rows, err := r.pool.Query(ctx, q, string(userPlan), accessibleGroups)
	if err != nil {
		return nil, translateErr("models.list_visible_to", err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// ModelInput is the writable shape. ID, timestamps, upstream_ref are
// either server-assigned or set via separate sync paths; this struct
// is what admin "create / edit" forms produce.
type ModelInput struct {
	Code            string
	DisplayName     string
	Family          string
	ContextWindow   int
	MaxOutput       int
	Capabilities    Capabilities
	MinPlan         Plan
	Status          EntityStatus
	SortOrder       int
	RoutingStrategy RoutingStrategy
	ManualOverride  bool
	// Mode 对应 model_relay.models.mode (10 枚举 v0.3, 见 types.go:ModeChat...).
	// 空值 = 让仓库层填 ModeChat (向后兼容老调用方)。
	Mode string
	// FallbackModels v0.3 — 主渠道全部失败时按数组顺序尝试的备用 model code.
	// 不设 = 空数组, 不影响现有行为.
	FallbackModels []string
	// IsDefaultChat marks this model as the platform default chat model
	// (migration 00002). Setting true clears the flag on every other
	// model inside the same transaction. Only mode='chat' models may be
	// marked — enforced at the adminapi layer (400).
	IsDefaultChat bool
}

func (in ModelInput) validate() error {
	if in.Code == "" {
		return fmt.Errorf("models: code required")
	}
	if in.DisplayName == "" {
		return fmt.Errorf("models: display_name required")
	}
	switch in.MinPlan {
	case PlanFree, PlanPro, PlanTeam:
	default:
		return fmt.Errorf("models: invalid min_plan %q", in.MinPlan)
	}
	if in.Mode != "" && !IsValidMode(in.Mode) {
		return fmt.Errorf("models: invalid mode %q", in.Mode)
	}
	return nil
}

func (r *ModelRepo) Insert(ctx context.Context, in ModelInput) (*Model, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusDisabled
	}
	if in.RoutingStrategy == "" {
		in.RoutingStrategy = StrategyWeighted
	}
	if in.Mode == "" {
		in.Mode = ModeChat
	}
	caps, err := json.Marshal(in.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("models.insert.caps: %w", err)
	}
	if in.FallbackModels == nil {
		in.FallbackModels = []string{}
	}
	const q = `
		INSERT INTO model_relay.models
			(code, display_name, family, context_window, max_output,
			 capabilities, min_plan, status, sort_order,
			 manual_override, routing_strategy, mode, fallback_models,
			 is_default_chat)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, code, display_name, family, context_window, max_output,
		          capabilities, min_plan, status, sort_order,
		          upstream_ref, manual_override, routing_strategy,
		          mode, pricing_strategy, dispatch_mode,
		          fallback_models, is_default_chat,
		          created_at, updated_at
	`
	return r.withDefaultChatTx(ctx, uuid.Nil, in.IsDefaultChat,
		func(ctx context.Context, qx queryer) (*Model, error) {
			rows, err := qx.Query(ctx, q,
				in.Code, in.DisplayName, in.Family, in.ContextWindow, in.MaxOutput,
				caps, in.MinPlan, in.Status, in.SortOrder,
				in.ManualOverride, in.RoutingStrategy, in.Mode, in.FallbackModels,
				in.IsDefaultChat,
			)
			if err != nil {
				return nil, translateErr("models.insert", err)
			}
			defer rows.Close()
			out, err := scanModels(rows)
			if err != nil {
				return nil, err
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("models.insert: no row returned")
			}
			return &out[0], nil
		})
}

func (r *ModelRepo) Update(ctx context.Context, id uuid.UUID, in ModelInput) (*Model, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.RoutingStrategy == "" {
		in.RoutingStrategy = StrategyWeighted
	}
	if in.Status == "" {
		in.Status = StatusDisabled
	}
	if in.Mode == "" {
		in.Mode = ModeChat
	}
	if in.FallbackModels == nil {
		in.FallbackModels = []string{}
	}
	caps, err := json.Marshal(in.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("models.update.caps: %w", err)
	}
	const q = `
		UPDATE model_relay.models
		   SET code = $1, display_name = $2, family = $3,
		       context_window = $4, max_output = $5,
		       capabilities = $6, min_plan = $7, status = $8,
		       sort_order = $9, manual_override = $10, routing_strategy = $11,
		       mode = $12, fallback_models = $13, is_default_chat = $14,
		       updated_at = now()
		 WHERE id = $15
		RETURNING id, code, display_name, family, context_window, max_output,
		          capabilities, min_plan, status, sort_order,
		          upstream_ref, manual_override, routing_strategy,
		          mode, pricing_strategy, dispatch_mode,
		          fallback_models, is_default_chat,
		          created_at, updated_at
	`
	return r.withDefaultChatTx(ctx, id, in.IsDefaultChat,
		func(ctx context.Context, qx queryer) (*Model, error) {
			rows, err := qx.Query(ctx, q,
				in.Code, in.DisplayName, in.Family,
				in.ContextWindow, in.MaxOutput,
				caps, in.MinPlan, in.Status,
				in.SortOrder, in.ManualOverride, in.RoutingStrategy, in.Mode,
				in.FallbackModels, in.IsDefaultChat, id,
			)
			if err != nil {
				return nil, translateErr("models.update", err)
			}
			defer rows.Close()
			out, err := scanModels(rows)
			if err != nil {
				return nil, err
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("models.update: %w", ErrNotFound)
			}
			return &out[0], nil
		})
}

// queryer is satisfied by both *pgxpool.Pool and pgx.Tx so Insert/Update
// can run standalone or inside the clear-default transaction.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// withDefaultChatTx runs fn directly on the pool when setDefault is
// false. When true it wraps fn in a transaction that first clears
// is_default_chat on every other model (excluding excludeID), so the
// "at most one default" invariant holds even without the partial unique
// index models_default_chat_unique_idx — the index is the race backstop.
func (r *ModelRepo) withDefaultChatTx(ctx context.Context, excludeID uuid.UUID, setDefault bool,
	fn func(ctx context.Context, qx queryer) (*Model, error)) (*Model, error) {
	if !setDefault {
		return fn(ctx, r.pool)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("models.begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`UPDATE model_relay.models SET is_default_chat = false, updated_at = now()
		 WHERE is_default_chat AND id <> $1`, excludeID); err != nil {
		return nil, translateErr("models.clear_default_chat", err)
	}
	m, err := fn(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, translateErr("models.commit", err)
	}
	return m, nil
}

// SetStatus is the lightweight toggle used by the admin "enable/disable"
// switch — avoids constructing a full ModelInput.
func (r *ModelRepo) SetStatus(ctx context.Context, id uuid.UUID, status EntityStatus) error {
	const q = `UPDATE model_relay.models SET status = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, q, status, id)
	if err != nil {
		return translateErr("models.set_status", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("models.set_status: %w", ErrNotFound)
	}
	return nil
}

func (r *ModelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM model_relay.models WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return translateErr("models.delete", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("models.delete: %w", ErrNotFound)
	}
	return nil
}

// SetUpstreamRef stamps the upstream_ref jsonb. Used only by the
// sync-upstream path; admin UI never touches this.
func (r *ModelRepo) SetUpstreamRef(ctx context.Context, id uuid.UUID, ref *UpstreamRef) error {
	var raw []byte
	if ref != nil {
		var err error
		raw, err = json.Marshal(ref)
		if err != nil {
			return fmt.Errorf("models.set_upstream_ref: %w", err)
		}
	}
	const q = `UPDATE model_relay.models SET upstream_ref = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, q, raw, id)
	if err != nil {
		return translateErr("models.set_upstream_ref", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("models.set_upstream_ref: %w", ErrNotFound)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────

// scanModels handles the row → Model conversion + jsonb columns.
// Both List and Insert/Update paths funnel through here so the
// jsonb decode logic stays in one place.
func scanModels(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Model, error) {
	out := make([]Model, 0, 16)
	for rows.Next() {
		var m Model
		var caps []byte
		var ref []byte
		if err := rows.Scan(
			&m.ID, &m.Code, &m.DisplayName, &m.Family,
			&m.ContextWindow, &m.MaxOutput,
			&caps, &m.MinPlan, &m.Status, &m.SortOrder,
			&ref, &m.ManualOverride, &m.RoutingStrategy,
			&m.Mode, &m.PricingStrategy, &m.DispatchMode,
			&m.FallbackModels, &m.IsDefaultChat,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("models.scan: %w", err)
		}
		if len(caps) > 0 {
			if err := json.Unmarshal(caps, &m.Capabilities); err != nil {
				return nil, fmt.Errorf("models.scan.caps: %w", err)
			}
		}
		if len(ref) > 0 {
			var u UpstreamRef
			if err := json.Unmarshal(ref, &u); err != nil {
				return nil, fmt.Errorf("models.scan.upstream_ref: %w", err)
			}
			m.UpstreamRef = &u
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
