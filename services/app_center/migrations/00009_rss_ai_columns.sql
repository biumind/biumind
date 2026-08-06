-- +goose Up
-- +goose StatementBegin

-- ─── M1: AI digest 列 + embedding 占位 ─────────────────────────────
--
-- M1 给 rss.entries 加 AI 摘要字段, 让收件箱卡片不用进 reader 就能
-- 看到 AI 给的 takeaway + 3 bullets + ★ 重要度. embedding 列同步建
-- 好但 M1 不写; M4 语义雷达接通 model-relay /v1/embeddings 时再填,
-- 跨 milestone 拆 schema 再 ALTER 一次成本高于一次性建好.
--
-- 设计文档: docs/BiuMind-RSS-Reader-v2-Design.md §8.1
-- 开发计划: docs/BiuMind-RSS-Reader-v2-DevPlan.md §2 M1.1

ALTER TABLE rss.entries
    ADD COLUMN IF NOT EXISTS ai_takeaway     text,
    ADD COLUMN IF NOT EXISTS ai_bullets      jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS ai_topics       text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS ai_importance   smallint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ai_lang         text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ai_processed_at timestamptz,
    ADD COLUMN IF NOT EXISTS ai_error        text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding       vector(1536),
    ADD COLUMN IF NOT EXISTS word_count      int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reading_seconds int NOT NULL DEFAULT 0;

-- Hot-path partial index for the digest worker — scans the unprocessed
-- queue. Tiny in steady state because entries get processed within
-- minutes; large initially (whole table) only on first run.
CREATE INDEX IF NOT EXISTS entries_ai_unprocessed_idx
    ON rss.entries (fetched_at)
    WHERE ai_processed_at IS NULL AND ai_error = '';

-- ivfflat for cosine search. lists=100 is the pgvector recommended
-- starting point for tables under ~1M rows; rebalance later. NB: the
-- index is built but stays empty until M4 starts populating embedding.
-- Building it now saves a DROP / CREATE roundtrip when M4 lands.
CREATE INDEX IF NOT EXISTS entries_embedding_idx
    ON rss.entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- 用户的"沉 wiki / star / pin / shared"标记表 (M3 会用到, 但 schema
-- 在这里一起建避免再 ALTER 一次)
CREATE TABLE IF NOT EXISTS rss.entry_marks (
    user_id       text NOT NULL,
    entry_id      uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    mark          text NOT NULL CHECK (mark IN ('star', 'pin', 'wiki', 'shared')),
    wiki_block_id uuid,
    pin_until     timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, entry_id, mark)
);

CREATE INDEX IF NOT EXISTS entry_marks_user_mark_recent_idx
    ON rss.entry_marks (user_id, mark, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rss.entry_marks;

DROP INDEX IF EXISTS rss.entries_embedding_idx;
DROP INDEX IF EXISTS rss.entries_ai_unprocessed_idx;

ALTER TABLE rss.entries
    DROP COLUMN IF EXISTS reading_seconds,
    DROP COLUMN IF EXISTS word_count,
    DROP COLUMN IF EXISTS embedding,
    DROP COLUMN IF EXISTS ai_error,
    DROP COLUMN IF EXISTS ai_processed_at,
    DROP COLUMN IF EXISTS ai_lang,
    DROP COLUMN IF EXISTS ai_importance,
    DROP COLUMN IF EXISTS ai_topics,
    DROP COLUMN IF EXISTS ai_bullets,
    DROP COLUMN IF EXISTS ai_takeaway;

-- +goose StatementEnd
