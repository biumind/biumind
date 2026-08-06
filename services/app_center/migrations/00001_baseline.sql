-- ─── App Center 基线 (squashed baseline) ───────────────────────────
--
-- 本文件由原 00001–00023（缺 00011，共 22 个 migration）squash 而成，
-- 只面向全新部署：空库从 00001 顺序应用即得最终 schema。不再考虑存量
-- 库升级路径。
--
-- 三个 schema：app_center（平台管道）、rss（RSS 应用域数据）、
-- rankings（热榜快照）。共 26 张表（app_center 7 + rss 16 + rankings 3）。
--
-- 折叠决策（相对原历史的差异点）：
--   * rankings.boards：00008 的 color 列并入 CREATE TABLE；seed 直接
--     写最终态 43 行（00008 UPSERT 的 43 行，name/color/expected_domain
--     为 00008 覆盖后的值）。00008 DELETE 的 3 个死源（hacker-news /
--     ruanyifeng / yahoo-finance）本就不在 43 行内，故最终就是 43 行。
--   * rss.entries：00009 的 10 个 AI 列并入 CREATE；embedding 直接定义
--     为 vector(1024)（00014 从 1536 改过来的最终态）；embedding_model
--     并入；entries_embedding_idx 只建 1024 版 ivfflat 一次；
--     entries_pending_embed_idx（00014）保留；00021 的 enclosure_url /
--     enclosure_type / transcribed_at、00022 的 transcript_segments
--     及 entries_untranscribed_idx 并入。
--   * rss.watch_rules：00012 的 4 列并入；semantic_embedding 直接
--     vector(1024)；semantic_embedding_model（00014）并入。
--   * rss.feeds：00018 forced、00020 kind 并入 CREATE。
--   * app_center.installations：00005 的 webhook_secret 并入 CREATE。
--   * rss.user_interests.interest_centroid 建为 vector(1024) —— 这是
--     对原 00014 遗漏的修正：00014 把 entries.embedding 和
--     watch_rules.semantic_embedding 从 1536 切到 1024，却漏改了本列。
--     Go 代码 (internal/rss/interest/recompute.go) 的 centroid 就是
--     rss.entries.embedding（bge-m3, 1024d）的均值，1536 下 UPSERT
--     必然报错，故基线直接建 1024。
--   * 00014 整个文件蒸发（维度切换 + 索引 DROP/CREATE 往返在基线里
--     不存在）。
--
-- 原样保留：notify_event + events_notify 触发器（原 00001）、
-- prune_sidebar_on_uninstall 触发器（原 00004，挂在 installations 上）。
-- 表间刻意无 FK 的地方（apps.icon_file_hash、installations.app_id、
-- invocations.install_id 等）保持无 FK。
--
-- 目标环境：PostgreSQL 16 + pgvector。

-- +goose Up
-- +goose StatementBegin

-- pgvector: embedding 列。gen_random_uuid 在 PG 13+ 已内置，pgcrypto
-- 仅为显式兜底（幂等，装了不亏）。
CREATE EXTENSION IF NOT EXISTS "vector";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE SCHEMA IF NOT EXISTS app_center;
CREATE SCHEMA IF NOT EXISTS rss;
CREATE SCHEMA IF NOT EXISTS rankings;

-- +goose StatementEnd

-- ═══════════════════════════ app_center ═══════════════════════════

-- +goose StatementBegin

-- 1. Catalogue. One row per (identifier, version).
CREATE TABLE IF NOT EXISTS app_center.apps (
    id              text PRIMARY KEY,                         -- "app_<ulid>" assigned by application
    identifier      text NOT NULL,                            -- kebab-case slug, scoped per-source
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    source          text NOT NULL
                         CHECK (source IN ('bundled', 'org', 'marketplace', 'user_webview')),
    org_id          uuid,                                     -- non-NULL for source='org'

    -- Full manifest JSON — typed via biuapp.Manifest in app code, but
    -- stored as jsonb so third-party manifests with new fields don't
    -- require column migrations on every minor schema bump.
    manifest        jsonb NOT NULL,
    manifest_hash   char(64) NOT NULL,                        -- sha256 of canonical manifest YAML

    -- code_hash: bundled = git rev / marketplace = OCI image digest /
    -- org user_webview = NULL.
    code_hash       char(64),
    -- ed25519 signature of (manifest_hash || code_hash).
    signature       text,

    -- Files CAS sha256 of the icon image (PNG/SVG); plain text, no FK.
    icon_file_hash  char(64),

    category        text NOT NULL DEFAULT 'utility'
                         CHECK (category IN ('productivity', 'content', 'data',
                                              'comm', 'dev', 'utility')),
    status          text NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'deprecated', 'suspended', 'beta')),

    -- semver. Same identifier may have multiple rows (one per published
    -- version). Installation rows pin a specific version.
    version         text NOT NULL,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (identifier, version)
);

CREATE INDEX IF NOT EXISTS apps_identifier_idx
    ON app_center.apps (identifier);

CREATE INDEX IF NOT EXISTS apps_status_idx
    ON app_center.apps (status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS apps_source_idx
    ON app_center.apps (source);

CREATE INDEX IF NOT EXISTS apps_org_idx
    ON app_center.apps (org_id)
    WHERE org_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS apps_category_idx
    ON app_center.apps (category);

-- 2. Events ledger. Every mutation in any app_center.* table must
--    insert a row here in the same tx (invariant I4).
CREATE TABLE IF NOT EXISTS app_center.events (
    id           bigserial PRIMARY KEY,
    scope        text NOT NULL,                               -- e.g. "app:org:<uuid>" / "install:<uuid>"
    actor_type   text NOT NULL,                               -- "user" / "agent" / "system" / "admin"
    actor_id     text NOT NULL DEFAULT '',
    event_type   text NOT NULL,                               -- "app.installed" / "app.uninstalled" / ...
    payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
    schema_ver   int NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),

    -- Outbox marker. NULL = unpublished.
    published_at timestamptz
);

CREATE INDEX IF NOT EXISTS events_scope_id_idx
    ON app_center.events (scope, id);

CREATE INDEX IF NOT EXISTS events_created_at_idx
    ON app_center.events (created_at);

-- Partial index — scans only the unpublished tail.
CREATE INDEX IF NOT EXISTS events_outbox_pending_idx
    ON app_center.events (id)
    WHERE published_at IS NULL;

-- LISTEN/NOTIFY fast-path for in-process Realtime fanout.
CREATE OR REPLACE FUNCTION app_center.notify_event() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('app_center_events', json_build_object(
        'scope',   NEW.scope,
        'id',      NEW.id,
        'type',    NEW.event_type,
        'payload', NEW.payload
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS events_notify ON app_center.events;
CREATE TRIGGER events_notify
    AFTER INSERT ON app_center.events
    FOR EACH ROW EXECUTE FUNCTION app_center.notify_event();

-- +goose StatementEnd

-- +goose StatementBegin

-- 3. Installations + per-Agent enablement.
--    No FK to app_center.apps(id): catalogue rows can be GC'd while live
--    installations still reference them; we snapshot identifier+version.
CREATE TABLE IF NOT EXISTS app_center.installations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant key. scope ∈ {org, user}.
    scope                 text NOT NULL CHECK (scope IN ('org', 'user')),
    scope_id              uuid NOT NULL,

    -- Snapshot of the apps row at install time.
    app_id                text NOT NULL,                     -- soft ref to app_center.apps(id)
    identifier            text NOT NULL,                     -- denormalised for query speed
    version               text NOT NULL,

    enabled               boolean NOT NULL DEFAULT true,

    -- pinned_version: when set, auto-upgrade is suppressed.
    pinned_version        text,

    -- Permissions actually agreed to at install time (may be a SUBSET
    -- of manifest.permissions).
    permissions_granted   text[] NOT NULL DEFAULT '{}',

    -- App-private config (non-secret only).
    config                jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Forced-by-org marker.
    forced                boolean NOT NULL DEFAULT false,

    -- 32 random bytes for HMAC-SHA256 signing of inbound webhook
    -- callbacks (原 00005 并入). NULL when the manifest declares no
    -- webhook triggers.
    webhook_secret        bytea,

    installed_at          timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    installed_by          uuid,                              -- soft ref to identity.users.id

    UNIQUE (scope, scope_id, identifier)
);

CREATE INDEX IF NOT EXISTS installs_scope_idx
    ON app_center.installations (scope, scope_id);

CREATE INDEX IF NOT EXISTS installs_app_idx
    ON app_center.installations (app_id);

CREATE INDEX IF NOT EXISTS installs_identifier_idx
    ON app_center.installations (identifier);

CREATE INDEX IF NOT EXISTS installs_enabled_idx
    ON app_center.installations (scope, scope_id)
    WHERE enabled = true;

-- Per-agent grants. ON DELETE CASCADE on install_id only — agent_id has
-- no FK because agents are owned by Identity and may be deleted out of band.
CREATE TABLE IF NOT EXISTS app_center.agent_apps (
    agent_id    uuid NOT NULL,
    install_id  uuid NOT NULL REFERENCES app_center.installations(id) ON DELETE CASCADE,
    enabled     boolean NOT NULL DEFAULT true,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, install_id)
);

CREATE INDEX IF NOT EXISTS agent_apps_install_idx
    ON app_center.agent_apps (install_id);

CREATE INDEX IF NOT EXISTS agent_apps_agent_enabled_idx
    ON app_center.agent_apps (agent_id)
    WHERE enabled = true;

-- 4. Invocations audit ledger. No FK on install_id because invocations
--    OUTLIVE installs (billing must remain queryable post-uninstall).
CREATE TABLE IF NOT EXISTS app_center.invocations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    install_id      uuid NOT NULL,
    app_id          text NOT NULL,
    identifier      text NOT NULL,
    action          text NOT NULL,

    caller          text NOT NULL
                         CHECK (caller IN ('user', 'agent', 'scheduler', 'channel', 'webhook')),
    caller_id       text NOT NULL DEFAULT '',                -- user_id / agent_session_id / etc.

    trace_id        text NOT NULL DEFAULT '',
    duration_ms     int,
    tokens_in       int,
    tokens_out      int,
    cost_micro_usd  bigint,

    status          text NOT NULL
                         CHECK (status IN ('ok', 'error', 'denied', 'timeout')),
    error_code      text NOT NULL DEFAULT '',

    occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS invocations_install_recent_idx
    ON app_center.invocations (install_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_app_recent_idx
    ON app_center.invocations (app_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_action_recent_idx
    ON app_center.invocations (identifier, action, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_trace_idx
    ON app_center.invocations (trace_id)
    WHERE trace_id <> '';

CREATE INDEX IF NOT EXISTS invocations_errors_idx
    ON app_center.invocations (occurred_at DESC)
    WHERE status IN ('error', 'denied', 'timeout');

-- 5. Sidebar layouts (per-user, per-scope desktop|mobile).
CREATE TABLE IF NOT EXISTS app_center.sidebar_layouts (
    user_id           uuid NOT NULL,                          -- soft ref to identity.users.id
    scope             text NOT NULL CHECK (scope IN ('desktop', 'mobile')),

    items             jsonb NOT NULL DEFAULT '[]'::jsonb,

    -- Optimistic lock counter; PUT mismatch → 409.
    version           int NOT NULL DEFAULT 1 CHECK (version >= 1),

    updated_by_device text NOT NULL DEFAULT '',

    updated_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, scope)
);

-- Cascading cleanup: uninstall → prune sidebar items.
CREATE OR REPLACE FUNCTION app_center.prune_sidebar_on_uninstall() RETURNS trigger AS $$
BEGIN
    UPDATE app_center.sidebar_layouts
       SET items      = COALESCE(
                          (SELECT jsonb_agg(item)
                             FROM jsonb_array_elements(items) AS item
                            WHERE NOT (item->>'kind' = 'app'
                                       AND item->>'ref' = OLD.id::text)),
                          '[]'::jsonb
                        ),
           version    = version + 1,
           updated_at = now()
     WHERE items @> jsonb_build_array(jsonb_build_object('kind', 'app',
                                                          'ref',  OLD.id::text));
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sidebar_prune_on_uninstall ON app_center.installations;
CREATE TRIGGER sidebar_prune_on_uninstall
    AFTER DELETE ON app_center.installations
    FOR EACH ROW EXECUTE FUNCTION app_center.prune_sidebar_on_uninstall();

-- 6. Scheduler jobs — durable cron / webhook / inbox job rows.
CREATE TABLE IF NOT EXISTS app_center.scheduler_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    install_id      uuid NOT NULL REFERENCES app_center.installations(id) ON DELETE CASCADE,
    identifier      text NOT NULL,                              -- app slug (denormalised)

    name            text NOT NULL,                              -- manifest trigger name
    kind            text NOT NULL CHECK (kind IN ('cron', 'webhook', 'inbox')),

    -- Cron-only fields.
    cron_expr       text,                                       -- standard 5-field
    if_inactive_for text,                                       -- Go time.ParseDuration string

    -- Webhook-only fields.
    webhook_path    text,

    -- Inbox-only field.
    inbox_pattern   text,

    -- Action to dispatch, copied from manifest.triggers[].action.
    action          text NOT NULL,

    -- Static input merged with the trigger event payload at fire time.
    input           jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Dispatcher state.
    next_run        timestamptz,                                -- NULL for webhook / inbox
    locked_until    timestamptz,                                -- in-flight claim window
    last_run_at     timestamptz,
    last_status     text NOT NULL DEFAULT '',                   -- "ok" | "error" | "skipped" | ""
    last_error      text NOT NULL DEFAULT '',
    consecutive_failures int NOT NULL DEFAULT 0,

    enabled         boolean NOT NULL DEFAULT true,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (install_id, name)
);

CREATE INDEX IF NOT EXISTS scheduler_jobs_next_run_idx
    ON app_center.scheduler_jobs (next_run)
    WHERE enabled = true AND kind = 'cron' AND next_run IS NOT NULL;

CREATE INDEX IF NOT EXISTS scheduler_jobs_install_idx
    ON app_center.scheduler_jobs (install_id);

CREATE INDEX IF NOT EXISTS scheduler_jobs_webhook_idx
    ON app_center.scheduler_jobs (install_id, webhook_path)
    WHERE kind = 'webhook';

-- +goose StatementEnd

-- ══════════════════════════════ rss ═══════════════════════════════

-- +goose StatementBegin

-- 1. feeds — user/org-scoped subscriptions.
--    00018 forced（组织强制订阅）、00020 kind（来源显示标签）并入。
CREATE TABLE IF NOT EXISTS rss.feeds (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant. Mirrors app_center.installations.scope/scope_id.
    scope           text NOT NULL,
    scope_id        text NOT NULL,

    feed_url        text NOT NULL,
    site_url        text,                                   -- discovered <link rel="alternate" href>
    title           text NOT NULL,
    description     text,
    icon_url        text,                                   -- absolute URL or 'cas:<sha256>'
    category        text,                                   -- user-assigned grouping (free-form)

    refresh_sec     int  NOT NULL DEFAULT 1800,             -- 30 min default

    -- Conditional GET state.
    etag            text,
    last_modified   text,

    last_fetched_at timestamptz,
    last_status     text NOT NULL DEFAULT '',               -- ok|stale|error|disabled
    last_error      text NOT NULL DEFAULT '',
    consecutive_failures int NOT NULL DEFAULT 0,            -- backoff hint

    -- 组织强制订阅标记 (原 00018)：成员不可删。
    forced          bool NOT NULL DEFAULT false,

    -- 来源显示标签 (原 00020)：'rss'|'wechat'|'x'|'podcast'。
    kind            text NOT NULL DEFAULT 'rss',

    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (scope, scope_id, feed_url)
);

-- Hot list query: "show me this user's feeds, enabled first".
CREATE INDEX IF NOT EXISTS feeds_scope_enabled_idx
    ON rss.feeds (scope, scope_id, enabled);

-- Scheduler fan-out: "find feeds due for refresh".
CREATE INDEX IF NOT EXISTS feeds_refresh_due_idx
    ON rss.feeds (last_fetched_at NULLS FIRST)
    WHERE enabled = true;

-- org 成员 feeds_list join 强制源用；partial 只覆盖 forced 行。
CREATE INDEX IF NOT EXISTS feeds_forced_idx
    ON rss.feeds (scope, scope_id)
    WHERE forced;

-- 2. entries — fetched items.
--    00009 的 AI 列、00014 的 embedding vector(1024) + embedding_model、
--    00021 的 enclosure 三列、00022 的 transcript_segments 全部并入。
CREATE TABLE IF NOT EXISTS rss.entries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    feed_id         uuid NOT NULL REFERENCES rss.feeds(id) ON DELETE CASCADE,

    -- Upstream identity. Atom <id> / RSS <guid>.
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

    -- Cross-feed dedup key. sha256(lower(title) || '|' || coalesce(url,'')).
    hash            bytea NOT NULL,

    -- ── AI digest 列 (原 00009) ──
    ai_takeaway     text,
    ai_bullets      jsonb NOT NULL DEFAULT '[]'::jsonb,
    ai_topics       text[] NOT NULL DEFAULT '{}',
    ai_importance   smallint NOT NULL DEFAULT 0,
    ai_lang         text NOT NULL DEFAULT '',
    ai_processed_at timestamptz,
    ai_error        text NOT NULL DEFAULT '',
    -- 最终态 1024 维 (bge-m3; 原 00009 建 1536, 00014 改 1024)。
    embedding       vector(1024),
    -- 标记每行 embedding 由哪个模型生成 (原 00014)。
    embedding_model text,
    word_count      int NOT NULL DEFAULT 0,
    reading_seconds int NOT NULL DEFAULT 0,

    -- ── 播客 enclosure (原 00021) ──
    enclosure_url   text,
    enclosure_type  text,
    transcribed_at  timestamptz,                            -- transcribe worker 已填充标记

    -- ── 句级转写分段 (原 00022)：[{id,start,end,text}] ──
    transcript_segments jsonb,

    UNIQUE (feed_id, guid)
);

CREATE INDEX IF NOT EXISTS entries_feed_published_idx
    ON rss.entries (feed_id, published_at DESC NULLS LAST);

-- Unread count hot query — partial index keeps it tiny.
CREATE INDEX IF NOT EXISTS entries_unread_idx
    ON rss.entries (feed_id)
    WHERE read_at IS NULL;

CREATE INDEX IF NOT EXISTS entries_hash_idx
    ON rss.entries (hash);

-- digest worker 未处理队列 (原 00009)。
CREATE INDEX IF NOT EXISTS entries_ai_unprocessed_idx
    ON rss.entries (fetched_at)
    WHERE ai_processed_at IS NULL AND ai_error = '';

-- ivfflat cosine (1024 维最终态，只建一次; lists=100 适合 ~1M 行以下)。
CREATE INDEX IF NOT EXISTS entries_embedding_idx
    ON rss.entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- worker 找待 embed 的 entries: 没 embedding + 有内容 (原 00014)。
CREATE INDEX IF NOT EXISTS entries_pending_embed_idx
    ON rss.entries (fetched_at DESC)
    WHERE embedding IS NULL
      AND (length(coalesce(content_text,''))+length(coalesce(title,''))) > 20;

-- 播客转写 backfill scan (原 00021)。
CREATE INDEX IF NOT EXISTS entries_untranscribed_idx
    ON rss.entries (fetched_at)
    WHERE enclosure_url IS NOT NULL AND transcribed_at IS NULL;

-- 3. watch_rules — per-user keyword + semantic rules.
--    00012 的 semantic 列与 actions 并入；semantic_embedding 直接
--    vector(1024)（00014 最终态）；semantic_embedding_model 并入。
CREATE TABLE IF NOT EXISTS rss.watch_rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    scope           text NOT NULL,
    scope_id        text NOT NULL,

    name            text NOT NULL,                          -- user-facing label
    match_any       text[] NOT NULL DEFAULT '{}',
    match_all       text[] NOT NULL DEFAULT '{}',
    exclude         text[] NOT NULL DEFAULT '{}',

    -- Source filter. '{*}' means all; otherwise 'rss:<feed_uuid>' or
    -- rankings board id.
    sources         text[] NOT NULL DEFAULT '{*}',

    on_hit_badge    text NOT NULL DEFAULT 'warn',           -- info|warn|error
    on_hit_notify   text[] NOT NULL DEFAULT '{}',           -- channel ids

    cooldown_sec    int  NOT NULL DEFAULT 1800,             -- 30 min default

    -- ── 语义匹配 (原 00012) ──
    semantic_query           text,
    semantic_threshold       real NOT NULL DEFAULT 0.78,
    semantic_embedding       vector(1024),
    semantic_embedding_model text,

    -- 命中后动作链 [{type:'notify'|'wiki'|'task'|'skill', config:{...}}]
    actions         jsonb NOT NULL DEFAULT '[]'::jsonb,

    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS watch_rules_scope_enabled_idx
    ON rss.watch_rules (scope, scope_id, enabled);

-- 4. watch_hits — append-only match journal.
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

CREATE INDEX IF NOT EXISTS watch_hits_cooldown_idx
    ON rss.watch_hits (rule_id, title_hash, hit_at DESC);

CREATE INDEX IF NOT EXISTS watch_hits_unread_idx
    ON rss.watch_hits (rule_id)
    WHERE read_at IS NULL;

-- 5. entry_marks — 用户的 star/pin/wiki/shared 标记 (原 00009)。
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

-- 6. entry_clusters — Today 近重复聚合 (原 00010)。
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

-- 7. user_interests — 兴趣 centroid + 话题榜 (原 00010)。
--    interest_centroid 建为 vector(1024)：这是对原 00014 遗漏的修正。
--    recompute.go 写入的是 rss.entries.embedding (bge-m3, 1024d) 的均值，
--    原历史的 1536 在 00014 之后必然写入报错。
CREATE TABLE IF NOT EXISTS rss.user_interests (
    user_id            text PRIMARY KEY,
    interest_centroid  vector(1024),
    top_topics         text[] NOT NULL DEFAULT '{}',
    sample_count       int NOT NULL DEFAULT 0,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- 8. reading_log — 追加型行为账本 (原 00010)。
CREATE TABLE IF NOT EXISTS rss.reading_log (
    id           bigserial PRIMARY KEY,
    user_id      text NOT NULL,
    entry_id     uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    event        text NOT NULL CHECK (event IN
                   ('opened','read_full','starred','wiki','task','dismissed','shared')),
    seconds      int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS reading_log_user_recent_idx
    ON rss.reading_log (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS reading_log_entry_idx
    ON rss.reading_log (entry_id);

-- 9. hit_clusters — 同规则同题命中聚合 (原 00012)。
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

-- 10. action_runs — 命中动作执行账本 (原 00012)。
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

-- 11. starter_packs — 主题包 (原 00013)。
CREATE TABLE IF NOT EXISTS rss.starter_packs (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    icon_emoji  text NOT NULL DEFAULT '',
    feeds       jsonb NOT NULL,
    sort_order  int NOT NULL DEFAULT 100,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 12. audio_cache — TTS 简报缓存, 24h TTL (原 00015)。
CREATE TABLE IF NOT EXISTS rss.audio_cache (
    user_id        text        NOT NULL,
    generated_date date        NOT NULL,
    -- 内容签名: SHA256(headline_ids || missed_ids || generated_at_truncated_hour).
    content_hash   bytea       NOT NULL,
    script         text        NOT NULL,        -- 朗读脚本 (debug/降级回退用)
    mp3            bytea       NOT NULL,        -- audio/mpeg, 24kbps mono, ~ 100KB
    voice          text        NOT NULL,
    model          text        NOT NULL,
    characters     int         NOT NULL DEFAULT 0,
    duration_ms    int         NOT NULL DEFAULT 0,  -- 0 = 未估算; client 加载后写回
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,         -- created_at + 24h
    PRIMARY KEY (user_id, generated_date)
);

CREATE INDEX IF NOT EXISTS audio_cache_expires_idx
    ON rss.audio_cache (expires_at);

-- 13. weekly_runs — 周报 cron 幂等键 (原 00016)。
CREATE TABLE IF NOT EXISTS rss.weekly_runs (
    user_id    text        NOT NULL,
    iso_week   text        NOT NULL,             -- 'YYYY-Www', ISO 8601
    ran_at     timestamptz NOT NULL DEFAULT now(),
    page_id    text        NOT NULL DEFAULT '',  -- wiki page id
    summary    text        NOT NULL DEFAULT '',
    error      text        NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, iso_week)
);

CREATE INDEX IF NOT EXISTS weekly_runs_recent_idx
    ON rss.weekly_runs (ran_at DESC);

-- 14. shared_views — 公开只读分享 (原 00017)。
CREATE TABLE IF NOT EXISTS rss.shared_views (
    token           text        PRIMARY KEY,
    owner_user_id   text        NOT NULL,
    owner_org_id    text        NOT NULL DEFAULT '',
    -- 'today' | 'radar' | 'saved' | 'inbox'
    view_kind       text        NOT NULL,
    filter_json     jsonb       NOT NULL DEFAULT '{}',
    scope           text        NOT NULL DEFAULT 'user',
    scope_id        text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz
);

CREATE INDEX IF NOT EXISTS shared_views_owner_idx
    ON rss.shared_views (owner_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS shared_views_active_idx
    ON rss.shared_views (expires_at)
    WHERE revoked_at IS NULL;

-- 15. user_preferences — 单行 jsonb 偏好 (原 00019)。
CREATE TABLE IF NOT EXISTS rss.user_preferences (
    user_id     text        PRIMARY KEY,
    config      jsonb       NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 16. reading_progress — 跨设备续读位置 (原 00023)。
CREATE TABLE IF NOT EXISTS rss.reading_progress (
    user_id    text NOT NULL,
    entry_id   uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    pct        real NOT NULL DEFAULT 0 CHECK (pct >= 0 AND pct <= 1),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, entry_id)
);

-- entries ON DELETE CASCADE 按 entry_id 找行;无此索引则级联删要全表扫。
CREATE INDEX IF NOT EXISTS reading_progress_entry_idx
    ON rss.reading_progress (entry_id);

-- +goose StatementEnd

-- +goose StatementBegin

-- ─── starter_packs seed (原 00013, UPSERT 风格保留) ───────────────

INSERT INTO rss.starter_packs (id, name, description, icon_emoji, feeds, sort_order) VALUES
    ('ai',        'AI / 大模型',
     '前沿 LLM / 多模态 / Agent 研究与产品动态',
     '🤖',
     '[
        {"url":"https://www.anthropic.com/news/rss.xml","title":"Anthropic Blog"},
        {"url":"https://openai.com/blog/rss.xml","title":"OpenAI Blog"},
        {"url":"https://blog.google/technology/ai/rss/","title":"Google AI Blog"},
        {"url":"https://huggingface.co/blog/feed.xml","title":"Hugging Face"},
        {"url":"https://simonwillison.net/atom/everything/","title":"Simon Willison"}
      ]'::jsonb,
     1),
    ('finance',   '投资 / 财经',
     '宏观 / 个股 / 一级市场 / 风口资讯',
     '💹',
     '[
        {"url":"https://wallstreetcn.com/rss/","title":"华尔街见闻"},
        {"url":"https://www.cls.cn/rss","title":"财联社"},
        {"url":"https://36kr.com/feed","title":"36氪"}
      ]'::jsonb,
     2),
    ('design',    '设计 / 产品',
     '产品打磨 / 设计趋势 / 用户体验',
     '🎨',
     '[
        {"url":"https://uxdesign.cc/feed","title":"UX Collective"},
        {"url":"https://www.nngroup.com/feed/rss/","title":"NN/g"},
        {"url":"https://sspai.com/feed","title":"少数派"}
      ]'::jsonb,
     3),
    ('tech',      '科技 / 工程',
     '工程师必看的科技深度文章',
     '⚙️',
     '[
        {"url":"https://hnrss.org/frontpage","title":"Hacker News"},
        {"url":"https://www.solidot.org/index.rss","title":"Solidot"},
        {"url":"https://www.ithome.com/rss/","title":"IT之家"},
        {"url":"https://feeds.feedburner.com/ruanyifeng","title":"阮一峰的网络日志"}
      ]'::jsonb,
     4),
    ('chinese',   '中文资讯',
     '聚合中文圈热门内容与时评',
     '🇨🇳',
     '[
        {"url":"https://www.thepaper.cn/rss_chosen.jsp","title":"澎湃新闻"},
        {"url":"https://www.zhihu.com/rss","title":"知乎日报"},
        {"url":"https://www.juejin.cn/rss","title":"掘金"}
      ]'::jsonb,
     5)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    icon_emoji = EXCLUDED.icon_emoji,
    feeds = EXCLUDED.feeds,
    sort_order = EXCLUDED.sort_order;

-- +goose StatementEnd

-- ════════════════════════════ rankings ════════════════════════════

-- +goose StatementBegin

-- Rankings schema 刻意无租户：所有用户共享同一份热榜快照。

-- 1. boards — newsnow 源 + 调度状态。00008 的 color 列并入 CREATE。
CREATE TABLE IF NOT EXISTS rankings.boards (
    id              text PRIMARY KEY,                       -- newsnow source id
    name            text NOT NULL,                          -- 显示名
    color           text NOT NULL DEFAULT 'gray',           -- 原 00008; newsnow 视觉色
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

-- 2. snapshots — one row per scheduler tick that produced fresh data.
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

-- 3. items_seen — per-(board, title) memory for "新进榜" detection.
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

-- ─── Seed: 43 个默认 boards（最终态）─────────────────────────────
--
-- 原 00007 seed 14 个 → 00008 UPSERT 扩到 43 并覆盖 name/color →
-- 00008 DELETE 3 个死源 (hacker-news / ruanyifeng / yahoo-finance，
-- 均不在下面 43 行内)。基线直接写最终态 43 行。

INSERT INTO rankings.boards (id, name, color, expected_domain) VALUES
    ('wallstreetcn-quick',     '华尔街见闻 · 快讯',         'blue',    'wallstreetcn.com'),
    ('wallstreetcn-news',      '华尔街见闻 · 最新',         'blue',    'wallstreetcn.com'),
    ('wallstreetcn-hot',       '华尔街见闻 · 最热',         'blue',    'wallstreetcn.com'),
    ('pcbeta-windows11',       '远景论坛 Win11',            'blue',    'pcbeta.com'),
    ('cls-telegraph',          '财联社 · 电报',             'red',     'cls.cn'),
    ('cls-depth',              '财联社 · 深度',             'red',     'cls.cn'),
    ('cls-hot',                '财联社 · 热门',             'red',     'cls.cn'),
    ('xueqiu-hotstock',        '雪球 · 热门股票',           'blue',    'xueqiu.com'),
    ('bilibili-hot-search',    '哔哩哔哩 · 热搜',           'blue',    'bilibili.com'),
    ('bilibili-hot-video',     '哔哩哔哩 · 热门视频',       'blue',    'bilibili.com'),
    ('bilibili-ranking',       '哔哩哔哩 · 排行榜',         'blue',    'bilibili.com'),
    ('chongbuluo-latest',      '虫部落 · 最新',             'green',   'chongbuluo.com'),
    ('chongbuluo-hot',         '虫部落 · 最热',             'green',   'chongbuluo.com'),
    ('tencent-hot',            '腾讯新闻 · 综合早报',       'blue',    'qq.com'),
    ('qqvideo-tv-hotsearch',   '腾讯视频 · 热搜榜',         'blue',    'qq.com'),
    ('iqiyi-hot-ranklist',     '爱奇艺 · 热播榜',           'green',   'iqiyi.com'),
    ('36kr-quick',             '36氪 · 快讯',               'blue',    '36kr.com'),
    ('36kr-renqi',             '36氪 · 人气榜',             'blue',    '36kr.com'),
    ('github-trending-today',  'GitHub Trending · Today',   'gray',    'github.com'),
    ('zhihu',                  '知乎 · 热榜',               'blue',    'zhihu.com'),
    ('weibo',                  '微博 · 实时热搜',           'red',     'weibo.com'),
    ('baidu',                  '百度 · 热搜',               'blue',    'baidu.com'),
    ('toutiao',                '今日头条',                  'red',     'toutiao.com'),
    ('douyin',                 '抖音 · 热点',               'gray',    'douyin.com'),
    ('kuaishou',               '快手 · 热榜',               'orange',  'kuaishou.com'),
    ('ithome',                 'IT 之家',                   'red',     'ithome.com'),
    ('ifeng',                  '凤凰网 · 24 小时热文',      'red',     'ifeng.com'),
    ('tieba',                  '百度贴吧 · 热议',           'blue',    'baidu.com'),
    ('thepaper',               '澎湃新闻 · 热榜',           'red',     'thepaper.cn'),
    ('sputniknewscn',          '卫星通讯社',                'orange',  'sputniknews.cn'),
    ('cankaoxiaoxi',           '参考消息',                  'red',     'cankaoxiaoxi.com'),
    ('solidot',                'Solidot',                   'teal',    'solidot.org'),
    ('producthunt',            'Product Hunt',              'red',     'producthunt.com'),
    ('sspai',                  '少数派',                    'red',     'sspai.com'),
    ('nowcoder',               '牛客',                      'green',   'nowcoder.com'),
    ('juejin',                 '掘金',                      'blue',    'juejin.cn'),
    ('xueqiu',                 '雪球',                      'blue',    'xueqiu.com'),
    ('hupu',                   '虎扑 · 步行街热帖',         'orange',  'hupu.com'),
    ('jin10',                  '金十数据 · 快讯',           'blue',    'jin10.com'),
    ('gelonghui',              '格隆汇 · 事件',             'blue',    'gelonghui.com'),
    ('coolapk',                '酷安 · 今日最热',           'green',   'coolapk.com'),
    ('pcbeta',                 '远景论坛',                  'blue',    'pcbeta.com'),
    ('zaobao',                 '联合早报',                  'red',     'zaobao.com')
ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 逆序 drop，先子表后父表，最后 drop 三个 schema。
-- 扩展 (vector / pgcrypto) 不 drop —— 可能被同库其他服务共用。

-- rankings
DROP TABLE IF EXISTS rankings.items_seen;
DROP TABLE IF EXISTS rankings.snapshots;
DROP TABLE IF EXISTS rankings.boards;

-- rss (先依赖 entries / watch_rules / watch_hits / feeds 的表)
DROP TABLE IF EXISTS rss.action_runs;
DROP TABLE IF EXISTS rss.hit_clusters;
DROP TABLE IF EXISTS rss.watch_hits;
DROP TABLE IF EXISTS rss.reading_progress;
DROP TABLE IF EXISTS rss.reading_log;
DROP TABLE IF EXISTS rss.entry_clusters;
DROP TABLE IF EXISTS rss.entry_marks;
DROP TABLE IF EXISTS rss.entries;
DROP TABLE IF EXISTS rss.watch_rules;
DROP TABLE IF EXISTS rss.feeds;
DROP TABLE IF EXISTS rss.audio_cache;
DROP TABLE IF EXISTS rss.weekly_runs;
DROP TABLE IF EXISTS rss.shared_views;
DROP TABLE IF EXISTS rss.user_preferences;
DROP TABLE IF EXISTS rss.user_interests;
DROP TABLE IF EXISTS rss.starter_packs;

-- app_center
DROP TRIGGER IF EXISTS sidebar_prune_on_uninstall ON app_center.installations;
DROP TRIGGER IF EXISTS events_notify ON app_center.events;
DROP TABLE IF EXISTS app_center.scheduler_jobs;
DROP TABLE IF EXISTS app_center.agent_apps;
DROP TABLE IF EXISTS app_center.sidebar_layouts;
DROP TABLE IF EXISTS app_center.invocations;
DROP TABLE IF EXISTS app_center.installations;
DROP TABLE IF EXISTS app_center.events;
DROP TABLE IF EXISTS app_center.apps;
DROP FUNCTION IF EXISTS app_center.prune_sidebar_on_uninstall();
DROP FUNCTION IF EXISTS app_center.notify_event();

DROP SCHEMA IF EXISTS rankings;
DROP SCHEMA IF EXISTS rss;
DROP SCHEMA IF EXISTS app_center;

-- +goose StatementEnd
