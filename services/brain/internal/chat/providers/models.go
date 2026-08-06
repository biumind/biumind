// Per-user model metadata + preferences.
//
// One row per (user_id, provider_id, model_id) combination. Stores:
//   * Display data — name, type, capability flags, context window
//   * Pricing payload (jsonb) for the UI's $/M chip
//   * enabled / sort_order for the model picker
//   * source = 'builtin' (our static catalog) | 'remote' (fetched from
//     provider /models endpoint) | 'custom' (user-added one-off)
//
// Builtin seeds are written by the application code from a static
// catalog so we don't need a separate migration each time the model
// roster updates.

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ModelTypeChat      = "chat"
	ModelTypeImage     = "image"
	ModelTypeVideo     = "video"
	ModelTypeEmbedding = "embedding"
	ModelTypeSTT       = "stt"
	ModelTypeTTS       = "tts"
)

func validModelType(t string) bool {
	switch t {
	case ModelTypeChat, ModelTypeImage, ModelTypeVideo,
		ModelTypeEmbedding, ModelTypeSTT, ModelTypeTTS:
		return true
	}
	return false
}

const (
	ModelSourceBuiltin = "builtin"
	ModelSourceRemote  = "remote"
	ModelSourceCustom  = "custom"
)

// Model is one row in chat.models, post-decoding.
type Model struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	ProviderID    string
	ModelID       string
	DisplayName   string
	Type          string
	Abilities     map[string]bool
	ContextWindow *int
	Pricing       map[string]any
	ReleasedAt    *time.Time
	Enabled       bool
	SortOrder     int
	Source        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ModelInput is what callers pass to upsert a model row. Pointer-y
// optional fields let us distinguish "not set" from "explicitly null"
// when needed.
type ModelInput struct {
	UserID        uuid.UUID
	ProviderID    string
	ModelID       string
	DisplayName   string
	Type          string
	Abilities     map[string]bool
	ContextWindow *int
	Pricing       map[string]any
	ReleasedAt    *time.Time
	Enabled       *bool
	SortOrder     int
	Source        string
}

// UpsertModel writes one model row. Idempotent on
// (user_id, provider_id, model_id) — used by both the static-catalog
// seed and remote-refresh paths.
func (s *Store) UpsertModel(ctx context.Context, in ModelInput) (*Model, error) {
	if in.UserID == uuid.Nil ||
		in.ProviderID == "" || in.ModelID == "" {
		return nil, fmt.Errorf("%w: user_id, provider_id, model_id required",
			ErrInvalid)
	}
	t := in.Type
	if t == "" {
		t = ModelTypeChat
	}
	if !validModelType(t) {
		return nil, fmt.Errorf("%w: bad type %q", ErrInvalid, t)
	}
	src := in.Source
	if src == "" {
		src = ModelSourceBuiltin
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	abilities := in.Abilities
	if abilities == nil {
		abilities = map[string]bool{}
	}
	abJSON, _ := json.Marshal(abilities)
	var pricingJSON []byte
	if in.Pricing != nil {
		pricingJSON, _ = json.Marshal(in.Pricing)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO chat.models
		    (user_id, provider_id, model_id, display_name, type,
		     abilities, context_window, pricing_json, released_at,
		     enabled, sort_order, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (user_id, provider_id, model_id) DO UPDATE SET
		    display_name   = EXCLUDED.display_name,
		    type           = EXCLUDED.type,
		    abilities      = EXCLUDED.abilities,
		    context_window = EXCLUDED.context_window,
		    pricing_json   = EXCLUDED.pricing_json,
		    released_at    = EXCLUDED.released_at,
		    -- Don't trample user's enabled/sort choices on remote refresh.
		    -- Source rotates so callers can tell where the row came from.
		    source         = EXCLUDED.source,
		    updated_at     = now()
		RETURNING id, user_id, provider_id, model_id, display_name, type,
		          abilities, context_window, pricing_json, released_at,
		          enabled, sort_order, source, created_at, updated_at
	`,
		in.UserID, in.ProviderID, in.ModelID, in.DisplayName, t,
		abJSON, in.ContextWindow, pricingJSON, in.ReleasedAt,
		enabled, in.SortOrder, src)
	return s.scanModel(row)
}

type ListModelsInput struct {
	UserID     uuid.UUID
	ProviderID string // optional filter
	Type       string // optional filter
	Enabled    *bool  // optional filter
}

func (s *Store) ListModels(ctx context.Context, in ListModelsInput) ([]*Model, error) {
	q := strings.Builder{}
	q.WriteString(`
		SELECT id, user_id, provider_id, model_id, display_name, type,
		       abilities, context_window, pricing_json, released_at,
		       enabled, sort_order, source, created_at, updated_at
		  FROM chat.models
		 WHERE user_id = $1`)
	args := []any{in.UserID}
	if in.ProviderID != "" {
		args = append(args, in.ProviderID)
		fmt.Fprintf(&q, " AND provider_id = $%d", len(args))
	}
	if in.Type != "" {
		args = append(args, in.Type)
		fmt.Fprintf(&q, " AND type = $%d", len(args))
	}
	if in.Enabled != nil {
		args = append(args, *in.Enabled)
		fmt.Fprintf(&q, " AND enabled = $%d", len(args))
	}
	q.WriteString(" ORDER BY sort_order ASC, model_id ASC")

	rows, err := s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Model{}
	for rows.Next() {
		m, err := s.scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetModelByID(ctx context.Context, userID, id uuid.UUID) (*Model, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, provider_id, model_id, display_name, type,
		       abilities, context_window, pricing_json, released_at,
		       enabled, sort_order, source, created_at, updated_at
		  FROM chat.models
		 WHERE id = $1 AND user_id = $2
	`, id, userID)
	m, err := s.scanModel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

type UpdateModelInput struct {
	UserID    uuid.UUID
	ID        uuid.UUID
	Enabled   *bool
	SortOrder *int
}

func (s *Store) UpdateModel(ctx context.Context, in UpdateModelInput) (*Model, error) {
	sets := []string{}
	args := []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Enabled != nil {
		add("enabled", *in.Enabled)
	}
	if in.SortOrder != nil {
		add("sort_order", *in.SortOrder)
	}
	if len(sets) == 0 {
		return s.GetModelByID(ctx, in.UserID, in.ID)
	}
	add("updated_at", time.Now().UTC())
	args = append(args, in.ID, in.UserID)
	q := fmt.Sprintf(`
		UPDATE chat.models SET %s
		 WHERE id = $%d AND user_id = $%d
		RETURNING id, user_id, provider_id, model_id, display_name, type,
		          abilities, context_window, pricing_json, released_at,
		          enabled, sort_order, source, created_at, updated_at
	`, strings.Join(sets, ", "), len(args)-1, len(args))

	row := s.pool.QueryRow(ctx, q, args...)
	m, err := s.scanModel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// PruneRemoteModels 删掉 (user, providerID, source='remote') 行里 model_id
// **不在** keepModelIDs 里的所有行。给 BiuMind Cloud 同步用 ——
// admin 把某模型从 active 切到 disabled / 删除时,该用户老的 chat.models
// remote 行要清理掉, 否则 picker 会列已下架的模型, 选了 model-relay 会
// 拒。
//
// keepModelIDs 空 → 删该 (user, provider, remote) 全部行。这是 admin
// 把 model-relay active 列表清空(或 brain 拉到了空)时的正常行为。
//
// source='remote' 守门防止误删 builtin (静态 catalog 写的) / custom
// (用户手加的) 行 — 这些不是 admin 同步过来的。
func (s *Store) PruneRemoteModels(
	ctx context.Context, userID uuid.UUID, providerID string, keepModelIDs []string,
) (int, error) {
	if userID == uuid.Nil || providerID == "" {
		return 0, fmt.Errorf("%w: user_id, provider_id required", ErrInvalid)
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM chat.models
		 WHERE user_id     = $1
		   AND provider_id = $2
		   AND source      = 'remote'
		   AND NOT (model_id = ANY($3::text[]))
	`, userID, providerID, keepModelIDs)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) DeleteModel(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM chat.models WHERE id = $1 AND user_id = $2`,
		id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) scanModel(r row) (*Model, error) {
	var m Model
	var abJSON, pricingJSON []byte
	var ctx *int
	var released *time.Time
	if err := r.Scan(
		&m.ID, &m.UserID, &m.ProviderID, &m.ModelID, &m.DisplayName, &m.Type,
		&abJSON, &ctx, &pricingJSON, &released,
		&m.Enabled, &m.SortOrder, &m.Source, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	m.ContextWindow = ctx
	m.ReleasedAt = released
	if len(abJSON) > 0 {
		_ = json.Unmarshal(abJSON, &m.Abilities)
	}
	if m.Abilities == nil {
		m.Abilities = map[string]bool{}
	}
	if len(pricingJSON) > 0 {
		_ = json.Unmarshal(pricingJSON, &m.Pricing)
	}
	return &m, nil
}
