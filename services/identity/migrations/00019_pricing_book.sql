-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- billing.pricing_book — 统一价格表 (chat / aigc / digital_human / hotparse)
--
-- 双层定价:
--   cost_*  → 上游真实成本 (内部审计, 不暴露给前端)
--   markup_ratio (默认 3.0) → 毛利系数
--   list_*  = cost_* * markup_ratio  → 用户标价 (毫分, CNY)
--
-- 历史价反查: 改价 = INSERT 新 row + UPDATE 旧 row 的 effective_to. 退款查
-- credit_logs.created_at 命中区间, 保审计可回溯.
--
-- 单位:
--   ref_type='chat'         → cost_basis='per_mtok', 单位 millicents/百万 token (CNY)
--   ref_type='aigc_image'   → cost_basis='per_call', 单位 millicents/次
--   ref_type='aigc_video'   → cost_basis='per_call', pricing_key 含 resolution+duration
--   ref_type='digital_human'→ cost_basis='per_second'
--
-- 汇率: USD→CNY 按 7.2 锁定 (财务可季度调整, 改 markup_ratio 比改 cost 安全).
--
-- 设计来源: docs/BiuMind-Billing-Redesign.md §3.2.
-- ═══════════════════════════════════════════════════════════════════

CREATE SCHEMA IF NOT EXISTS billing;

CREATE TABLE billing.pricing_book (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- 路由 key
    ref_type                 text NOT NULL CHECK (ref_type IN
                             ('chat','aigc_image','aigc_video','digital_human','hotparse_parse')),
    pricing_key              text NOT NULL,

    -- 计价基准
    cost_basis               text NOT NULL CHECK (cost_basis IN
                             ('per_call','per_mtok','per_second','per_image_megapixel')),

    -- 成本侧 (CNY millicents, 内部审计用; per_mtok 时为「百万 token 成本」)
    cost_input_per_unit      bigint NOT NULL DEFAULT 0 CHECK (cost_input_per_unit >= 0),
    cost_output_per_unit     bigint NOT NULL DEFAULT 0 CHECK (cost_output_per_unit >= 0),
    cost_cache_read          bigint NOT NULL DEFAULT 0 CHECK (cost_cache_read >= 0),
    cost_cache_write         bigint NOT NULL DEFAULT 0 CHECK (cost_cache_write >= 0),

    -- 标价侧
    markup_ratio             numeric(6,4) NOT NULL DEFAULT 3.0000
                             CHECK (markup_ratio > 0),
    -- 单次硬下限 / 上限 (毫分)
    min_charge               bigint NOT NULL DEFAULT 0 CHECK (min_charge >= 0),
    max_charge_per_request   bigint CHECK (max_charge_per_request IS NULL OR max_charge_per_request > 0),

    -- 生效区间
    enabled                  boolean NOT NULL DEFAULT true,
    effective_from           timestamptz NOT NULL DEFAULT now(),
    effective_to             timestamptz,

    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),

    UNIQUE (ref_type, pricing_key, effective_from)
);

-- 当前生效价格 (effective_to IS NULL) 的快速命中
CREATE INDEX billing_pricing_book_active
    ON billing.pricing_book (ref_type, pricing_key, enabled, effective_from DESC)
    WHERE effective_to IS NULL;

-- 历史价反查 (退款时按 ts 命中)
CREATE INDEX billing_pricing_book_history
    ON billing.pricing_book (ref_type, pricing_key, effective_from);

-- ─── seed: chat 模型 (8 个常用) ─────────────────────────────────────
-- USD→CNY 7.2; 1 USD/M tokens = 7200 millicents/M tokens
-- 数据源: model_relay/internal/pricing/pricing.go (2026-05 锁定)

INSERT INTO billing.pricing_book
    (ref_type, pricing_key, cost_basis,
     cost_input_per_unit, cost_output_per_unit, cost_cache_read, cost_cache_write,
     markup_ratio, min_charge)
VALUES
    -- ─── Anthropic ─────────────────────────────────────────────────
    -- haiku-4-5: $1/$5/$0.1/$1.25 per M tok
    ('chat', 'claude-haiku-4-5',  'per_mtok',  7200,   36000,   720,   9000,   3.0000, 1000),
    -- sonnet-4-5: $3/$15/$0.3/$3.75
    ('chat', 'claude-sonnet-4-5', 'per_mtok',  21600,  108000,  2160,  27000,  3.0000, 1000),
    -- opus-4-7: $15/$75/$1.5/$18.75
    ('chat', 'claude-opus-4-7',   'per_mtok',  108000, 540000,  10800, 135000, 3.0000, 1000),

    -- ─── OpenAI ────────────────────────────────────────────────────
    -- gpt-4o: $2.5/$10/$1.25
    ('chat', 'gpt-4o',            'per_mtok',  18000,  72000,   9000,  0,      3.0000, 1000),
    -- gpt-4o-mini: $0.15/$0.6/$0.075
    ('chat', 'gpt-4o-mini',       'per_mtok',  1080,   4320,    540,   0,      3.0000, 1000),
    -- o1: $15/$60/$7.5
    ('chat', 'o1',                'per_mtok',  108000, 432000,  54000, 0,      3.0000, 1000),

    -- ─── DeepSeek (CNY 直标) ────────────────────────────────────────
    -- deepseek-chat: ¥1/¥2/¥0.1 per M tok
    ('chat', 'deepseek-chat',     'per_mtok',  1000,   2000,    100,   0,      2.5000, 100),

    -- ─── 字节豆包 (CNY 直标) ────────────────────────────────────────
    -- doubao-1-5-pro: ¥0.8/¥2 per M tok, cache 10% read
    ('chat', 'doubao-1-5-pro',    'per_mtok',  800,    2000,    80,    0,      2.5000, 100);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS billing.pricing_book;
DROP SCHEMA IF EXISTS billing;

-- +goose StatementEnd
