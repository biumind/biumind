-- +goose Up
-- +goose StatementBegin

-- ─── Phase 4: source extension + chat.models ─────────────────
--
-- Expand chat.providers.source to admit 'official' (BiuMind's own
-- platform-pool channel — Brain dispatches with the Hub env key and
-- bills via subscription / metered).
--
-- Add chat.models for per-user model metadata: which models are
-- enabled for a given provider, ordering, and the capability/pricing
-- payload that powers the model list UI. Built-in models are seeded
-- by application code on first list-models call (cheap idempotent
-- upsert keyed by (user_id, provider_id, model_id)).

ALTER TABLE chat.providers DROP CONSTRAINT IF EXISTS providers_source_check;
ALTER TABLE chat.providers ADD CONSTRAINT providers_source_check
    CHECK (source IN ('official', 'builtin', 'custom'));

CREATE TABLE IF NOT EXISTS chat.models (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    provider_id     text NOT NULL,           -- matches chat.providers.provider_id
    model_id        text NOT NULL,           -- wire id ('claude-opus-4-7')
    display_name    text NOT NULL DEFAULT '',
    type            text NOT NULL DEFAULT 'chat'
                        CHECK (type IN ('chat','image','video','embedding','stt','tts')),
    -- abilities: {"vision": true, "audio": false, "functions": true, "reasoning": false}
    abilities       jsonb NOT NULL DEFAULT '{}'::jsonb,
    context_window  integer,
    -- pricing: {"input_per_m_usd": 5, "output_per_m_usd": 25, ...}.
    -- Null for free / unknown.
    pricing_json    jsonb,
    released_at     date,
    enabled         boolean NOT NULL DEFAULT true,
    sort_order      integer NOT NULL DEFAULT 0,
    -- 'builtin' (from our static catalog) or 'remote' (fetched from
    -- provider's /models endpoint) or 'custom' (user-added one-off).
    source          text NOT NULL DEFAULT 'builtin'
                        CHECK (source IN ('builtin','remote','custom')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS models_user_provider_model
    ON chat.models (user_id, provider_id, model_id);

CREATE INDEX IF NOT EXISTS models_user_enabled
    ON chat.models (user_id, enabled);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS chat.models;
ALTER TABLE chat.providers DROP CONSTRAINT IF EXISTS providers_source_check;
ALTER TABLE chat.providers ADD CONSTRAINT providers_source_check
    CHECK (source IN ('builtin', 'custom'));

-- +goose StatementEnd
