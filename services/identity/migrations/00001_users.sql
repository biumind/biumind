-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS identity.users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext UNIQUE NOT NULL,
    password_hash   text,
    display_name    text NOT NULL DEFAULT '',
    default_org_id  uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity.refresh_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    token_hash      bytea NOT NULL UNIQUE,
    device_name     text NOT NULL DEFAULT '',
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON identity.refresh_tokens(user_id);
CREATE INDEX refresh_tokens_expires_idx ON identity.refresh_tokens(expires_at);

CREATE TABLE IF NOT EXISTS identity.virtual_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    prefix          text UNIQUE NOT NULL,
    secret_hash     bytea NOT NULL,
    name            text NOT NULL DEFAULT '',
    scope           jsonb NOT NULL DEFAULT '{}'::jsonb,
    expires_at      timestamptz,
    revoked_at      timestamptz,
    last_used_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX virtual_keys_user_idx ON identity.virtual_keys(user_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS identity.virtual_keys;
DROP TABLE IF EXISTS identity.refresh_tokens;
DROP TABLE IF EXISTS identity.users;
