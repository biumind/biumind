-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W6-6 — 优惠券 + 邀请奖励数据骨架
--
-- 三张表:
--   billing.coupons              — 券模板 (运营创建 / 后台管理)
--   billing.coupon_redemptions   — 用户兑换记录 (一码一行)
--   billing.referrals            — 邀请关系 (邀请人 → 被邀请人)
--
-- 设计来源: docs/BiuMind-Billing-Membership-Dev-Plan.md §7 (W6-7/8).
-- ═══════════════════════════════════════════════════════════════════

-- ─── 1. coupons (券模板) ──────────────────────────
-- 一行一种券. code 是用户输入的兑换码 (大小写不敏感存大写).
-- kind 决定执行逻辑:
--   amount_off    — 立减 N 分 (subscription 续费时直接减价)
--   percent_off   — 立减 N% (跟订阅价格走百分比, 上限 max_amount_cents)
--   credits_grant — 直接发 N 积分 (走 credits.Grant)
--   trial_extend  — 试用期延长 N 天
CREATE TABLE IF NOT EXISTS billing.coupons (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code              text UNIQUE NOT NULL,
    kind              text NOT NULL CHECK (kind IN
                          ('amount_off', 'percent_off', 'credits_grant', 'trial_extend')),

    -- value 通用字段 — 含义按 kind 解释:
    --   amount_off    → cents 数额
    --   percent_off   → 百分比 0-100 (整数, 5 表示 5%)
    --   credits_grant → 积分数
    --   trial_extend  → 天数
    value             bigint NOT NULL CHECK (value > 0),
    -- percent_off 专用 cap (无 cap 设很大值)
    max_amount_cents  bigint NOT NULL DEFAULT 0 CHECK (max_amount_cents >= 0),

    -- 适用范围
    plan_codes        text[] NOT NULL DEFAULT '{}',  -- 空数组 = 不限 plan
    currency          text,                          -- amount_off 时必填; 其他类型 NULL
    once_per_user     boolean NOT NULL DEFAULT true, -- 同 user 只能用一次
    max_total_uses    bigint NOT NULL DEFAULT 0 CHECK (max_total_uses >= 0), -- 0=不限

    -- 时效
    valid_from        timestamptz NOT NULL DEFAULT now(),
    valid_until       timestamptz,                   -- NULL = 永不过期

    -- 状态
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'archived')),

    -- 审计
    description       text NOT NULL DEFAULT '',
    created_by        uuid REFERENCES identity.users(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS coupons_status      ON billing.coupons (status, valid_until);
CREATE INDEX IF NOT EXISTS coupons_kind        ON billing.coupons (kind);

-- ─── 2. coupon_redemptions ─────────────────────────
-- 一行一兑换. 唯一约束 (coupon_id, user_id) 在 once_per_user=true 时由
-- 应用层保证 (查询时挡); 表本身允许多次, 因为有的券支持 once_per_user=false.
CREATE TABLE IF NOT EXISTS billing.coupon_redemptions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id       uuid NOT NULL REFERENCES billing.coupons(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL,
    -- 关联订单 (amount_off / percent_off 减价时关联到具体支付单)
    payment_order_id uuid REFERENCES billing.payment_orders(id) ON DELETE SET NULL,
    -- 关联订阅 (trial_extend 时记到订阅)
    subscription_id uuid REFERENCES billing.subscriptions(id) ON DELETE SET NULL,
    -- 实际生效金额 (amount_off 直接 = value; percent_off 算出 cents; credits_grant 0)
    discount_cents  bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    -- 关联 credit_logs.id (credits_grant 时写; 用于回溯)
    credit_log_id   uuid,
    -- 关联 trial extend 天数 (trial_extend 时写)
    extra_days      int NOT NULL DEFAULT 0,
    redeemed_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS redemptions_user      ON billing.coupon_redemptions (user_id, redeemed_at DESC);
CREATE INDEX IF NOT EXISTS redemptions_coupon    ON billing.coupon_redemptions (coupon_id, redeemed_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS redemptions_unique_per_user
    ON billing.coupon_redemptions (coupon_id, user_id);

-- ─── 3. referrals (邀请关系) ─────────────────────
-- 一行一关系. (inviter_user_id, invitee_user_id) 唯一. invite_code 是邀请人的
-- 长期邀请码 (一人一码), 多人共享同 code.
CREATE TABLE IF NOT EXISTS billing.referrals (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inviter_user_id uuid NOT NULL,
    invitee_user_id uuid NOT NULL,
    invite_code     text NOT NULL,

    -- 防刷三元组 (W4-6 已用同模式)
    invitee_device_fp text NOT NULL DEFAULT '',
    invitee_ip      inet,

    -- 状态机:
    --   pending      — 邀请关系建立, 奖励未发
    --   rewarded     — 双方奖励已发
    --   reverted     — 检测到刷单 / 退款 → 回退奖励
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'rewarded', 'reverted')),

    -- 奖励落账 (审计)
    inviter_credit_log_id uuid,
    invitee_credit_log_id uuid,

    rewarded_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- 一对邀请人/被邀请人只能记一条
CREATE UNIQUE INDEX IF NOT EXISTS referrals_pair_unique
    ON billing.referrals (inviter_user_id, invitee_user_id);

CREATE INDEX IF NOT EXISTS referrals_inviter ON billing.referrals (inviter_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS referrals_invitee ON billing.referrals (invitee_user_id);
CREATE INDEX IF NOT EXISTS referrals_code    ON billing.referrals (invite_code);
CREATE INDEX IF NOT EXISTS referrals_device  ON billing.referrals (invitee_device_fp) WHERE invitee_device_fp <> '';
CREATE INDEX IF NOT EXISTS referrals_ip      ON billing.referrals (invitee_ip) WHERE invitee_ip IS NOT NULL;

-- ─── 4. seed 4 类券模板 (运营 demo, 可在后台增删) ─
INSERT INTO billing.coupons (code, kind, value, max_amount_cents, currency, once_per_user, valid_until, description)
VALUES
  ('NEWUSER20', 'percent_off',   20,  10000, 'CNY', true, NULL, '新人 20% 折扣 (上限 ¥100)'),
  ('GIFT500',   'credits_grant', 500, 0,     NULL,  true, NULL, '500 积分礼包'),
  ('OFF10',     'amount_off',    1000, 0,    'CNY', true, NULL, '立减 ¥10'),
  ('TRIAL14',   'trial_extend',  14,  0,     NULL,  true, NULL, '延长 14 天试用期')
ON CONFLICT (code) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS billing.referrals;
DROP TABLE IF EXISTS billing.coupon_redemptions;
DROP TABLE IF EXISTS billing.coupons;

-- +goose StatementEnd
