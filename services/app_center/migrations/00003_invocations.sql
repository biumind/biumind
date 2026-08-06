-- +goose Up
-- +goose StatementBegin

-- ─── Invocations audit ledger (M1.3) ───────────────────────────────
--
-- Every action call (regardless of caller: user / agent / scheduler /
-- channel / webhook) writes one row here. Used for:
--   * Audit / compliance — who called what, when
--   * Rate limiting — recent invocations per install for sliding window
--   * Billing — token + cost aggregation rolls up here for marketplace
--     usage pricing (v2.5)
--   * Debugging — trace_id links to OTel spans
--
-- Partitioned by month would be ideal at scale but premature here. We
-- index on (install_id, occurred_at DESC) for the common dashboard
-- query "recent invocations for this app". When volume warrants it,
-- migrate to declarative partitioning by occurred_at month.
--
-- No FK on install_id because invocations OUTLIVE installs: when a user
-- uninstalls, billing for already-incurred costs must remain queryable.
-- Cleanup is by retention policy (cron deletes rows older than 365d),
-- not cascade.

CREATE SCHEMA IF NOT EXISTS app_center;

CREATE TABLE IF NOT EXISTS app_center.invocations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Snapshot the install context. install_id may dangle (post-uninstall)
    -- but identifier is the durable handle.
    install_id      uuid NOT NULL,
    app_id          text NOT NULL,
    identifier      text NOT NULL,
    action          text NOT NULL,

    -- Caller distinguishes UI clicks from Agent tool calls from
    -- automation. Used to bucket dashboards and apply different rate
    -- limits per source (e.g. webhook may be 10x stricter than user).
    caller          text NOT NULL
                         CHECK (caller IN ('user', 'agent', 'scheduler', 'channel', 'webhook')),
    caller_id       text NOT NULL DEFAULT '',                -- user_id / agent_session_id / etc.

    trace_id        text NOT NULL DEFAULT '',
    duration_ms     int,
    tokens_in       int,
    tokens_out      int,
    cost_micro_usd  bigint,

    -- Status taxonomy: ok = normal completion; error = app code threw;
    -- denied = Authz refused; timeout = wall clock exceeded.
    status          text NOT NULL
                         CHECK (status IN ('ok', 'error', 'denied', 'timeout')),
    error_code      text NOT NULL DEFAULT '',

    occurred_at     timestamptz NOT NULL DEFAULT now()
);

-- Most common queries: dashboard "recent for install", action perf
-- "last N for app+action", trace link "find by trace_id".
CREATE INDEX IF NOT EXISTS invocations_install_recent_idx
    ON app_center.invocations (install_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_app_recent_idx
    ON app_center.invocations (app_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_action_recent_idx
    ON app_center.invocations (identifier, action, occurred_at DESC);

CREATE INDEX IF NOT EXISTS invocations_trace_idx
    ON app_center.invocations (trace_id)
    WHERE trace_id <> '';

-- Status filter is highly selective (most calls are 'ok'); partial
-- index keeps the error-investigation lookup fast without bloating the
-- common path.
CREATE INDEX IF NOT EXISTS invocations_errors_idx
    ON app_center.invocations (occurred_at DESC)
    WHERE status IN ('error', 'denied', 'timeout');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS app_center.invocations;

-- +goose StatementEnd
