// Package providers manages per-user LLM provider configurations.
//
// Schema lives in chat.providers (migration 00008_providers.sql).
// Each row is one provider config for one user. P3 removed the key
// storage (key_vaults_encrypted), fetch_mode and internal columns —
// brain no longer holds user credentials; keys live in identity
// (user_api_keys) and are fetched on demand via IdentityBYOKClient.
// This table now keeps only provider metadata + the model catalog
// host (chat.models references provider_id).
//
// Owner-scoping convention matches the rest of chat.*: cross-tenant
// reads return ErrNotFound (not 403) so we don't leak existence.
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
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("providers: not found")
	ErrConflict = errors.New("providers: provider_id already exists for this user")
	ErrInvalid  = errors.New("providers: invalid input")
)

const (
	SourceBuiltin  = "builtin"
	SourceCustom   = "custom"
	SourceOfficial = "official"
)

func validSource(s string) bool {
	return s == SourceBuiltin || s == SourceCustom || s == SourceOfficial
}

// Provider is one user's configuration of one LLM provider.
//
// P3: 不再持有 APIKey / FetchMode / Internal. 用户凭据归 identity,
// 需要时由 IdentityBYOKClient 现取 (见 refresh.go / agentplane/byok.go).
type Provider struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	ProviderID  string // 'anthropic' | 'openai' | custom slug
	DisplayName string
	BaseURL     *string
	Enabled     bool
	Source      string
	Config      map[string]any
	SortOrder   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store is the data-access type for provider configs.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ─── CRUD ────────────────────────────────────────────────────

type CreateInput struct {
	UserID      uuid.UUID
	ProviderID  string // required
	DisplayName string
	BaseURL     *string
	Enabled     *bool          // defaults to true
	Source      string         // "builtin" | "custom"; defaults to "builtin"
	Config      map[string]any // optional
}

func (s *Store) Create(ctx context.Context, in CreateInput) (*Provider, error) {
	if in.UserID == uuid.Nil {
		return nil, fmt.Errorf("%w: user_id required", ErrInvalid)
	}
	if strings.TrimSpace(in.ProviderID) == "" {
		return nil, fmt.Errorf("%w: provider_id required", ErrInvalid)
	}
	src := in.Source
	if src == "" {
		src = SourceBuiltin
	}
	if !validSource(src) {
		return nil, fmt.Errorf("%w: bad source %q", ErrInvalid, src)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	cfg := in.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("providers: marshal config: %w", err)
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO chat.providers
		    (user_id, provider_id, display_name, base_url,
		     enabled, source, config_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, user_id, provider_id, display_name, base_url,
		          enabled, source, config_json, sort_order, created_at, updated_at
	`, in.UserID, in.ProviderID, in.DisplayName, in.BaseURL, enabled, src, cfgJSON)

	p, err := s.scan(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return p, nil
}

type UpdateInput struct {
	UserID      uuid.UUID
	ID          uuid.UUID
	DisplayName *string
	BaseURL     *string
	Enabled     *bool
	Config      map[string]any
}

func (s *Store) Update(ctx context.Context, in UpdateInput) (*Provider, error) {
	sets := []string{}
	args := []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.DisplayName != nil {
		add("display_name", *in.DisplayName)
	}
	if in.BaseURL != nil {
		add("base_url", *in.BaseURL)
	}
	if in.Enabled != nil {
		add("enabled", *in.Enabled)
	}
	if in.Config != nil {
		cfgJSON, err := json.Marshal(in.Config)
		if err != nil {
			return nil, err
		}
		add("config_json", cfgJSON)
	}
	if len(sets) == 0 {
		// No-op update — return the row as-is.
		return s.GetByID(ctx, in.UserID, in.ID)
	}
	add("updated_at", time.Now().UTC())
	// WHERE bindings
	args = append(args, in.ID, in.UserID)
	q := fmt.Sprintf(`
		UPDATE chat.providers
		   SET %s
		 WHERE id = $%d AND user_id = $%d
		RETURNING id, user_id, provider_id, display_name, base_url,
		          enabled, source, config_json, sort_order, created_at, updated_at
	`, strings.Join(sets, ", "), len(args)-1, len(args))

	row := s.pool.QueryRow(ctx, q, args...)
	p, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM chat.providers WHERE id = $1 AND user_id = $2`,
		id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetByID(ctx context.Context, userID, id uuid.UUID) (*Provider, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, provider_id, display_name, base_url,
		       enabled, source, config_json, sort_order, created_at, updated_at
		  FROM chat.providers
		 WHERE id = $1 AND user_id = $2
	`, id, userID)
	p, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetByProviderID is the runtime lookup: given a provider_id slug
// (e.g. "anthropic" derived from a model row), return the user's
// configured row, or ErrNotFound. Used by agentplane.ResolveBYOKCreds
// to read provider metadata (enabled/source); the actual key comes
// from identity (P3).
func (s *Store) GetByProviderID(ctx context.Context, userID uuid.UUID, providerID string) (*Provider, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, provider_id, display_name, base_url,
		       enabled, source, config_json, sort_order, created_at, updated_at
		  FROM chat.providers
		 WHERE user_id = $1 AND provider_id = $2
	`, userID, providerID)
	p, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// EnsureOfficial inserts the BiuMind Cloud provider row for the user
// if it doesn't already exist. Idempotent — called from List() so
// the user's first settings load auto-creates it.
//
// Official providers carry no key: dispatch goes through model-relay's
// platform pool and is gated by subscription / metered billing
// (handled outside this store).
func (s *Store) EnsureOfficial(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat.providers
		    (user_id, provider_id, display_name, enabled, source, sort_order)
		VALUES ($1, 'biumind-official', 'BiuMind Cloud', true, 'official', -1)
		ON CONFLICT (user_id, provider_id) DO NOTHING
	`, userID)
	return err
}

func (s *Store) List(ctx context.Context, userID uuid.UUID) ([]*Provider, error) {
	// Lazy seed:auto-create the official row on first list. This way
	// new users land on Settings and immediately see BiuMind Cloud
	// without an explicit provisioning step.
	if err := s.EnsureOfficial(ctx, userID); err != nil {
		return nil, fmt.Errorf("providers: ensure official: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, provider_id, display_name, base_url,
		       enabled, source, config_json, sort_order, created_at, updated_at
		  FROM chat.providers
		 WHERE user_id = $1
		 ORDER BY sort_order ASC, created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Provider{}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// row generalises pgx.Row / pgx.Rows so scan can serve both.
type row interface {
	Scan(dest ...any) error
}

func (s *Store) scan(r row) (*Provider, error) {
	var p Provider
	var cfgJSON []byte
	if err := r.Scan(
		&p.ID, &p.UserID, &p.ProviderID, &p.DisplayName, &p.BaseURL,
		&p.Enabled, &p.Source, &cfgJSON,
		&p.SortOrder, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(cfgJSON) > 0 {
		_ = json.Unmarshal(cfgJSON, &p.Config)
	}
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	return &p, nil
}

func isUniqueViolation(err error) bool {
	// Avoid pulling in pgconn just for the SQLSTATE check; substring
	// match is good enough for our single FK case.
	return err != nil && strings.Contains(err.Error(), "providers_user_provider_uniq")
}
