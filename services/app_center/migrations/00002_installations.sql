-- +goose Up
-- +goose StatementBegin

-- ─── Installations + per-Agent enablement (M1.2) ────────────────────
--
-- One row per (scope, scope_id, identifier) — a tenant (org or user)
-- can hold at most one installation of a given app slug at a time.
-- Multiple versions are NOT installed concurrently; upgrade replaces
-- the row. To support pinning across upgrades we store version on the
-- installation, not just a FK to apps.id.
--
-- Why we don't FK to app_center.apps(id):
--   * apps catalogue rows can be GC'd (deprecated marketplace entries)
--     while live installations still reference them in audit history.
--     We snapshot identifier + version on the install row instead and
--     resolve to the live apps row by (identifier, version) at runtime
--     with a fallback to "latest active" if the exact version is gone.
--
-- agent_apps lives here (not in runtime schema) because the source of
-- truth is the installation lifecycle: install → grant default agent;
-- uninstall → cascade-delete grants. Runtime queries this table on
-- every tool dispatch (cached) to gate Agent → App calls (decision §21#7).

CREATE SCHEMA IF NOT EXISTS app_center;

CREATE TABLE IF NOT EXISTS app_center.installations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant key. scope ∈ {org, user}; scope_id is the org_id or
    -- user_id depending on scope. Org-installed apps are visible to
    -- every org member; user-installed are private to that user.
    scope                 text NOT NULL CHECK (scope IN ('org', 'user')),
    scope_id              uuid NOT NULL,

    -- Snapshot of the apps row at install time. identifier survives
    -- catalogue edits; version is the exact version this tenant
    -- installed (also the basis for upgrade diffing).
    app_id                text NOT NULL,                     -- soft ref to app_center.apps(id)
    identifier            text NOT NULL,                     -- denormalised for query speed
    version               text NOT NULL,

    enabled               boolean NOT NULL DEFAULT true,

    -- pinned_version: when set, auto-upgrade is suppressed even if a
    -- new version with permission-subset diff is published. NULL =
    -- floats to latest compatible.
    pinned_version        text,

    -- Permissions the user/admin actually agreed to at install time.
    -- May be a SUBSET of manifest.permissions (optional permissions
    -- decision §8.3). Runtime checks both (manifest declares, install
    -- grants) — the intersection is what's allowed.
    permissions_granted   text[] NOT NULL DEFAULT '{}',

    -- App-private config. OAuth tokens / API keys MUST NOT live here
    -- (use vault.credentials with a credential_ref pointer); plain
    -- preferences and non-secret settings only. Webhook HMAC secret
    -- lives here in encrypted form (sealed by vault DEK).
    config                jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Forced-by-org marker (decision §21#3). When true, member can
    -- still 'enabled = false' to opt out, but cannot uninstall and
    -- cannot remove from sidebar.
    forced                boolean NOT NULL DEFAULT false,

    installed_at          timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    installed_by          uuid,                              -- soft ref to identity.users.id

    -- Same tenant, same slug = at most one row. Re-install replaces
    -- (UPSERT in app code).
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

-- Per-agent grants. Default agent is auto-granted at install time
-- (decision §21#7); other agents need explicit grant via PATCH
-- /v1/apps/installs/{id}/agents.
--
-- ON DELETE CASCADE on install_id only — agent_id has no FK because
-- agents are owned by Identity and may be deleted out of band; we
-- index for cleanup queries instead.
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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS app_center.agent_apps;
DROP TABLE IF EXISTS app_center.installations;

-- +goose StatementEnd
