-- +goose Up
-- +goose StatementBegin

-- ─── RSS app — feeds / entries / watch rules / watch hits ─────────
--
-- Lands four tables under a fresh `rss` schema. The split from
-- app_center is deliberate: app_center.* is platform plumbing
-- (catalogue + installs + scheduler), rss.* is per-app domain data
-- owned by the rss App's installations. Other Apps that need
-- persistent state will land their own schema (tasks.*, email.*, …).
--
-- Two of the tables (watch_rules, watch_hits) are pre-built here even
-- though only feeds/entries are used in P0 — the design doc calls for
-- a single migration so we don't have to migrate twice. P2 fills in
-- the matcher pipeline that writes hits.
--
-- Events: every mutation in this schema MUST insert a corresponding
-- row in app_center.events in the same tx (invariant I4). We do this
-- in Go via events.Write — no SQL triggers — so the writer stays the
-- single point of taxonomy enforcement (see internal/events/writer.go).
--
-- See docs/BiuMind-RSS-Radar-Design.md §4 for the full rationale.

CREATE SCHEMA IF NOT EXISTS rss;

-- ─── 1. feeds — user/org-scoped subscriptions ────────────────────
--
-- One row per subscription. (scope, scope_id, feed_url) UNIQUE so a
-- user can't accidentally double-subscribe; the API rejects the
-- second add with ErrAlreadySubscribed.
--
-- etag + last_modified support conditional GET — Miniflux fetcher
-- skips parse work when the upstream returns 304. last_status is the
-- diagnostic surface for the UI: ok | stale | error | disabled.
--
-- refresh_sec is per-feed because some feeds (HN frontpage) deserve
-- 5 min and some (a personal blog) 24 h. Default 1800s (30 min). On
-- repeated failure the scheduler doubles this in-memory until next
-- success — we don't persist the backoff, just compute from
-- consecutive_failures (added later if needed; v0.1 uses last_error).

CREATE TABLE IF NOT EXISTS rss.feeds (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant. Mirrors app_center.installations.scope/scope_id so the
    -- Authz path is the same: 'user'|'org', plus the principal id.
    scope           text NOT NULL,
    scope_id        text NOT NULL,

    feed_url        text NOT NULL,
    site_url        text,                                   -- discovered <link rel="alternate" href>
    title           text NOT NULL,
    description     text,
    icon_url        text,                                   -- absolute URL or 'cas:<sha256>'
    category        text,                                   -- user-assigned grouping (free-form)

    refresh_sec     int  NOT NULL DEFAULT 1800,             -- 30 min default

    -- Conditional GET state. Fetcher reads these on each tick; on 304
    -- we only update last_fetched_at + last_status='ok'.
    etag            text,
    last_modified   text,

    last_fetched_at timestamptz,
    last_status     text NOT NULL DEFAULT '',               -- ok|stale|error|disabled
    last_error      text NOT NULL DEFAULT '',
    consecutive_failures int NOT NULL DEFAULT 0,            -- backoff hint

    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (scope, scope_id, feed_url)
);

-- Hot list query: "show me this user's feeds, enabled first".
CREATE INDEX IF NOT EXISTS feeds_scope_enabled_idx
    ON rss.feeds (scope, scope_id, enabled);

-- Scheduler fan-out: "find feeds due for refresh".
-- Partial because most feeds at any moment are NOT due (last_fetched
-- + refresh_sec > now). Tiny index, fast scan.
CREATE INDEX IF NOT EXISTS feeds_refresh_due_idx
    ON rss.feeds (last_fetched_at NULLS FIRST)
    WHERE enabled = true;


-- ─── 2. entries — fetched items ──────────────────────────────────
--
-- One row per (feed, guid). Dedup is at the SQL layer via the unique
-- constraint — fetcher does INSERT … ON CONFLICT (feed_id, guid) DO
-- NOTHING which makes a re-fetch of the same upstream payload a no-op.
--
-- hash is a stronger dedup key for cross-feed matching (someone
-- subscribes to both Atom and RSS variants of the same blog) — radar
-- candidate dedup uses this.
--
-- read_at is NULL for unread; we don't have a separate boolean. Saves
-- a column; sorting by read_at NULLS FIRST gives "unread first".
--
-- content_html goes through Miniflux sanitizer (XSS-safe). content_text
-- is the readability-extracted plain text used for matcher (cheaper
-- than re-parsing HTML on every match).

CREATE TABLE IF NOT EXISTS rss.entries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    feed_id         uuid NOT NULL REFERENCES rss.feeds(id) ON DELETE CASCADE,

    -- Upstream identity. Atom <id> / RSS <guid>. Some feeds reuse the
    -- URL when no guid is present; we accept either.
    guid            text NOT NULL,

    url             text,
    title           text NOT NULL,
    author          text,

    content_html    text,                                   -- sanitized
    content_text    text,                                   -- plain-text projection

    published_at    timestamptz,
    fetched_at      timestamptz NOT NULL DEFAULT now(),
    read_at         timestamptz,                            -- NULL = unread
    starred         boolean NOT NULL DEFAULT false,

    -- Cross-feed dedup key. sha256(lower(title) || '|' || coalesce(url,''))
    -- computed by the fetcher. BYTEA(32).
    hash            bytea NOT NULL,

    UNIQUE (feed_id, guid)
);

CREATE INDEX IF NOT EXISTS entries_feed_published_idx
    ON rss.entries (feed_id, published_at DESC NULLS LAST);

-- Unread count hot query — partial index keeps it tiny once user
-- catches up.
CREATE INDEX IF NOT EXISTS entries_unread_idx
    ON rss.entries (feed_id)
    WHERE read_at IS NULL;

CREATE INDEX IF NOT EXISTS entries_hash_idx
    ON rss.entries (hash);


-- ─── 3. watch_rules — per-user keyword rules ─────────────────────
--
-- A rule is a saved query: (match_any, match_all, exclude) over titles,
-- restricted to a set of sources. sources values:
--   '*'              — every source the user has access to
--   'rss:<feed_id>'  — a specific RSS feed (must belong to scope)
--   '<board_id>'     — a rankings.boards row id (e.g. 'weibo')
--
-- Match semantics:
--   match_any — any element appearing in title (case-insensitive
--               substring) passes
--   match_all — every element must appear
--   When both non-empty: (any) AND (all) must both pass
--   exclude   — any match in exclude REJECTS the candidate
-- v0.1 is keyword-only; regex / NLP arrives in P3.
--
-- on_hit_badge controls Sidebar badge severity escalation (info|warn
-- |error). on_hit_notify is a list of channel IDs from notify.channels
-- (existing infra). cooldown_sec is per (rule, title_hash) — set to 0
-- to allow every match (fire-hose mode, not recommended).

CREATE TABLE IF NOT EXISTS rss.watch_rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    scope           text NOT NULL,
    scope_id        text NOT NULL,

    name            text NOT NULL,                          -- user-facing label
    match_any       text[] NOT NULL DEFAULT '{}',
    match_all       text[] NOT NULL DEFAULT '{}',
    exclude         text[] NOT NULL DEFAULT '{}',

    -- Source filter. '{*}' means all; otherwise a list of source ids
    -- — 'rss:<feed_uuid>' or rankings board id.
    sources         text[] NOT NULL DEFAULT '{*}',

    on_hit_badge    text NOT NULL DEFAULT 'warn',           -- info|warn|error
    on_hit_notify   text[] NOT NULL DEFAULT '{}',           -- channel ids

    cooldown_sec    int  NOT NULL DEFAULT 1800,             -- 30 min default

    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS watch_rules_scope_enabled_idx
    ON rss.watch_rules (scope, scope_id, enabled);


-- ─── 4. watch_hits — match flow ──────────────────────────────────
--
-- Append-only journal of (rule, candidate) matches. The matcher writes
-- one row per fired hit; the cooldown check queries
--   SELECT MAX(hit_at) FROM rss.watch_hits
--   WHERE rule_id = $1 AND title_hash = $2
-- before deciding to fire — no UNIQUE on (rule_id, title_hash)
-- because we WANT a second hit after the cooldown elapses.
--
-- read_at distinguishes "alert acknowledged" from "still red". The
-- Badge unread count reads WHERE read_at IS NULL.
--
-- notified is a write-after-dispatch flag — we set it true once the
-- notify.send fanout returns (not waited on for client UI).

CREATE TABLE IF NOT EXISTS rss.watch_hits (
    id              bigserial PRIMARY KEY,

    rule_id         uuid NOT NULL REFERENCES rss.watch_rules(id) ON DELETE CASCADE,
    hit_at          timestamptz NOT NULL DEFAULT now(),

    -- 'rss:<feed_uuid>' | rankings board id
    source          text NOT NULL,

    title           text NOT NULL,
    url             text,

    -- sha256(lower(title)). Same algorithm as rss.entries.hash and
    -- rankings.items_seen.title_hash so cross-source dedup works.
    title_hash      bytea NOT NULL,

    notified        boolean NOT NULL DEFAULT false,
    read_at         timestamptz                              -- NULL = unread
);

CREATE INDEX IF NOT EXISTS watch_hits_rule_hit_idx
    ON rss.watch_hits (rule_id, hit_at DESC);

-- Cooldown lookup. (rule_id, title_hash, hit_at DESC) lets us LIMIT 1
-- the latest hit per (rule, title) without a sort.
CREATE INDEX IF NOT EXISTS watch_hits_cooldown_idx
    ON rss.watch_hits (rule_id, title_hash, hit_at DESC);

-- Unread radar count: WHERE read_at IS NULL — partial keeps it tight.
CREATE INDEX IF NOT EXISTS watch_hits_unread_idx
    ON rss.watch_hits (rule_id)
    WHERE read_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rss.watch_hits;
DROP TABLE IF EXISTS rss.watch_rules;
DROP TABLE IF EXISTS rss.entries;
DROP TABLE IF EXISTS rss.feeds;
DROP SCHEMA IF EXISTS rss;

-- +goose StatementEnd
