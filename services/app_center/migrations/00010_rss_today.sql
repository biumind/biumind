-- +goose Up
-- +goose StatementBegin

-- ─── M2: Today picker — clusters + interests + reading log ────────
--
-- Three tables wire the data path for the Today view:
--
--   entry_clusters  - groups near-duplicate entries (different feeds
--                     publishing the same news) so Today's top-5
--                     doesn't show 5 cards for the same story.
--   user_interests  - per-user 1536-d centroid + topic frequency
--                     (recomputed daily from reading_log). Used to
--                     score candidate entries by cosine similarity.
--   reading_log     - append-only behavioural ledger that drives the
--                     interest recompute + the "read today / streak"
--                     stats footer.
--
-- 设计文档: docs/BiuMind-RSS-Reader-v2-Design.md §8.2
-- 开发计划: docs/BiuMind-RSS-Reader-v2-DevPlan.md §3 M2.1

-- 1. Clusters of near-duplicate entries.
--
-- canonical 是 cluster 内重要度最高的一篇 (今日头条只展示这篇);
-- member_ids 包含 canonical, 副本以"另外 N 个来源"展开. 计算时机:
-- Today picker 跑前 (per-tick), 不持久化太久 — 24h cron 清掉超龄 row.
CREATE TABLE IF NOT EXISTS rss.entry_clusters (
    cluster_id   bigserial PRIMARY KEY,
    canonical    uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    member_ids   uuid[] NOT NULL,
    topic_label  text NOT NULL DEFAULT '',
    quality      real NOT NULL DEFAULT 0.5,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS entry_clusters_canonical_idx
    ON rss.entry_clusters (canonical);
CREATE INDEX IF NOT EXISTS entry_clusters_recent_idx
    ON rss.entry_clusters (created_at DESC);


-- 2. User interest centroid + topic ranking.
--
-- Recomputed daily by the interest cron (internal/rss/interest).
-- Single row per user so the picker just JOIN-and-cosine without any
-- aggregation. interest_centroid is a mean of recently-engaged entry
-- embeddings; nullable for users who haven't engaged with anything yet
-- (Today falls back to global popularity for them).
CREATE TABLE IF NOT EXISTS rss.user_interests (
    user_id            text PRIMARY KEY,
    interest_centroid  vector(1536),
    top_topics         text[] NOT NULL DEFAULT '{}',
    sample_count       int NOT NULL DEFAULT 0,
    updated_at         timestamptz NOT NULL DEFAULT now()
);


-- 3. Reading behaviour log.
--
-- Append-only. Every UI interaction worth scoring lands here:
--   opened     - entry's reader pane opened (auto-fired by client)
--   read_full  - entry mark_read (1.5s auto-mark or manual button)
--   starred    - ★
--   wiki       - 沉到 wiki
--   task       - 加进 task
--   dismissed  - swipe-left or "不感兴趣" (negative signal for
--                interest recompute and future negative training)
--   shared     - 📤
--
-- seconds 是会话级阅读时长 (client 计时, ≤ 1800 cap), 给 read time
-- estimate 做校准用; opened/dismissed 之外的事件可缺省为 0.
CREATE TABLE IF NOT EXISTS rss.reading_log (
    id           bigserial PRIMARY KEY,
    user_id      text NOT NULL,
    entry_id     uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    event        text NOT NULL CHECK (event IN
                   ('opened','read_full','starred','wiki','task','dismissed','shared')),
    seconds      int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Hot path: per-user recent activity (interest recompute + Today
-- stats both scan the latest 30d slice).
CREATE INDEX IF NOT EXISTS reading_log_user_recent_idx
    ON rss.reading_log (user_id, created_at DESC);

-- Per-entry lookup (for "did this user already engage with this
-- candidate" — Today picker filters out engaged entries from the
-- "missed" slot).
CREATE INDEX IF NOT EXISTS reading_log_entry_idx
    ON rss.reading_log (entry_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS rss.reading_log_entry_idx;
DROP INDEX IF EXISTS rss.reading_log_user_recent_idx;
DROP TABLE IF EXISTS rss.reading_log;
DROP TABLE IF EXISTS rss.user_interests;
DROP INDEX IF EXISTS rss.entry_clusters_recent_idx;
DROP INDEX IF EXISTS rss.entry_clusters_canonical_idx;
DROP TABLE IF EXISTS rss.entry_clusters;

-- +goose StatementEnd
