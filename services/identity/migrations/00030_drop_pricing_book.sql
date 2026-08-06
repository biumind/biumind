-- 下线 billing.pricing_book — pricing 数据迁到 model_relay.pricing 单 SoT.
--
-- 历史:
--   00019: 建 billing.pricing_book + seed 8 个常用 chat 模型 (haiku/opus/
--          sonnet/gpt-4o/o1/deepseek/doubao)
--   00029: ALTER ref_type/cost_basis 加 embedding/rerank/audio_speech 等
--   00030 (本): DROP TABLE — model-relay 不再走 HTTP 调 identity 查价,
--          identity 端 /v1/internal/pricing 端点 + Service.LookupActivePrice
--          + pricing.go 文件 + pricing_test.go 已在配套 Go change 中删除.
--
-- 数据迁移: model-relay 端 00013_pricing_unify.sql 已把 enabled+effective_to
-- IS NULL 的行迁到 model_relay.pricing (按 model code JOIN). 跑过这条
-- migration 后丢失 model_relay.models 里没注册的模型 — 那些行本来也用不上
-- (没 channel 配置过的模型,relay 路由不到).
--
-- pricing_book 改价历史回看 (effective_to NOT NULL 的旧行) 不再保留 —
-- 实际审计需求由 model_relay.pricing 自己的 effective_at 链满足.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS billing.pricing_book;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 不可逆: 数据已经被 model_relay 端的 migration 吸过去了,这里 down 只
-- 重建空表 schema (不还原 seed),让回滚后表存在但无数据 — 跟 W1 期一样
-- 走 "pricing not found" 不计费 fallback.
CREATE TABLE IF NOT EXISTS billing.pricing_book (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ref_type               text NOT NULL,
    pricing_key            text NOT NULL,
    cost_basis             text NOT NULL,
    cost_input_per_unit    bigint NOT NULL DEFAULT 0,
    cost_output_per_unit   bigint NOT NULL DEFAULT 0,
    cost_cache_read        bigint NOT NULL DEFAULT 0,
    cost_cache_write       bigint NOT NULL DEFAULT 0,
    markup_ratio           numeric(6, 4) NOT NULL DEFAULT 1.0,
    min_charge             bigint NOT NULL DEFAULT 0,
    max_charge_per_request bigint,
    enabled                boolean NOT NULL DEFAULT true,
    effective_from         timestamptz NOT NULL DEFAULT now(),
    effective_to           timestamptz,
    created_by             uuid,
    created_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
