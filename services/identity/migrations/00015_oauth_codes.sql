-- +goose Up
-- +goose StatementBegin
--
-- P2-I-2b — OAuth 2.1 authorization code with PKCE.
--
-- Codes live ~10 minutes. PKCE challenge is required (OAuth 2.1 — no
-- public-client downgrade allowed). consumed_at is set atomically when
-- the token endpoint exchanges the code; second consumption is rejected
-- and SHOULD trigger revocation of every token derived from this code
-- (RFC 6819 §5.2.1.1 / OAuth 2.1 §7.5.1).

CREATE TABLE IF NOT EXISTS identity.oauth_authorization_codes (
    code                  text PRIMARY KEY,
    client_id             uuid NOT NULL,
    user_id               uuid NOT NULL,
    redirect_uri          text NOT NULL,
    scope                 text NOT NULL DEFAULT '',
    code_challenge        text NOT NULL,
    code_challenge_method text NOT NULL,
    expires_at            timestamptz NOT NULL,
    consumed_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- Sweep job uses this; client_id index for "what did this app authorize"
-- diagnostics.
CREATE INDEX IF NOT EXISTS oauth_codes_expires_idx
    ON identity.oauth_authorization_codes (expires_at);
CREATE INDEX IF NOT EXISTS oauth_codes_client_idx
    ON identity.oauth_authorization_codes (client_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.oauth_authorization_codes;
-- +goose StatementEnd
