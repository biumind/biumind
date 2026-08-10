-- +goose Up
-- +goose StatementBegin

-- Default chat model marker (Phase B): admin designates exactly one
-- mode='chat' model as the platform default; brain's ChatRunner pulls it
-- via GET /v1/internal/models/default-chat instead of a hardcoded
-- fallback model code.
ALTER TABLE model_relay.models
    ADD COLUMN IF NOT EXISTS is_default_chat boolean NOT NULL DEFAULT false;

-- At most one default chat model globally. The app layer clears other
-- rows inside the same transaction before setting a new default; this
-- partial unique index is the backstop against races.
CREATE UNIQUE INDEX IF NOT EXISTS models_default_chat_unique_idx
    ON model_relay.models (is_default_chat)
    WHERE is_default_chat;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS model_relay.models_default_chat_unique_idx;

ALTER TABLE model_relay.models
    DROP COLUMN IF EXISTS is_default_chat;

-- +goose StatementEnd
