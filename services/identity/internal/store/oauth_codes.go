// OAuth 2.1 authorization code store.
//
// Codes are random 32-byte base64url strings minted at /oauth/authorize and
// exchanged at /oauth/token. ConsumeAuthCode is atomic — the second
// consume returns ErrAuthCodeAlreadyConsumed, and the caller MUST revoke
// every token derived from this code (RFC 6819 §5.2.1.1).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuthCode is one row of identity.oauth_authorization_codes.
type AuthCode struct {
	Code                string
	ClientID            uuid.UUID
	UserID              uuid.UUID
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
	CreatedAt           time.Time
}

var (
	ErrAuthCodeNotFound        = errors.New("auth code not found")
	ErrAuthCodeExpired         = errors.New("auth code expired")
	ErrAuthCodeAlreadyConsumed = errors.New("auth code already consumed")
)

// CreateAuthCodeInput — what /oauth/authorize fills in.
type CreateAuthCodeInput struct {
	Code                string
	ClientID            uuid.UUID
	UserID              uuid.UUID
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
}

func (s *Store) CreateAuthCode(ctx context.Context, in CreateAuthCodeInput) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.oauth_authorization_codes
			(code, client_id, user_id, redirect_uri, scope,
			 code_challenge, code_challenge_method, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		in.Code, in.ClientID, in.UserID, in.RedirectURI, in.Scope,
		in.CodeChallenge, in.CodeChallengeMethod, in.ExpiresAt,
	)
	return err
}

// ConsumeAuthCode atomically marks the code as consumed and returns its
// payload. The second invocation returns ErrAuthCodeAlreadyConsumed and the
// caller must treat it as a replay attack — every token derived from this
// code must be revoked.
//
// We pull the row, validate, then UPDATE the consumed_at; the FOR UPDATE
// prevents concurrent token endpoint calls from both seeing
// consumed_at=NULL and minting two distinct token pairs.
func (s *Store) ConsumeAuthCode(ctx context.Context, code string) (*AuthCode, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var ac AuthCode
	err = tx.QueryRow(ctx, `
		SELECT code, client_id, user_id, redirect_uri, scope,
		       code_challenge, code_challenge_method,
		       expires_at, consumed_at, created_at
		FROM identity.oauth_authorization_codes
		WHERE code = $1
		FOR UPDATE
	`, code).Scan(
		&ac.Code, &ac.ClientID, &ac.UserID, &ac.RedirectURI, &ac.Scope,
		&ac.CodeChallenge, &ac.CodeChallengeMethod,
		&ac.ExpiresAt, &ac.ConsumedAt, &ac.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAuthCodeNotFound
	}
	if err != nil {
		return nil, err
	}
	if ac.ConsumedAt != nil {
		return &ac, ErrAuthCodeAlreadyConsumed
	}
	if time.Now().After(ac.ExpiresAt) {
		return &ac, ErrAuthCodeExpired
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE identity.oauth_authorization_codes
		SET consumed_at = $1
		WHERE code = $2
	`, now, code); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	ac.ConsumedAt = &now
	return &ac, nil
}

// SweepExpiredAuthCodes deletes codes past their expires_at. Returns the
// number of rows removed. Callers run this on a cron — the index on
// expires_at keeps the delete cheap.
func (s *Store) SweepExpiredAuthCodes(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM identity.oauth_authorization_codes
		WHERE expires_at < now() - interval '1 hour'
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
