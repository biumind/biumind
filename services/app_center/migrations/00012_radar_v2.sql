-- +goose Up
-- +goose StatementBegin

-- ─── M4: Semantic radar + action recipes ──────────────────────────
--
-- watch_rules grows two parallel match modes:
--   - keyword (existing match_any/match_all/exclude)
--   - semantic (new: query text → embedding → cosine ≥ threshold)
-- Either or both can be set; the matcher takes the union.
--
-- actions: a JSON array of action recipes that fire on hit. Each
-- entry is `{type: 'notify'|'wiki'|'task'|'skill', config: {...}}`.
-- Old hits stayed Badge-only; new hits run the chain in order.
--
-- hit_clusters: same-rule + similar-title hits within a tick get
-- collapsed so the user gets one notification instead of N.

ALTER TABLE rss.watch_rules
    ADD COLUMN IF NOT EXISTS semantic_query     text,
    ADD COLUMN IF NOT EXISTS semantic_threshold real NOT NULL DEFAULT 0.78,
    ADD COLUMN IF NOT EXISTS semantic_embedding vector(1536),
    ADD COLUMN IF NOT EXISTS actions            jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS rss.hit_clusters (
    id           bigserial PRIMARY KEY,
    user_id      text NOT NULL,
    rule_ids     uuid[] NOT NULL,
    title_seed   text NOT NULL,
    member_hits  bigint[] NOT NULL,
    cluster_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS hit_clusters_user_recent_idx
    ON rss.hit_clusters (user_id, cluster_at DESC);

-- action_runs ledger — already added the column types here in case
-- it wasn't created by an earlier migration on the dev DB. Idempotent.
CREATE TABLE IF NOT EXISTS rss.action_runs (
    id           bigserial PRIMARY KEY,
    rule_id      uuid NOT NULL REFERENCES rss.watch_rules(id) ON DELETE CASCADE,
    hit_id       bigint REFERENCES rss.watch_hits(id) ON DELETE CASCADE,
    action_seq   smallint NOT NULL,
    action_type  text NOT NULL,
    status       text NOT NULL,
    result       jsonb,
    error        text NOT NULL DEFAULT '',
    started_at   timestamptz NOT NULL DEFAULT now(),
    duration_ms  int NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS action_runs_rule_recent_idx
    ON rss.action_runs (rule_id, started_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rss.action_runs;
DROP TABLE IF EXISTS rss.hit_clusters;

ALTER TABLE rss.watch_rules
    DROP COLUMN IF EXISTS actions,
    DROP COLUMN IF EXISTS semantic_embedding,
    DROP COLUMN IF EXISTS semantic_threshold,
    DROP COLUMN IF EXISTS semantic_query;

-- +goose StatementEnd
