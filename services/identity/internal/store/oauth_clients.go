// OAuth 2.1 client (relying party) registry — RFC 7591 (DCR) + RFC 7592.
//
// Stores third-party app metadata (Claude Desktop, Cursor, browser extensions)
// that authenticate biumind users via the /oauth/* endpoints. Public clients
// keep ClientSecretHash empty; confidential clients store a bcrypt hash.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OAuthClient is one row of identity.oauth_clients.
type OAuthClient struct {
	ClientID                    uuid.UUID
	ClientSecretHash            string // empty for public clients
	ClientName                  string
	RedirectURIs                []string
	GrantTypes                  []string
	ResponseTypes               []string
	TokenEndpointAuthMethod     string
	Scope                       string
	Contacts                    []string
	LogoURI                     string
	ClientURI                   string
	TosURI                      string
	PolicyURI                   string
	SoftwareID                  string
	SoftwareVersion             string
	RegistrationAccessTokenHash string
	CreatedBy                   *uuid.UUID
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

var ErrOAuthClientNotFound = errors.New("oauth client not found")

// CreateOAuthClientInput is what DCR fills in.
type CreateOAuthClientInput struct {
	ClientSecretHash            string
	ClientName                  string
	RedirectURIs                []string
	GrantTypes                  []string
	ResponseTypes               []string
	TokenEndpointAuthMethod     string
	Scope                       string
	Contacts                    []string
	LogoURI                     string
	ClientURI                   string
	TosURI                      string
	PolicyURI                   string
	SoftwareID                  string
	SoftwareVersion             string
	RegistrationAccessTokenHash string
	CreatedBy                   *uuid.UUID
}

func (s *Store) CreateOAuthClient(ctx context.Context, in CreateOAuthClientInput) (*OAuthClient, error) {
	var c OAuthClient
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.oauth_clients
			(client_secret_hash, client_name, redirect_uris,
			 grant_types, response_types, token_endpoint_auth_method,
			 scope, contacts, logo_uri, client_uri, tos_uri, policy_uri,
			 software_id, software_version, registration_access_token_hash,
			 created_by)
		VALUES
			(NULLIF($1, ''), $2, $3,
			 $4, $5, $6,
			 $7, $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
			 NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''),
			 $16)
		RETURNING client_id, created_at, updated_at
	`,
		in.ClientSecretHash, in.ClientName, in.RedirectURIs,
		in.GrantTypes, in.ResponseTypes, in.TokenEndpointAuthMethod,
		in.Scope, in.Contacts, in.LogoURI, in.ClientURI, in.TosURI, in.PolicyURI,
		in.SoftwareID, in.SoftwareVersion, in.RegistrationAccessTokenHash,
		in.CreatedBy,
	).Scan(&c.ClientID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.ClientSecretHash = in.ClientSecretHash
	c.ClientName = in.ClientName
	c.RedirectURIs = in.RedirectURIs
	c.GrantTypes = in.GrantTypes
	c.ResponseTypes = in.ResponseTypes
	c.TokenEndpointAuthMethod = in.TokenEndpointAuthMethod
	c.Scope = in.Scope
	c.Contacts = in.Contacts
	c.LogoURI = in.LogoURI
	c.ClientURI = in.ClientURI
	c.TosURI = in.TosURI
	c.PolicyURI = in.PolicyURI
	c.SoftwareID = in.SoftwareID
	c.SoftwareVersion = in.SoftwareVersion
	c.RegistrationAccessTokenHash = in.RegistrationAccessTokenHash
	c.CreatedBy = in.CreatedBy
	return &c, nil
}

// GetOAuthClientByID fetches one client. Returns ErrOAuthClientNotFound if
// missing.
func (s *Store) GetOAuthClientByID(ctx context.Context, id uuid.UUID) (*OAuthClient, error) {
	var c OAuthClient
	var secretHash, logo, cliURI, tos, policy, swID, swVer, regHash *string
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_secret_hash, client_name, redirect_uris,
		       grant_types, response_types, token_endpoint_auth_method,
		       scope, contacts,
		       logo_uri, client_uri, tos_uri, policy_uri,
		       software_id, software_version, registration_access_token_hash,
		       created_by, created_at, updated_at
		FROM identity.oauth_clients
		WHERE client_id = $1
	`, id).Scan(
		&c.ClientID, &secretHash, &c.ClientName, &c.RedirectURIs,
		&c.GrantTypes, &c.ResponseTypes, &c.TokenEndpointAuthMethod,
		&c.Scope, &c.Contacts,
		&logo, &cliURI, &tos, &policy,
		&swID, &swVer, &regHash,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOAuthClientNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ClientSecretHash = strFromPtr(secretHash)
	c.LogoURI = strFromPtr(logo)
	c.ClientURI = strFromPtr(cliURI)
	c.TosURI = strFromPtr(tos)
	c.PolicyURI = strFromPtr(policy)
	c.SoftwareID = strFromPtr(swID)
	c.SoftwareVersion = strFromPtr(swVer)
	c.RegistrationAccessTokenHash = strFromPtr(regHash)
	return &c, nil
}

// DeleteOAuthClient removes a client by id. Caller is responsible for auth.
func (s *Store) DeleteOAuthClient(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM identity.oauth_clients WHERE client_id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOAuthClientNotFound
	}
	return nil
}

// ListOAuthClientsByCreator returns clients owned by `userID`, newest first.
// For the "my registered apps" UI.
func (s *Store) ListOAuthClientsByCreator(ctx context.Context, userID uuid.UUID) ([]*OAuthClient, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_id, client_name, redirect_uris,
		       grant_types, response_types, token_endpoint_auth_method,
		       scope, contacts,
		       COALESCE(logo_uri, ''), COALESCE(client_uri, ''),
		       COALESCE(tos_uri, ''), COALESCE(policy_uri, ''),
		       COALESCE(software_id, ''), COALESCE(software_version, ''),
		       created_at, updated_at
		FROM identity.oauth_clients
		WHERE created_by = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OAuthClient
	for rows.Next() {
		var c OAuthClient
		if err := rows.Scan(
			&c.ClientID, &c.ClientName, &c.RedirectURIs,
			&c.GrantTypes, &c.ResponseTypes, &c.TokenEndpointAuthMethod,
			&c.Scope, &c.Contacts,
			&c.LogoURI, &c.ClientURI, &c.TosURI, &c.PolicyURI,
			&c.SoftwareID, &c.SoftwareVersion,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		uid := userID
		c.CreatedBy = &uid
		out = append(out, &c)
	}
	return out, rows.Err()
}

func strFromPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
