-- +goose Up
-- +goose StatementBegin
--
-- Vision-caption pipeline (LLM_WIKI port).
--
-- Two pieces:
--
--   1. brain.image_captions — content-addressed cache. Same image URL
--      across N pages = ONE vision call total. SHA-256 of the URL is
--      the PK so dedupe is O(1) and we never store >1 row per URL even
--      under concurrent writes (ON CONFLICT DO NOTHING).
--
--   2. brain.blocks.captioned_at — per-block high-water mark. Worker
--      finds blocks where captioned_at IS NULL OR < updated_at, runs
--      the caption pipeline, sets it to now(). Self-healing tick (same
--      pattern as embedworker / enrich worker — no separate task queue).

CREATE TABLE IF NOT EXISTS brain.image_captions (
    url_hash    bytea PRIMARY KEY,
    url         text NOT NULL,
    caption     text NOT NULL,
    model       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE brain.blocks
    ADD COLUMN IF NOT EXISTS captioned_at timestamptz;

-- Worker query: WHERE deleted_at IS NULL AND (captioned_at IS NULL OR
-- captioned_at < updated_at). Partial index keeps us off seq scans
-- when the table grows past a few thousand rows.
CREATE INDEX IF NOT EXISTS blocks_caption_pending_idx
    ON brain.blocks (updated_at)
    WHERE deleted_at IS NULL
      AND (captioned_at IS NULL OR captioned_at < updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.blocks_caption_pending_idx;
ALTER TABLE brain.blocks DROP COLUMN IF EXISTS captioned_at;
DROP TABLE IF EXISTS brain.image_captions;
-- +goose StatementEnd
