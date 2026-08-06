-- +goose Up
-- +goose StatementBegin

-- ─── model_relay seed data (MC-M1.3) ────────────────────────────────
--
-- Two pieces of data the application assumes exist on day 1:
--
-- 1) The 'default' model_group. ModelResolver short-circuits group
--    filtering when a user has no explicit memberships AND the model
--    is bound to 'default' — i.e. the MVP "everyone sees default group"
--    semantic. If this row is missing, fresh installs route every model
--    to 0 channels (because no model is bound to anything).
--
-- 2) Initial fx_rates for USD↔CNY at 7.20. Manual rate; admin updates
--    via /v1/admin/fx-rates. The Pricing resolver and Usage writer both
--    look up rates by (from, to) and will refuse to settle if the row
--    is missing — better to have a stale-but-present rate than a hard
--    NULL fail at runtime.
--
-- Idempotency: ON CONFLICT DO NOTHING on every insert. This migration
-- is safe to re-run, and DOES NOT clobber an admin-edited row.

-- ─── 1. default model group ────────────────────────────────────────
-- Fixed UUID so application code can reference it as a constant
-- without a lookup. Format chosen to be obviously the "default" group
-- on visual inspection.
INSERT INTO model_relay.model_groups (id, code, name, owner_type, owner_id, description, status)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'default',
    'Default',
    'system',
    '',
    'Built-in group every authenticated user implicitly belongs to. MVP: all models default-bound here.',
    'active'
)
ON CONFLICT (code) DO NOTHING;

-- ─── 2. initial fx_rates ───────────────────────────────────────────
-- 2026-05-30 reference rates. Admin should refresh weekly; system
-- starts warning the UI after 14 days (Design §10 risks).
INSERT INTO model_relay.fx_rates (from_currency, to_currency, rate, source)
VALUES
    ('USD', 'CNY', 7.200000, 'manual'),
    ('CNY', 'USD', 0.138889, 'manual'),
    ('USD', 'USD', 1.000000, 'manual'),
    ('CNY', 'CNY', 1.000000, 'manual')
ON CONFLICT (from_currency, to_currency) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 撤销 seed 不撤销 schema；只清掉本 migration 显式插入的行。
-- 用户改过的行（updated_by 非空）保留 — 避免 down 后 admin 数据丢失。
DELETE FROM model_relay.fx_rates
    WHERE source = 'manual'
      AND updated_by IS NULL
      AND (from_currency, to_currency) IN (
          ('USD','CNY'), ('CNY','USD'), ('USD','USD'), ('CNY','CNY')
      );

DELETE FROM model_relay.model_groups
    WHERE id = '00000000-0000-0000-0000-000000000001'
      AND code = 'default';

-- +goose StatementEnd
