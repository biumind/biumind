-- +goose Up
-- +goose StatementBegin

-- ─── Triggers + webhook secrets (M4.1) ─────────────────────────────
--
-- Two new things land here:
--
-- 1) app_center.scheduler_jobs — durable cron / webhook job rows. The
--    in-process dispatcher polls this table FOR UPDATE SKIP LOCKED so
--    multi-replica deployments share work without double-firing.
--
-- 2) app_center.installations.webhook_secret — random 32B value
--    generated when an installation declares a webhook trigger,
--    used to HMAC-SHA256 sign the inbound /webhooks/app_center/...
--    callback. Stored in cleartext for v1.5; v2.0 wraps it through
--    vault.credentials with a credential_ref pointer (decision §5.1
--    referenced this for OAuth tokens; webhook secrets follow the
--    same path at marketplace ship). Adding the column now means we
--    don't have to rewrite the install path twice.
--
-- We keep the dispatcher state minimal: next_run drives the SELECT,
-- locked_until is the optimistic lock for in-flight executions, and
-- last_run_at + last_status are diagnostic. No queue-of-historical-
-- runs table here — invocations is the audit story; this is the
-- live job state.

CREATE SCHEMA IF NOT EXISTS app_center;

CREATE TABLE IF NOT EXISTS app_center.scheduler_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant of the job. install_id is the source of truth; we
    -- denormalise identifier + scope so the dispatcher can build the
    -- TriggerEvent without an extra join. Nullable install_id is
    -- explicitly forbidden — every job must trace to one install.
    install_id      uuid NOT NULL REFERENCES app_center.installations(id) ON DELETE CASCADE,
    identifier      text NOT NULL,                              -- app slug (denormalised)

    -- Job identity within the install. The triggers map keys on
    -- (install_id, name); UNIQUE catches accidental dups. kind
    -- distinguishes cron jobs from webhook receivers (those that
    -- live here primarily for accounting; the actual webhook entry
    -- is HTTP-driven).
    name            text NOT NULL,                              -- manifest trigger name
    kind            text NOT NULL CHECK (kind IN ('cron', 'webhook', 'inbox')),

    -- Cron-only fields. Other kinds keep these NULL.
    cron_expr       text,                                       -- standard 5-field
    -- Inactive-skip is an "advisory only" flag stored as a duration
    -- string ("24h" / "7d") parsed by Go time.ParseDuration. The
    -- dispatcher checks identity.activity_events before firing.
    if_inactive_for text,

    -- Webhook-only fields.
    webhook_path    text,                                       -- "/callback" — full URL prefix is at HTTP layer

    -- Inbox-only field.
    inbox_pattern   text,

    -- Action to dispatch, copied from manifest.triggers[].action.
    -- Cross-checked against manifest.actions[] at install time, but
    -- the dispatcher re-validates: a manifest change could have
    -- removed the action between install and fire.
    action          text NOT NULL,

    -- Static input merged with the trigger event payload at fire time.
    -- Stored as jsonb so dispatcher can inject ${trigger.fired_at}
    -- variables without parsing strings.
    input           jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Dispatcher state. next_run drives the SELECT; locked_until is
    -- a per-job mutex (claim window) so concurrent app_center
    -- replicas can't double-fire. last_run_at + last_status are
    -- diagnostic / dashboard.
    next_run        timestamptz,                                -- NULL for webhook / inbox (HTTP-driven)
    locked_until    timestamptz,                                -- in-flight claim window
    last_run_at     timestamptz,
    last_status     text NOT NULL DEFAULT '',                   -- "ok" | "error" | "skipped" | ""
    last_error      text NOT NULL DEFAULT '',
    consecutive_failures int NOT NULL DEFAULT 0,                -- throttle backoff hint

    enabled         boolean NOT NULL DEFAULT true,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (install_id, name)
);

-- The hot-path query: "find jobs to fire". next_run ASC LIMIT N gets
-- the heads of each queue; FOR UPDATE SKIP LOCKED gives multi-replica
-- safety. The partial index keeps it small (most rows have next_run
-- in the future).
CREATE INDEX IF NOT EXISTS scheduler_jobs_next_run_idx
    ON app_center.scheduler_jobs (next_run)
    WHERE enabled = true AND kind = 'cron' AND next_run IS NOT NULL;

CREATE INDEX IF NOT EXISTS scheduler_jobs_install_idx
    ON app_center.scheduler_jobs (install_id);

-- Webhook lookup: "given an install_id and path, find the job".
-- Sparse index — most rows are cron, not webhook.
CREATE INDEX IF NOT EXISTS scheduler_jobs_webhook_idx
    ON app_center.scheduler_jobs (install_id, webhook_path)
    WHERE kind = 'webhook';

-- ─── installations.webhook_secret ──────────────────────────────────
--
-- 32 random bytes. NULL for installations whose manifest declares no
-- webhook triggers. Generated once at install time; rotated by
-- uninstall + reinstall (we do NOT support in-place rotation in v1.5
-- because clients holding old secrets would silently break).

ALTER TABLE app_center.installations
    ADD COLUMN IF NOT EXISTS webhook_secret bytea;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE app_center.installations DROP COLUMN IF EXISTS webhook_secret;
DROP TABLE IF EXISTS app_center.scheduler_jobs;

-- +goose StatementEnd
