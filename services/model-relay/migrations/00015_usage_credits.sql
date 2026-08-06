-- +goose Up
-- +goose StatementBegin

-- ─── usage_log.credits_charged ───────────────────────────────────
--
-- Additive column: the settled list-price credits for each request, so the
-- client 数据统计 · 用量 (usage) page can show per-call + daily + monthly
-- spend in 积分 (the user-visible currency) from a single source.
--
-- The authoritative credits ledger remains identity.credit_logs; this column
-- is model-relay's local denormalised view, written by writeUsageLogAsync
-- with the same amount finalizeBilling settles (requestState.settleCredits).
-- Nullable + default 0 so historical rows (pre-migration) read as 0 spend.

ALTER TABLE model_relay.usage_log
    ADD COLUMN IF NOT EXISTS credits_charged bigint NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE model_relay.usage_log DROP COLUMN IF EXISTS credits_charged;

-- +goose StatementEnd
