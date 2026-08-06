-- +goose Up
-- +goose StatementBegin

-- ─── Page community_id ──────────────────────────────────────────
-- Louvain modularity clustering of the wikilink + relevance graph.
-- Workers populate this column; readers (search, "see also" panel)
-- use it as a coarse-grained "this page belongs to topic cluster N"
-- signal that complements the per-pair relevance score.
--
-- Type is `int`: communities have no natural identity beyond
-- "members of this set". Worker reassigns ids on every full
-- recompute, so any process holding a stale id will simply look
-- mismatched (no members) — search treats that as "no boost",
-- which is correct.
--
-- NULL = unclustered (workers haven't run yet, or page sits in a
-- singleton community below the size threshold). Search code paths
-- handle NULL the same as "no boost".

ALTER TABLE brain.pages ADD COLUMN IF NOT EXISTS community_id int;

CREATE INDEX IF NOT EXISTS pages_community_idx
    ON brain.pages(project_id, community_id)
    WHERE deleted_at IS NULL AND community_id IS NOT NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.pages_community_idx;
ALTER TABLE brain.pages DROP COLUMN IF EXISTS community_id;
-- +goose StatementEnd
