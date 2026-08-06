-- +goose Up
-- +goose StatementBegin

-- ─── App Center subsystem (Phase AC-V1.5 M1.1 — catalog + events) ──
--
-- This migration lands the catalogue (app_center.apps) plus the events
-- ledger that all subsequent app_center mutations must write to (I4).
-- Subsequent migrations build per-tenant install / audit / sidebar
-- tables on top, all referencing apps.id.
--
-- Design provenance: docs/BiuMind-AppCenter-Design.md §5 "数据库 Schema".
-- Field shapes mirror the planned proto packages/proto/biumind/app_center/v1
-- so the registry can map both directions without translation.
--
-- Cross-schema FKs are deliberately NOT used:
--   * app_center.apps.icon_file_hash references files CAS by sha256
--     plain text (no FK to brain.files / files.global_files): services
--     boundary, no hard coupling.
--   * org_id references identity.organizations only when the apps row
--     has source='org'. We don't FK to keep the apps row alive even if
--     an org is deleted (orphaned bundled apps are still meaningful in
--     the catalogue history).
--
-- The events table mirrors brain.events shape (scope/actor/event_type/
-- payload/schema_ver/created_at) plus the outbox column added later in
-- brain (00012_events_outbox.sql) so we don't have to retrofit it. The
-- LISTEN/NOTIFY trigger and durable poller can both consume from day 1.

CREATE SCHEMA IF NOT EXISTS app_center;

-- 1. Catalogue. One row per (identifier, version). source distinguishes
--    where the app came from; bundled rows are inserted at service boot
--    by the SDK (see Registry.Register), org rows by admin upload, and
--    marketplace rows by the catalogue fetcher (v2.5).
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
    -- org user_webview = NULL. Used by signature verification and
    -- by upgrade detection (see M15).
    code_hash       char(64),
    -- ed25519 signature of (manifest_hash || code_hash). Marketplace
    -- entries MUST have this; bundled / org may omit (validated in app
    -- code, not at the SQL level — keeps this schema neutral).
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

    -- A given (identifier, version) is unique globally. Marketplace
    -- enforces scoped identifiers (`<author>/<slug>`) at submission
    -- time so bundled vs marketplace cannot collide. See decision §21#8.
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
--    insert a row here in the same tx (invariant I4). Consumers:
--    LISTEN/NOTIFY listeners for low-latency UI updates, outbox poller
--    for durable bridge to NATS / Realtime.
CREATE TABLE IF NOT EXISTS app_center.events (
    id           bigserial PRIMARY KEY,
    scope        text NOT NULL,                               -- e.g. "app:org:<uuid>" / "install:<uuid>"
    actor_type   text NOT NULL,                               -- "user" / "agent" / "system" / "admin"
    actor_id     text NOT NULL DEFAULT '',
    event_type   text NOT NULL,                               -- "app.installed" / "app.uninstalled" / ...
    payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
    schema_ver   int NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),

    -- Outbox marker. NULL = unpublished; the poller scans WHERE
    -- published_at IS NULL FOR UPDATE SKIP LOCKED, publishes via
    -- realtime/NATS, then stamps. Mirrors brain.events outbox added in
    -- brain/00012; we land it on day 1 to skip the future retrofit.
    published_at timestamptz
);

CREATE INDEX IF NOT EXISTS events_scope_id_idx
    ON app_center.events (scope, id);

CREATE INDEX IF NOT EXISTS events_created_at_idx
    ON app_center.events (created_at);

-- Partial index — scans only the unpublished tail. Stays tiny in
-- steady state; poller's SELECT cost stays flat regardless of total
-- ledger size.
CREATE INDEX IF NOT EXISTS events_outbox_pending_idx
    ON app_center.events (id)
    WHERE published_at IS NULL;

-- LISTEN/NOTIFY fast-path for in-process Realtime fanout. The outbox
-- poller is the durability floor; this trigger is the low-latency
-- accelerator. Channel name 'app_center_events' is fixed; any
-- subscriber must match exactly.
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

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS events_notify ON app_center.events;
DROP FUNCTION IF EXISTS app_center.notify_event();
DROP TABLE IF EXISTS app_center.events;
DROP TABLE IF EXISTS app_center.apps;

-- We DO drop the schema here because this is the very first migration
-- in the app_center namespace; no other migration owns it. Subsequent
-- migrations (00002+) reuse the schema via CREATE SCHEMA IF NOT EXISTS.
DROP SCHEMA IF EXISTS app_center;

-- +goose StatementEnd
