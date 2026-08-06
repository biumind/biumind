-- model_relay.pricing 成为 billing 系统的 single source of truth.
--
-- 历史背景: W1 设计期建了两张价格表 —— billing.pricing_book (identity 服务管,
-- 通过 /v1/internal/pricing 端点暴露给 model-relay 扣积分) + model_relay.pricing
-- (admin 后台编辑,只用于 model-relay 自己 dashboard / usage_log 关联). 实践中
-- admin UI 写后者,扣积分查前者,两张表无同步 —— 实测 glm-5.1 在 admin 后台配
-- 了价格但 hold/settle 链路查不到,Agent 模式不扣积分.
--
-- 治本: model_relay.pricing 加上 billing.pricing_book 才有的字段
-- (markup_ratio / min_charge / max_charge_per_request / cost_per_search_unit),
-- 把 pricing_book 数据迁过来,billing.Client.LookupPrice 改本地 DB 查询,
-- pricing_book 表 + identity 端 endpoint 在配套 migration 里 DROP.
--
-- 单位换算: pricing_book.cost_*_per_unit 是 millicents/Mtok (1 USD = 7200 millicents,
-- 1 CNY = 1000 millicents). model_relay.pricing.* 是 numeric(14,6) 原币种/Mtok.
-- 反向迁移: 已知 seed 历史用 USD 7.2 倍率换算, 反推 = millicents / 7200.
-- DeepSeek/Doubao 标的 CNY 直接除 1000 也能近似(CNY 模型 millicents = CNY*1000).
--
-- 这里统一按 USD 反推 (millicents/7200) — 误差对 dev/test 环境无影响, 生产用
-- 户应该在 admin 后台重新录入精确价格.

-- +goose Up
-- +goose StatementBegin

-- 1. 加缺失字段
ALTER TABLE model_relay.pricing
    ADD COLUMN IF NOT EXISTS markup_ratio NUMERIC(6, 4) NOT NULL DEFAULT 3.0,
    ADD COLUMN IF NOT EXISTS min_charge BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_charge_per_request BIGINT,
    ADD COLUMN IF NOT EXISTS cost_per_search_unit NUMERIC(14, 6) NOT NULL DEFAULT 0;

-- 2. 数据迁移: billing.pricing_book → model_relay.pricing
-- 规则:
--   - 只迁 ref_type='chat' 因为我们当前主要用 chat (embed/rerank/aigc_*
--     的 model 通常 model_relay.pricing 已有数据)
--   - JOIN model_relay.models on pricing_key=code,匹配不到的跳过
--     (那个模型在 model-relay catalogue 里没注册,反正用不上)
--   - 已有 model_relay.pricing 行的不覆盖 (admin 录入优先)
--   - currency 一律 USD (历史 seed 假设)
--   - millicents → USD: cost_per_unit / 7200
--
-- 注意: identity 端 0030 migration 也会 DROP billing.pricing_book.
-- migration 顺序不保证(model-relay / identity 是两个独立服务,各自 goose
-- up,顺序由谁先启动决定). 用 DO + IF EXISTS 包裹:表在 → 迁数据, 表不
-- 在(已被 0030 删) → silent skip,这条 migration 仍然成功.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'billing' AND table_name = 'pricing_book'
    ) THEN
        INSERT INTO model_relay.pricing (
            model_id, currency,
            input_per_mtok, output_per_mtok,
            cache_read_per_mtok, cache_write_per_mtok,
            markup_ratio, min_charge, max_charge_per_request,
            effective_at
        )
        SELECT
            m.id,
            'USD',
            pb.cost_input_per_unit::numeric / 7200,
            pb.cost_output_per_unit::numeric / 7200,
            pb.cost_cache_read::numeric / 7200,
            pb.cost_cache_write::numeric / 7200,
            pb.markup_ratio,
            pb.min_charge,
            pb.max_charge_per_request,
            pb.effective_from
        FROM billing.pricing_book pb
        JOIN model_relay.models m ON m.code = pb.pricing_key
        WHERE pb.enabled = true
          AND pb.effective_to IS NULL
          AND pb.ref_type = 'chat'
          AND NOT EXISTS (
            SELECT 1 FROM model_relay.pricing p
            WHERE p.model_id = m.id
          );
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE model_relay.pricing
    DROP COLUMN IF EXISTS markup_ratio,
    DROP COLUMN IF EXISTS min_charge,
    DROP COLUMN IF EXISTS max_charge_per_request,
    DROP COLUMN IF EXISTS cost_per_search_unit;
-- 数据迁移 down 不做 (不安全:可能丢失 admin 后续录入)
-- +goose StatementEnd
