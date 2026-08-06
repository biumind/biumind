-- +goose Up
-- +goose StatementBegin

-- ─── usage_log — per-request billing detail (MC-M1.5, revised) ─────
--
-- Why this table exists (deviation from earlier plan):
-- The original Dev Plan §M1.5 specified "ClickHouse usage_log column
-- extension". Investigation showed BiuMind has NO ClickHouse instance
-- — `deploy/docker-compose/README.md:185` explicitly notes "Postgres
-- (no ClickHouse); ClickHouse cluster is future architecture". The
-- existing admin Usage view (admin/src/views/UsageView.vue) reads
-- Prometheus counters (biumind_hub_llm_tokens_total etc.), and no SQL
-- detail table exists today.
--
-- So we land usage_log in Postgres. Two consumers:
--   1. Prometheus metrics (untouched) — aggregate dashboards stay fast.
--   2. This table — admin "drill into channel X's last 7 days of
--      requests" view, dual-currency settlement audit, and the data
--      source for any ClickHouse migration when it lands.
--
-- Volume sanity check: 1M requests/month × ~400B/row = ~400MB/month.
-- Manageable in Postgres for several months. Add a 90-day TTL when
-- volume crosses ~10M/month or when ClickHouse arrives.
--
-- Cross-schema FKs not used (services boundary): user_id, model_id,
-- channel_id stored as uuid but not constrained. Cleanup is the app's
-- job.

CREATE TABLE IF NOT EXISTS model_relay.usage_log (
    id                     bigserial PRIMARY KEY,

    -- Tenant + routing identity
    user_id                uuid NOT NULL,
    model_id               uuid NOT NULL,
    channel_id             uuid NOT NULL,
    model_code             varchar(128) NOT NULL,                -- denormalised for admin queries
    upstream_model         varchar(128) NOT NULL,                -- denormalised; pricing audit
    user_plan              text NOT NULL DEFAULT 'free'
                                CHECK (user_plan IN ('free', 'pro', 'team')),

    -- Token counts (provider-reported)
    input_tokens           bigint NOT NULL DEFAULT 0,
    output_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_tokens     bigint NOT NULL DEFAULT 0,
    cache_read_tokens      bigint NOT NULL DEFAULT 0,

    -- Cost in source currency (whatever pricing.currency was at request time)
    cost_origin_currency   text NOT NULL CHECK (cost_origin_currency IN ('CNY', 'USD')),
    cost_origin_amount     numeric(18, 8) NOT NULL DEFAULT 0,

    -- Cost in user's settlement currency, after fx_rates lookup
    cost_settle_currency   text NOT NULL CHECK (cost_settle_currency IN ('CNY', 'USD')),
    cost_settle_amount     numeric(18, 8) NOT NULL DEFAULT 0,

    -- Snapshot of fx_rates.rate at request time (for retroactive audit
    -- — fx_rates row may have been updated since)
    fx_rate                numeric(10, 6) NOT NULL DEFAULT 1.0,

    -- Latency / outcome
    latency_ms             int NOT NULL DEFAULT 0,
    status                 text NOT NULL DEFAULT 'ok'
                                CHECK (status IN ('ok', 'error', 'rate_limited', 'cancelled')),
    error_code             text NOT NULL DEFAULT '',

    -- request_id matches the trace id surfaced to the client; useful
    -- for correlating with structured logs.
    request_id             text NOT NULL DEFAULT '',

    created_at             timestamptz NOT NULL DEFAULT now()
);

-- Hot paths:
-- 1. "Show me user X's last N requests" → (user_id, created_at desc)
-- 2. "Channel Y's recent failures" → (channel_id, created_at desc) WHERE status='error'
-- 3. "Aggregate spend over period for billing" → BRIN on created_at
--    works well at table size, but a btree on (created_at) is enough
--    until table crosses ~10M rows.
CREATE INDEX IF NOT EXISTS usage_log_user_time_idx
    ON model_relay.usage_log (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_channel_time_idx
    ON model_relay.usage_log (channel_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_model_time_idx
    ON model_relay.usage_log (model_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_errors_idx
    ON model_relay.usage_log (created_at DESC)
    WHERE status != 'ok';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS model_relay.usage_log;

-- +goose StatementEnd
