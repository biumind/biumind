// API token (PAT) persistence.
//
// The actual token secret is a JWT signed by identity's signer; we
// only store its jti + metadata here. Listing returns redacted info
// for UI; the secret is shown once at creation time and never again.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// APIToken is one row of identity.api_tokens.
type APIToken struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	WorkspaceID *uuid.UUID
	ProjectID   *uuid.UUID
	Name        string
	Prefix      string
	JTI         string
	Scopes      []string
	LastUsedAt  *time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// CreateAPITokenInput is what the API hand the store on mint. Caller
// has already generated jti + prefix and signed the JWT — this layer
// just persists the metadata.
type CreateAPITokenInput struct {
	OwnerID     uuid.UUID
	WorkspaceID *uuid.UUID
	ProjectID   *uuid.UUID
	Name        string
	Prefix      string
	JTI         string
	Scopes      []string
	ExpiresAt   time.Time
}

func (s *Store) CreateAPIToken(ctx context.Context, in CreateAPITokenInput) (*APIToken, error) {
	if in.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("owner_id required")
	}
	if in.Name == "" {
		return nil, fmt.Errorf("name required")
	}
	if in.Prefix == "" || in.JTI == "" {
		return nil, fmt.Errorf("prefix and jti required")
	}
	scopes := in.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	t := &APIToken{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.api_tokens
		    (owner_id, workspace_id, project_id, name, prefix, jti, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, owner_id, workspace_id, project_id, name, prefix, jti,
		          scopes, last_used_at, expires_at, revoked_at, created_at
	`, in.OwnerID, in.WorkspaceID, in.ProjectID, in.Name, in.Prefix,
		in.JTI, scopes, in.ExpiresAt).Scan(
		&t.ID, &t.OwnerID, &t.WorkspaceID, &t.ProjectID, &t.Name, &t.Prefix,
		&t.JTI, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert api_token: %w", err)
	}
	return t, nil
}

// ListAPITokens returns the caller's tokens, newest first. Excludes
// the JWT secret (which we don't store anyway) — only metadata.
func (s *Store) ListAPITokens(ctx context.Context, ownerID uuid.UUID) ([]*APIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, workspace_id, project_id, name, prefix, jti,
		       scopes, last_used_at, expires_at, revoked_at, created_at
		  FROM identity.api_tokens
		 WHERE owner_id = $1
		 ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t := &APIToken{}
		if err := rows.Scan(
			&t.ID, &t.OwnerID, &t.WorkspaceID, &t.ProjectID, &t.Name, &t.Prefix,
			&t.JTI, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken marks the token revoked. Returns ErrAPITokenNotFound
// when no row matches the (id, owner) pair — prevents a user from
// revoking another user's token via crafted UUIDs.
//
// Repeat revocations are no-ops (idempotent: revoked_at sticks at the
// first revocation timestamp).
func (s *Store) RevokeAPIToken(ctx context.Context, ownerID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.api_tokens
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE id = $1 AND owner_id = $2
	`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAPITokenNotFound
	}
	return nil
}

// TouchAPITokenLastUsed bumps last_used_at when the token is observed
// in a request. Best-effort: we don't fail the request if the update
// errors. Identity calls this from its auth middleware; other services
// don't (they don't have direct DB access to identity).
func (s *Store) TouchAPITokenLastUsed(ctx context.Context, jti string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.api_tokens SET last_used_at = now()
		 WHERE jti = $1 AND revoked_at IS NULL
	`, jti)
	return err
}

// GetAPITokenByJTI returns the token matching the given JTI. Used by
// the verifier-callback path to check revocation. Returns nil + nil
// when no match (caller treats that as "not a PAT" → fall through to
// regular JWT auth).
func (s *Store) GetAPITokenByJTI(ctx context.Context, jti string) (*APIToken, error) {
	t := &APIToken{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, workspace_id, project_id, name, prefix, jti,
		       scopes, last_used_at, expires_at, revoked_at, created_at
		  FROM identity.api_tokens WHERE jti = $1
	`, jti).Scan(
		&t.ID, &t.OwnerID, &t.WorkspaceID, &t.ProjectID, &t.Name, &t.Prefix,
		&t.JTI, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ErrAPITokenNotFound — not exported via a generic Err* alias because
// the API layer wants the specific case to map to 404 distinctly from
// other store errors.
var ErrAPITokenNotFound = errors.New("api_token not found")
