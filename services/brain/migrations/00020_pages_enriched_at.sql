-- +goose Up
-- +goose StatementBegin
--
-- LLM_WIKI port — wikilink enrichment worker.
--
-- enriched_at is the high-water mark for the LLM-driven [[wikilink]]
-- pass. NULL ⇒ never enriched. enriched_at < page.updated_at ⇒ stale,
-- needs re-enrichment. The worker uses (enriched_at IS NULL OR
-- enriched_at < updated_at) to find candidates — same self-healing
-- pattern as embedworker (no separate task queue table).
--
-- We keep enrichment OPT-IN at the project level via brain.projects
-- frontmatter (config.enrich_wikilinks=true) — running an LLM call per
-- page edit is expensive, and it should be a deliberate choice on
-- knowledge-base-style projects, not e.g. chat threads. Worker checks
-- the flag before processing each candidate.

ALTER TABLE brain.pages
    ADD COLUMN IF NOT EXISTS enriched_at timestamptz;

-- Worker query: WHERE deleted_at IS NULL AND (enriched_at IS NULL OR
-- enriched_at < updated_at). Partial index keeps it cheap (only the
-- active candidates show up here, never the satisfied ones).
CREATE INDEX IF NOT EXISTS pages_enrich_pending_idx
    ON brain.pages (project_id, updated_at)
    WHERE deleted_at IS NULL
      AND (enriched_at IS NULL OR enriched_at < updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.pages_enrich_pending_idx;
ALTER TABLE brain.pages DROP COLUMN IF EXISTS enriched_at;
-- +goose StatementEnd
