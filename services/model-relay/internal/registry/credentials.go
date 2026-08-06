// CredentialRepo is the CRUD surface for model_relay.credentials.
//
// IMPORTANT: this layer stores envelope-encrypted bytes verbatim. The
// envelope.Envelope wrapping/unwrapping is the caller's job (see
// internal/registry/credential_envelope.go for the helper that combines
// envelope + repo into one Save/Reveal pair). Repos themselves are
// dumb byte movers — keeps this file independent of crypto concerns
// and makes Repo unit tests trivial.
//
// Admin DTOs MUST go through NewCredentialSafe (types.go) so the four
// envelope byte fields never leak to JSON. Repo callers occasionally
// need the full Credential (e.g. ModelResolver decrypts at request
// time); only those paths use the *Credential return shape.

package registry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CredentialRepo struct {
	pool *pgxpool.Pool
}

type CredentialFilter struct {
	ProviderID uuid.UUID    // uuid.Nil matches any
	Status     EntityStatus // "" matches any
	Search     string       // ILIKE on label
}

func (r *CredentialRepo) List(ctx context.Context, f CredentialFilter) ([]Credential, error) {
	q := `
		SELECT id, provider_id, label,
		       ciphertext, wrapped_dek, iv, wrap_iv,
		       key_preview, base_url, header_override,
		       status, last_test_at, last_test_error,
		       created_at, updated_at
		FROM model_relay.credentials
		WHERE ($1::uuid IS NULL OR provider_id = $1)
		  AND ($2 = ''       OR status = $2)
		  AND ($3 = ''       OR label ILIKE '%' || $3 || '%')
		ORDER BY created_at DESC
	`
	var pid any
	if f.ProviderID != uuid.Nil {
		pid = f.ProviderID
	}
	rows, err := r.pool.Query(ctx, q, pid, string(f.Status), f.Search)
	if err != nil {
		return nil, translateErr("credentials.list", err)
	}
	defer rows.Close()

	out := make([]Credential, 0, 16)
	for rows.Next() {
		var c Credential
		var headerJSON []byte
		if err := rows.Scan(
			&c.ID, &c.ProviderID, &c.Label,
			&c.Ciphertext, &c.WrappedDEK, &c.IV, &c.WrapIV,
			&c.KeyPreview, &c.BaseURL, &headerJSON,
			&c.Status, &c.LastTestAt, &c.LastTestError,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, translateErr("credentials.list.scan", err)
		}
		if err := unmarshalHeaderOverride(headerJSON, &c.HeaderOverride); err != nil {
			return nil, fmt.Errorf("credentials.list.header: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListSafe returns scrubbed views in one round-trip — the common case
// for admin list endpoints. Saves the caller from a List + map.
func (r *CredentialRepo) ListSafe(ctx context.Context, f CredentialFilter) ([]CredentialSafe, error) {
	full, err := r.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]CredentialSafe, 0, len(full))
	for i := range full {
		out = append(out, NewCredentialSafe(&full[i]))
	}
	return out, nil
}

func (r *CredentialRepo) Get(ctx context.Context, id uuid.UUID) (*Credential, error) {
	const q = `
		SELECT id, provider_id, label,
		       ciphertext, wrapped_dek, iv, wrap_iv,
		       key_preview, base_url, header_override,
		       status, last_test_at, last_test_error,
		       created_at, updated_at
		FROM model_relay.credentials WHERE id = $1
	`
	var c Credential
	var headerJSON []byte
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&c.ID, &c.ProviderID, &c.Label,
		&c.Ciphertext, &c.WrappedDEK, &c.IV, &c.WrapIV,
		&c.KeyPreview, &c.BaseURL, &headerJSON,
		&c.Status, &c.LastTestAt, &c.LastTestError,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("credentials.get", err)
	}
	if err := unmarshalHeaderOverride(headerJSON, &c.HeaderOverride); err != nil {
		return nil, fmt.Errorf("credentials.get.header: %w", err)
	}
	return &c, nil
}

// CredentialInput is the writable shape. The envelope fields are raw
// bytes — caller must already have run keys.Envelope.Encrypt and put
// the components here. label / provider_id / preview / base_url /
// header_override are plain admin-supplied data.
type CredentialInput struct {
	ProviderID     uuid.UUID
	Label          string
	Ciphertext     []byte
	WrappedDEK     []byte
	IV             []byte
	WrapIV         []byte
	KeyPreview     string
	BaseURL        string
	HeaderOverride map[string]string
	Status         EntityStatus
}

func (in CredentialInput) validate() error {
	if in.ProviderID == uuid.Nil {
		return fmt.Errorf("credentials: provider_id required")
	}
	if in.Label == "" {
		return fmt.Errorf("credentials: label required")
	}
	if len(in.Ciphertext) == 0 || len(in.WrappedDEK) == 0 ||
		len(in.IV) == 0 || len(in.WrapIV) == 0 {
		return fmt.Errorf("credentials: envelope fields required")
	}
	return nil
}

func (r *CredentialRepo) Insert(ctx context.Context, in CredentialInput) (*Credential, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	headerJSON, err := marshalHeaderOverride(in.HeaderOverride)
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO model_relay.credentials
			(provider_id, label,
			 ciphertext, wrapped_dek, iv, wrap_iv,
			 key_preview, base_url, header_override, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, provider_id, label,
		          ciphertext, wrapped_dek, iv, wrap_iv,
		          key_preview, base_url, header_override,
		          status, last_test_at, last_test_error,
		          created_at, updated_at
	`
	var c Credential
	var outHeader []byte
	err = r.pool.QueryRow(ctx, q,
		in.ProviderID, in.Label,
		in.Ciphertext, in.WrappedDEK, in.IV, in.WrapIV,
		in.KeyPreview, in.BaseURL, headerJSON, in.Status,
	).Scan(
		&c.ID, &c.ProviderID, &c.Label,
		&c.Ciphertext, &c.WrappedDEK, &c.IV, &c.WrapIV,
		&c.KeyPreview, &c.BaseURL, &outHeader,
		&c.Status, &c.LastTestAt, &c.LastTestError,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("credentials.insert", err)
	}
	if err := unmarshalHeaderOverride(outHeader, &c.HeaderOverride); err != nil {
		return nil, fmt.Errorf("credentials.insert.header: %w", err)
	}
	return &c, nil
}

// CredentialPatch is for partial updates — admin "update label" without
// re-pasting the key, or rotation that re-pastes only ciphertext et al.
// Nil fields are left unchanged. Label and BaseURL use *string so empty
// vs unset is distinguishable.
type CredentialPatch struct {
	Label          *string
	BaseURL        *string
	HeaderOverride map[string]string // nil = unchanged; empty map = clear
	Status         *EntityStatus

	// Rotate the encrypted material — all four MUST be set together
	// (caller can't legally rotate ciphertext without a new DEK / IV).
	Ciphertext []byte
	WrappedDEK []byte
	IV         []byte
	WrapIV     []byte
	KeyPreview *string

	LastTestAt    *interface{} // pointer-to-anything; we use *time.Time elsewhere but interface kept simple here
	LastTestError *string
}

// PatchTestResult is a focused helper used by health probes to stamp
// last_test_at / last_test_error in one call without touching the
// general Patch path.
func (r *CredentialRepo) PatchTestResult(ctx context.Context, id uuid.UUID, errMsg string, success bool) error {
	const q = `
		UPDATE model_relay.credentials
		   SET last_test_at  = now(),
		       last_test_error = $1,
		       status = CASE WHEN $2 AND status = 'invalid' THEN 'active' ELSE status END,
		       updated_at = now()
		 WHERE id = $3
	`
	tag, err := r.pool.Exec(ctx, q, errMsg, success, id)
	if err != nil {
		return translateErr("credentials.patch_test_result", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credentials.patch_test_result: %w", ErrNotFound)
	}
	return nil
}

// Update replaces ALL writable fields. CredentialPatch (above) is
// described but not implemented in MVP — admin UI does full PUT and
// the rotation path is a separate handler that re-encrypts then calls
// Update. Keeping a single simple Update keeps the layer thin.
type CredentialUpdate struct {
	Label          string
	BaseURL        string
	HeaderOverride map[string]string
	Status         EntityStatus

	// Set re-rotation fields together; all four nil = keep existing.
	Ciphertext []byte
	WrappedDEK []byte
	IV         []byte
	WrapIV     []byte
	KeyPreview string // only used if rotating
}

func (r *CredentialRepo) Update(ctx context.Context, id uuid.UUID, u CredentialUpdate) (*Credential, error) {
	if u.Label == "" {
		return nil, fmt.Errorf("credentials: label required")
	}
	rotating := len(u.Ciphertext) > 0
	if rotating && (len(u.WrappedDEK) == 0 || len(u.IV) == 0 || len(u.WrapIV) == 0) {
		return nil, fmt.Errorf("credentials: rotation requires all envelope fields")
	}
	headerJSON, err := marshalHeaderOverride(u.HeaderOverride)
	if err != nil {
		return nil, err
	}

	// Two query shapes — one keeps the old ciphertext (label-only edit),
	// one rotates. Cleaner than COALESCE on each column.
	var (
		q    string
		args []any
	)
	if rotating {
		q = `
			UPDATE model_relay.credentials
			   SET label = $1, base_url = $2, header_override = $3, status = $4,
			       ciphertext = $5, wrapped_dek = $6, iv = $7, wrap_iv = $8,
			       key_preview = $9, updated_at = now()
			 WHERE id = $10
			RETURNING id, provider_id, label,
			          ciphertext, wrapped_dek, iv, wrap_iv,
			          key_preview, base_url, header_override,
			          status, last_test_at, last_test_error,
			          created_at, updated_at
		`
		args = []any{
			u.Label, u.BaseURL, headerJSON, u.Status,
			u.Ciphertext, u.WrappedDEK, u.IV, u.WrapIV,
			u.KeyPreview, id,
		}
	} else {
		q = `
			UPDATE model_relay.credentials
			   SET label = $1, base_url = $2, header_override = $3, status = $4,
			       updated_at = now()
			 WHERE id = $5
			RETURNING id, provider_id, label,
			          ciphertext, wrapped_dek, iv, wrap_iv,
			          key_preview, base_url, header_override,
			          status, last_test_at, last_test_error,
			          created_at, updated_at
		`
		args = []any{u.Label, u.BaseURL, headerJSON, u.Status, id}
	}

	var c Credential
	var outHeader []byte
	err = r.pool.QueryRow(ctx, q, args...).Scan(
		&c.ID, &c.ProviderID, &c.Label,
		&c.Ciphertext, &c.WrappedDEK, &c.IV, &c.WrapIV,
		&c.KeyPreview, &c.BaseURL, &outHeader,
		&c.Status, &c.LastTestAt, &c.LastTestError,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("credentials.update", err)
	}
	if err := unmarshalHeaderOverride(outHeader, &c.HeaderOverride); err != nil {
		return nil, fmt.Errorf("credentials.update.header: %w", err)
	}
	return &c, nil
}

func (r *CredentialRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM model_relay.credentials WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return translateErr("credentials.delete", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credentials.delete: %w", ErrNotFound)
	}
	return nil
}

// CountChannels returns how many active+disabled channels reference
// this credential. Admin UI shows it as the "in use" indicator.
func (r *CredentialRepo) CountChannels(ctx context.Context, id uuid.UUID) (int, error) {
	const q = `SELECT count(*) FROM model_relay.channels WHERE credential_id = $1`
	var n int
	if err := r.pool.QueryRow(ctx, q, id).Scan(&n); err != nil {
		return 0, translateErr("credentials.count_channels", err)
	}
	return n, nil
}

// ─── helpers ──────────────────────────────────────────────────────

func marshalHeaderOverride(m map[string]string) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("credentials: marshal header_override: %w", err)
	}
	return b, nil
}

func unmarshalHeaderOverride(b []byte, dst *map[string]string) error {
	if len(b) == 0 {
		*dst = map[string]string{}
		return nil
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("credentials: unmarshal header_override: %w", err)
	}
	return nil
}
