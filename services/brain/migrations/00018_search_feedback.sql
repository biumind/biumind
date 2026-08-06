-- +goose Up
-- +goose StatementBegin

-- ─── Search feedback ────────────────────────────────────────────
-- Per-result thumbs up/down on the unified search page. Used by ops
-- to spot bad rankings ("query X always gets thumbs-down on Y") and
-- by future RRF tuning passes that re-weight the four retrieval
-- paths against per-user / per-query feedback signal.
--
-- Identity: a single (user, query_lower, page) tuple holds the
-- user's most recent verdict. Re-clicking the same thumb is a no-op;
-- clicking the opposite thumb flips the row; clicking the already-
-- selected thumb DELETEs the row (UI toggle off → no opinion).
--
-- Why query_lower (vs raw query): users typing "Total assets" twice
-- shouldn't accumulate two rows just because of a capital letter.
-- This also matches the tokenizer's lowercased-everywhere shape.
--
-- Rank captures position in the fused list at click time, so a
-- training pipeline can ask "of all the up-votes we got, what was
-- the median rank?" without re-running the search.

CREATE TABLE IF NOT EXISTS brain.search_feedback (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL,
    project_id   uuid REFERENCES brain.projects(id) ON DELETE CASCADE,
    query_lower  text NOT NULL,
    page_id      uuid NOT NULL,
    rank         int  NOT NULL DEFAULT 0,
    signal       text NOT NULL CHECK (signal IN ('up', 'down')),
    -- Free-form jsonb — used today for the source path that surfaced
    -- the hit (wiki / vector / graph), opens up later for richer
    -- click context without schema churn.
    meta         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- One verdict per (user, query, page). The API layer relies on
    -- this for upsert + toggle semantics.
    UNIQUE (user_id, query_lower, page_id)
);

CREATE INDEX IF NOT EXISTS search_feedback_user_idx
    ON brain.search_feedback(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS search_feedback_query_idx
    ON brain.search_feedback(query_lower, signal);

CREATE INDEX IF NOT EXISTS search_feedback_page_idx
    ON brain.search_feedback(page_id, signal);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.search_feedback;
-- +goose StatementEnd
