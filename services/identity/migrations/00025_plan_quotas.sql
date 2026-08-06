-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W4-1 — 套餐月度配额 + 用户用量
--
-- 引入 "扣减优先级第一档" — 每月免费额度 (quota), 配额走完才落到时效
-- 积分, 时效用完才到永久积分 (现有 packages 顺序). 单位与 credits 一致
-- (积分), 跟 credit_logs.delta 同口径.
--
-- 与现有 packages 体系区别:
--   * packages — 充值 / 赠送 / plan_grant 落地的实例, 退款回填到包
--   * plan_quotas / user_quota_usage — 不落地为包, 月度计数器 (扣到的
--     credits 蒸发, 不退还; 退款只回填到 packages)
--
-- 下游影响:
--   credit_logs.consume_breakdown_json 多 source='quota' 一段 (旧数据
--   未填该字段, scanner 默认 'package' 兼容)
--
-- 设计来源: docs/BiuMind-Billing-Membership-Dev-Plan.md §5 (W4-1).
-- ═══════════════════════════════════════════════════════════════════

-- ─── 1. plan_quotas — 每档 plan 每个业务的月度配额字典 ─────────────
-- 一行一组合 (plan_id × ref_type). monthly_amount=0 表示该档此业务无
-- 免费额度 (走纯余额). enterprise 行运维可后台调.
CREATE TABLE IF NOT EXISTS billing.plan_quotas (
    plan_id          uuid NOT NULL REFERENCES billing.plans(id) ON DELETE CASCADE,
    ref_type         text NOT NULL CHECK (ref_type IN ('chat_message', 'aigc_task')),
    monthly_amount   bigint NOT NULL DEFAULT 0 CHECK (monthly_amount >= 0),
    description      text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, ref_type)
);

-- ─── 2. user_quota_usage — 每用户每业务的当前周期用量 ──────────────
-- 一行一 (user_id × ref_type). period_start / period_end 锚定当前自然
-- 月 (UTC); cron 月初推进 + 重置 used_amount=0. used_amount 单调递增
-- (周期内). 不存在的行视作 used=0 + period 落到当月.
CREATE TABLE IF NOT EXISTS identity.user_quota_usage (
    user_id          uuid NOT NULL,
    ref_type         text NOT NULL CHECK (ref_type IN ('chat_message', 'aigc_task')),
    period_start     timestamptz NOT NULL,
    period_end       timestamptz NOT NULL CHECK (period_end > period_start),
    used_amount      bigint NOT NULL DEFAULT 0 CHECK (used_amount >= 0),
    -- 周期内用过的最大 monthly_amount 缓存 — 用于 GET /v1/subscriptions/me
    -- 显示进度条; cron 重置时一并刷新成当时的 plan 配额.
    monthly_amount   bigint NOT NULL DEFAULT 0 CHECK (monthly_amount >= 0),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, ref_type)
);

CREATE INDEX IF NOT EXISTS user_quota_usage_period
    ON identity.user_quota_usage (period_end);

-- ─── 3. seed 4 档 × 2 业务 = 8 行 ───────────────────────────────────
-- credits 单位; 单价参考 model_relay.pricing_book 1 token ≈ 0.05 credits
-- (粗略对照). 数值给 W4 PoC 用, 后续业务调.
INSERT INTO billing.plan_quotas (plan_id, ref_type, monthly_amount, description)
SELECT id, 'chat_message',
    CASE code
        WHEN 'free'       THEN 0
        WHEN 'pro'        THEN 5000      -- ≈ 100K token / 月
        WHEN 'team'       THEN 30000     -- ≈ 600K token / 月
        WHEN 'enterprise' THEN 1000000   -- ≈ 20M token / 月 (定制可调)
    END,
    code || ' chat 每月免费配额'
FROM billing.plans
ON CONFLICT (plan_id, ref_type) DO UPDATE SET
    monthly_amount = EXCLUDED.monthly_amount,
    description    = EXCLUDED.description,
    updated_at     = now();

INSERT INTO billing.plan_quotas (plan_id, ref_type, monthly_amount, description)
SELECT id, 'aigc_task',
    CASE code
        WHEN 'free'       THEN 0
        WHEN 'pro'        THEN 1000      -- ≈ 10 张图 / 月 (按 100 credits/图)
        WHEN 'team'       THEN 5000
        WHEN 'enterprise' THEN 100000
    END,
    code || ' aigc 每月免费配额'
FROM billing.plans
ON CONFLICT (plan_id, ref_type) DO UPDATE SET
    monthly_amount = EXCLUDED.monthly_amount,
    description    = EXCLUDED.description,
    updated_at     = now();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS identity.user_quota_usage;
DROP TABLE IF EXISTS billing.plan_quotas;

-- +goose StatementEnd
