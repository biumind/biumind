// ProviderRepo is the CRUD surface for model_relay.providers.
//
// Providers are an admin-managed catalogue of upstream LLM services
// (OpenAI, Anthropic, a specific Azure deployment, etc.). Each row is
// referenced by 1-N Credentials. Deletion is RESTRICTed at the FK level
// so an admin can't accidentally orphan credentials by removing a
// provider — the UI must surface "delete all credentials first".

package registry

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProviderRepo struct {
	pool *pgxpool.Pool
}

// ProviderFilter narrows List results. Empty filter returns everything.
type ProviderFilter struct {
	Status   EntityStatus // "" matches any
	Protocol ProviderProtocol
	Search   string // case-insensitive substring on code or name
}

func (r *ProviderRepo) List(ctx context.Context, f ProviderFilter) ([]Provider, error) {
	q := `
		SELECT id, code, name, protocol, icon, description, status, created_at, updated_at
		FROM model_relay.providers
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR protocol = $2)
		  AND ($3 = '' OR code ILIKE '%' || $3 || '%' OR name ILIKE '%' || $3 || '%')
		ORDER BY code ASC
	`
	rows, err := r.pool.Query(ctx, q,
		string(f.Status), string(f.Protocol), f.Search)
	if err != nil {
		return nil, translateErr("providers.list", err)
	}
	defer rows.Close()

	out := make([]Provider, 0, 16)
	for rows.Next() {
		var p Provider
		if err := rows.Scan(
			&p.ID, &p.Code, &p.Name, &p.Protocol,
			&p.Icon, &p.Description, &p.Status,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, translateErr("providers.list.scan", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProviderRepo) Get(ctx context.Context, id uuid.UUID) (*Provider, error) {
	const q = `
		SELECT id, code, name, protocol, icon, description, status, created_at, updated_at
		FROM model_relay.providers WHERE id = $1
	`
	var p Provider
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.Code, &p.Name, &p.Protocol,
		&p.Icon, &p.Description, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("providers.get", err)
	}
	return &p, nil
}

func (r *ProviderRepo) GetByCode(ctx context.Context, code string) (*Provider, error) {
	const q = `
		SELECT id, code, name, protocol, icon, description, status, created_at, updated_at
		FROM model_relay.providers WHERE code = $1
	`
	var p Provider
	err := r.pool.QueryRow(ctx, q, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Protocol,
		&p.Icon, &p.Description, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("providers.get_by_code", err)
	}
	return &p, nil
}

// ProviderInput is the writable shape for Insert / Update. ID and
// timestamps are server-assigned.
type ProviderInput struct {
	Code        string
	Name        string
	Protocol    ProviderProtocol
	Icon        string
	Description string
	Status      EntityStatus
}

func (in ProviderInput) validate() error {
	if in.Code == "" {
		return fmt.Errorf("providers: code required")
	}
	if in.Name == "" {
		return fmt.Errorf("providers: name required")
	}
	switch in.Protocol {
	case ProtocolOpenAICompat, ProtocolAnthropic, ProtocolDashScope, ProtocolVolcEngine:
		// ok
	default:
		return fmt.Errorf("providers: invalid protocol %q", in.Protocol)
	}
	return nil
}

func (r *ProviderRepo) Insert(ctx context.Context, in ProviderInput) (*Provider, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	const q = `
		INSERT INTO model_relay.providers
			(code, name, protocol, icon, description, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, code, name, protocol, icon, description, status, created_at, updated_at
	`
	var p Provider
	err := r.pool.QueryRow(ctx, q,
		in.Code, in.Name, in.Protocol,
		in.Icon, in.Description, in.Status,
	).Scan(
		&p.ID, &p.Code, &p.Name, &p.Protocol,
		&p.Icon, &p.Description, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("providers.insert", err)
	}
	return &p, nil
}

func (r *ProviderRepo) Update(ctx context.Context, id uuid.UUID, in ProviderInput) (*Provider, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	const q = `
		UPDATE model_relay.providers
		   SET code = $1, name = $2, protocol = $3,
		       icon = $4, description = $5, status = $6,
		       updated_at = now()
		 WHERE id = $7
		RETURNING id, code, name, protocol, icon, description, status, created_at, updated_at
	`
	var p Provider
	err := r.pool.QueryRow(ctx, q,
		in.Code, in.Name, in.Protocol,
		in.Icon, in.Description, in.Status, id,
	).Scan(
		&p.ID, &p.Code, &p.Name, &p.Protocol,
		&p.Icon, &p.Description, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("providers.update", err)
	}
	return &p, nil
}

// Delete removes a provider. The credentials FK is RESTRICT, so this
// returns a conflict error if any credential still references the row.
// Callers (admin handler) translate this to a 409 with hint
// "delete dependent credentials first".
func (r *ProviderRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM model_relay.providers WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return translateErr("providers.delete", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("providers.delete: %w", ErrNotFound)
	}
	return nil
}

// txQueryRow is a small helper for handlers that want to compose a
// provider lookup inside a larger transaction (e.g. sync-upstream).
func (r *ProviderRepo) GetByCodeTx(ctx context.Context, tx pgx.Tx, code string) (*Provider, error) {
	const q = `
		SELECT id, code, name, protocol, icon, description, status, created_at, updated_at
		FROM model_relay.providers WHERE code = $1
	`
	var p Provider
	err := tx.QueryRow(ctx, q, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Protocol,
		&p.Icon, &p.Description, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("providers.get_by_code_tx", err)
	}
	return &p, nil
}
