-- +goose Up
-- +goose StatementBegin

-- ─── chat.providers — per-user LLM provider configuration ─────
--
-- Stores the user's chosen LLM providers + credentials. Each row is
-- one provider config for one user. Keys are encrypted at rest via
-- AES-256-GCM with a server-known KEYVAULTS_SECRET (rotation handled
-- by re-encrypting all rows under a new secret — out of scope for now).
--
-- fetch_mode controls who calls the LLM:
--   * 'server' — Brain dereferences the encrypted key and proxies the
--                LLM call. Default for builtin providers (lower client
--                bandwidth, easier debugging).
--   * 'client' — Brain returns the (decrypted) key to the requesting
--                client over JWT-protected HTTPS; the client opens the
--                LLM stream directly. Default for custom providers.
--
-- source distinguishes:
--   * 'builtin' — pre-known providers (anthropic / openai / google …),
--                 user just adds credentials. provider_id matches a
--                 client-side catalog entry.
--   * 'custom'  — user-defined endpoint (typically OpenAI-compatible:
--                 vLLM / Ollama / LiteLLM / a self-host of any model).
--                 provider_id is a user-chosen slug.

CREATE TABLE IF NOT EXISTS chat.providers (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- user_id is the JWT subject; we don't FK to identity.users because
    -- identity lives in a separate schema/service and chat tables follow
    -- the same convention. Owner-scoped by application code.
    user_id               uuid NOT NULL,
    provider_id           text NOT NULL,            -- 'anthropic' | 'openai' | slug
    display_name          text NOT NULL DEFAULT '',
    fetch_mode            text NOT NULL DEFAULT 'server'
                              CHECK (fetch_mode IN ('server','client')),
    base_url              text,                     -- override / required for custom
    enabled               boolean NOT NULL DEFAULT true,
    -- Encrypted JSON blob: { "api_key": "...", "extra": {...} }.
    -- AES-256-GCM (nonce|ciphertext|tag), base64 stored as bytea.
    key_vaults_encrypted  bytea,
    source                text NOT NULL DEFAULT 'builtin'
                              CHECK (source IN ('builtin','custom')),
    -- Misc per-provider knobs (e.g. response_api flag, custom headers).
    config_json           jsonb NOT NULL DEFAULT '{}'::jsonb,
    sort_order            integer NOT NULL DEFAULT 0,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- A given user has at most one row per builtin provider id; custom
-- providers are uniqued by (user_id, provider_id) too — the user picks
-- the slug, so collisions are their own choice. Either way the index
-- catches dupes.
CREATE UNIQUE INDEX IF NOT EXISTS providers_user_provider_uniq
    ON chat.providers (user_id, provider_id);

CREATE INDEX IF NOT EXISTS providers_user_enabled
    ON chat.providers (user_id, enabled);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS chat.providers;

-- +goose StatementEnd
