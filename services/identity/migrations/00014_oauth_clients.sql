-- +goose Up
-- +goose StatementBegin
--
-- P2-I-2 OAuth 2.1 — third-party application registry.
--
-- biumind acts as Authorization Server for third-party clients (MCP
-- desktop apps, browser extensions, CI agents). Each client registers
-- via Dynamic Client Registration (RFC 7591) at POST /oauth/register
-- and is later identified by client_id. Public clients (PKCE-only) keep
-- client_secret_hash = NULL; confidential clients (server-side, secret
-- stored at rest) get a bcrypt hash here.
--
-- redirect_uris is an array — RFC 7591 lets a client declare multiple,
-- and authorize-time matching is exact-match against the array. This
-- closes the "open redirect" hole that loose substring matching opens.

CREATE TABLE IF NOT EXISTS identity.oauth_clients (
    client_id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_secret_hash          text,
    client_name                 text NOT NULL,
    redirect_uris               text[] NOT NULL,
    grant_types                 text[] NOT NULL DEFAULT ARRAY['authorization_code','refresh_token']::text[],
    response_types              text[] NOT NULL DEFAULT ARRAY['code']::text[],
    token_endpoint_auth_method  text NOT NULL DEFAULT 'none',  -- 'none' | 'client_secret_post' | 'client_secret_basic'
    scope                       text NOT NULL DEFAULT '',
    contacts                    text[] NOT NULL DEFAULT ARRAY[]::text[],
    logo_uri                    text,
    client_uri                  text,
    tos_uri                     text,
    policy_uri                  text,
    software_id                 text,
    software_version            text,
    -- registration_access_token (RFC 7592 client management) lets the
    -- registrar later GET/PUT/DELETE the client. We hash it the same way
    -- as the secret so a DB read can't replay it.
    registration_access_token_hash text,
    -- The user who registered this app — useful for audit + per-user
    -- rate-limit on registrations. Null for system-seeded clients.
    created_by                  uuid,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS oauth_clients_created_by_idx
    ON identity.oauth_clients (created_by, created_at DESC)
    WHERE created_by IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.oauth_clients;
-- +goose StatementEnd
