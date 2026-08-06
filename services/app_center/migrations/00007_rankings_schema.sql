-- +goose Up
-- +goose StatementBegin

-- ─── Rankings — newsnow-backed Chinese hot lists ─────────────────
--
-- Schema is deliberately tenant-LESS. Per the design decision
-- (BiuMind-RSS-Radar-Design.md §1.3 #3) every user shares the same
-- snapshot — boards are global infrastructure, not per-user feeds.
-- Per-user filtering happens later in the radar layer (rss.watch_rules
-- — already in 00006).
--
-- Three tables:
--   1. boards     — the seed catalogue + scheduler state per board
--   2. snapshots  — append-only history; each tick writes one row
--   3. items_seen — (board, sha256(title)) → first/last seen, used to
--                   mark "新进榜" entries for the radar matcher
--
-- Data retention is policy-driven by a daily cron (rss.gc trigger
-- already exists; we'll extend it to also prune snapshots > 24h and
-- items_seen rows where last_seen_at < now() - 7 days).

CREATE SCHEMA IF NOT EXISTS rankings;

-- 1. boards — newsnow source clients with per-board scheduling
--
-- id matches the newsnow source slug exactly (e.g. 'weibo', 'baidu').
-- We don't auto-generate uuids here so seed migrations stay idempotent
-- with ON CONFLICT DO NOTHING.
--
-- expected_domain is the security guard: every URL in a snapshot must
-- be https + host-equals-or-subdomain-of expected_domain. Mismatch
-- → drop the entire snapshot, set last_status='warn'. Optional (NULL
-- means "trust upstream"); strongly recommended in production.

CREATE TABLE IF NOT EXISTS rankings.boards (
    id              text PRIMARY KEY,                       -- newsnow source id
    name            text NOT NULL,                          -- 显示名
    enabled         boolean NOT NULL DEFAULT true,
    refresh_sec     int NOT NULL DEFAULT 600,               -- 10 min default
    expected_domain text,

    last_fetched_at timestamptz,
    last_status     text NOT NULL DEFAULT '',               -- ok|warn|error|disabled
    last_error      text NOT NULL DEFAULT '',
    consecutive_failures int NOT NULL DEFAULT 0,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Scheduler hot path — same partial-index trick as rss.feeds.
CREATE INDEX IF NOT EXISTS boards_due_idx
    ON rankings.boards (last_fetched_at NULLS FIRST)
    WHERE enabled = true;


-- 2. snapshots — one row per scheduler tick that produced fresh data
--
-- items is the full top-N JSONB array from newsnow, stored verbatim
-- after expected_domain validation. The UI's "current rank" view
-- queries `ORDER BY captured_at DESC LIMIT 1`; the "排名变化" arrows
-- compare to LIMIT 1 OFFSET 1.
--
-- updated_time is newsnow's own freshness marker (millis since epoch)
-- — we keep it so a 200/cache-hit doesn't pollute history when the
-- upstream payload didn't actually change.

CREATE TABLE IF NOT EXISTS rankings.snapshots (
    id            bigserial PRIMARY KEY,
    board_id      text NOT NULL REFERENCES rankings.boards(id) ON DELETE CASCADE,
    captured_at   timestamptz NOT NULL DEFAULT now(),
    updated_time  bigint,
    items         jsonb NOT NULL,
    UNIQUE (board_id, captured_at)
);

CREATE INDEX IF NOT EXISTS snapshots_board_recent_idx
    ON rankings.snapshots (board_id, captured_at DESC);


-- 3. items_seen — per-(board, title) memory for "新进榜" detection
--
-- title_hash is sha256(lower(title)). Per design §5.2 we don't trust
-- newsnow's item.id (some sources use the URL with dynamic params,
-- some use rotating numbers); the title text is the most stable
-- identity across two snapshots from the same source.
--
-- A title is "新进榜" when its first_seen_at == now() in the same
-- transaction that wrote it (use RETURNING (xmax = 0) to detect
-- INSERT vs UPDATE — see Design §5.2 "diff" pseudocode).

CREATE TABLE IF NOT EXISTS rankings.items_seen (
    board_id      text NOT NULL REFERENCES rankings.boards(id) ON DELETE CASCADE,
    title_hash    bytea NOT NULL,
    title         text NOT NULL,
    url           text,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (board_id, title_hash)
);

CREATE INDEX IF NOT EXISTS items_seen_last_seen_idx
    ON rankings.items_seen (board_id, last_seen_at);


-- ─── Seed: 14 default boards ─────────────────────────────────────
--
-- Curated from TrendRadar's default config (which itself proxies
-- newsnow). Add more sources later by INSERT … ON CONFLICT DO NOTHING
-- in a follow-up migration; never overwrite an existing user-tweaked
-- row (e.g. one that disabled a board).

INSERT INTO rankings.boards (id, name, expected_domain) VALUES
    ('toutiao',             '今日头条',          'toutiao.com'),
    ('baidu',               '百度热搜',          'baidu.com'),
    ('wallstreetcn-hot',    '华尔街见闻',        'wallstreetcn.com'),
    ('thepaper',            '澎湃新闻',          'thepaper.cn'),
    ('bilibili-hot-search', 'bilibili 热搜',     'bilibili.com'),
    ('cls-hot',             '财联社热门',        'cls.cn'),
    ('ifeng',               '凤凰网',            'ifeng.com'),
    ('tieba',               '贴吧',              'baidu.com'),
    ('weibo',               '微博',              'weibo.com'),
    ('douyin',              '抖音',              'douyin.com'),
    ('zhihu',               '知乎',              'zhihu.com'),
    ('hacker-news',         'Hacker News',       'ycombinator.com'),
    ('ruanyifeng',          '阮一峰的网络日志',  'ruanyifeng.com'),
    ('yahoo-finance',       '雅虎财经',          'yahoo.com')
ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rankings.items_seen;
DROP TABLE IF EXISTS rankings.snapshots;
DROP TABLE IF EXISTS rankings.boards;
DROP SCHEMA IF EXISTS rankings;

-- +goose StatementEnd
