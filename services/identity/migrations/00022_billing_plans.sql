-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W2-1 — 会员体系数据骨架
--
-- 目标: identity.users.plan 字段保留作 denorm, 真源切到 billing.subscriptions.
--
-- 4 张表:
--   billing.plans                — 4 档套餐字典 (free/pro/team/enterprise)
--   billing.subscriptions        — 用户订阅状态机 + 周期
--   billing.subscription_events  — 状态变更审计流
--   billing.payment_orders       — 支付订单 (Stripe + 国内支付预留)
--
-- 设计来源:
--   docs/BiuMind-Billing-Membership-Dev-Plan.md §3 (W2-1..W2-2)
--   docs/BiuMind-Billing-Redesign.md §4 (会员体系)
--
-- 不变量:
--   * identity.users.plan 字段保留 — 现有所有 plan 查询路径无需改动
--     (W2-9 才把 PlanLimits 改 DB 读)
--   * Stripe webhook (W2-7) 写完 subscriptions 后再 SetUserPlan, 维持
--     denorm 同步; backfill (W2-8) 一次性把存量 Stripe 用户落到 subscriptions
--   * 跨 schema FK 不用 — billing.subscriptions.user_id 不 FK 到
--     identity.users (服务边界), 用 uuid 即可
-- ═══════════════════════════════════════════════════════════════════

CREATE SCHEMA IF NOT EXISTS billing;

-- ─── 1. plans (套餐字典) ─────────────────────────────────
-- 一行一档. code 是业务键, 与现有 identity.billing.Plan 字面量对齐.
-- benefits 是 jsonb, 内部结构对应 PlanLimits struct (见 W2-3 反序列化).
CREATE TABLE IF NOT EXISTS billing.plans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code            text UNIQUE NOT NULL CHECK (code IN ('free', 'pro', 'team', 'enterprise')),
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    sort_order      int NOT NULL DEFAULT 0,

    -- 价格 (主存原币种, 与 model_relay.pricing 同口径)
    price_currency  text NOT NULL DEFAULT 'USD' CHECK (price_currency IN ('USD', 'CNY')),
    price_monthly   numeric(14, 2) NOT NULL DEFAULT 0,
    price_yearly    numeric(14, 2) NOT NULL DEFAULT 0,

    -- 月度积分配额 (W4 月度结算用; W2 仅落字段)
    monthly_credits bigint NOT NULL DEFAULT 0,

    -- benefits jsonb: {"hub_rpm": 60, "hub_tpm": 50000, "sandbox_daily":10, ...}
    -- 字段集对齐 services/identity/internal/billing/billing.go PlanLimits struct.
    benefits        jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Stripe 关联 (国内 IAP 走 W6 不在本表)
    stripe_price_monthly text,
    stripe_price_yearly  text,

    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS plans_status_sort ON billing.plans (status, sort_order);

-- ─── 2. subscriptions (订阅状态机) ─────────────────────
-- 一行一订阅 (一个用户在某时刻只有一条 active 订阅 — 升降级是 update
-- 此行而不是新建). status 状态机:
--   trialing → active            (试用期结束转付费)
--   active   → canceled          (用户取消, 但仍服务到周期末)
--   active   → past_due          (扣款失败, retry 中)
--   past_due → active / canceled (恢复扣款 / 永久失败)
--   canceled → expired           (周期结束, 转 free)
--   active   → active            (升降级 plan_id 变化)
--
-- canceled 与 expired 的区别: canceled 是用户操作 (current_period_end 仍
-- 有服务), expired 是系统标记 (服务已停, 不再续费).
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               uuid NOT NULL,                          -- 不 FK 到 identity.users
    plan_id               uuid NOT NULL REFERENCES billing.plans(id),

    status                text NOT NULL DEFAULT 'trialing'
                              CHECK (status IN ('trialing', 'active', 'past_due', 'canceled', 'expired')),

    -- 周期
    current_period_start  timestamptz NOT NULL,
    current_period_end    timestamptz NOT NULL,
    trial_end_at          timestamptz,                            -- NULL = 无试用期
    cancel_at             timestamptz,                            -- 用户预约取消时间; 一般 = current_period_end
    canceled_at           timestamptz,                            -- 实际取消时间 (操作发生时)
    expired_at            timestamptz,                            -- 服务停止时间

    -- 计费 (主存原币种)
    billing_cycle         text NOT NULL DEFAULT 'monthly'
                              CHECK (billing_cycle IN ('monthly', 'yearly', 'lifetime')),

    -- Stripe 关联
    stripe_customer_id    text,
    stripe_subscription_id text UNIQUE,                           -- 跨 webhook 唯一键

    -- 审计字段
    metadata              jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- 一个 user 一个 active 订阅 (其余 status 历史保留)
CREATE UNIQUE INDEX IF NOT EXISTS subscriptions_user_active
    ON billing.subscriptions (user_id)
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE INDEX IF NOT EXISTS subscriptions_user_history ON billing.subscriptions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscriptions_status      ON billing.subscriptions (status, current_period_end);
CREATE INDEX IF NOT EXISTS subscriptions_stripe_cust ON billing.subscriptions (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;

-- ─── 3. subscription_events (审计流) ───────────────────
-- 不可变, append-only. 每次状态变更 + 升降级 + 续费 + 退款都进一行.
-- W3 起 fan-out 到 NATS 给 ClickHouse, W2 仅落库.
CREATE TABLE IF NOT EXISTS billing.subscription_events (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id   uuid NOT NULL REFERENCES billing.subscriptions(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,                              -- denorm 方便按 user 查

    event_type        text NOT NULL,                              -- 'created' | 'activated' | 'renewed' | 'upgraded' | 'downgraded' | 'canceled' | 'expired' | 'past_due' | 'recovered' | 'payment_succeeded' | 'payment_failed' | 'refunded'

    -- 变更前后状态 (event_type='upgraded' / 'downgraded' 时 from/to 必填)
    from_plan_id      uuid REFERENCES billing.plans(id),
    to_plan_id        uuid REFERENCES billing.plans(id),
    from_status       text,
    to_status         text,

    -- Stripe 事件溯源
    stripe_event_id   text UNIQUE,                                -- 防 webhook 重复 (Stripe 可能多次投递)

    -- 自由 metadata (Stripe object snapshot / 操作人 / 备注)
    metadata          jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS subscription_events_user      ON billing.subscription_events (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscription_events_subscription ON billing.subscription_events (subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscription_events_type      ON billing.subscription_events (event_type, created_at DESC);

-- ─── 4. payment_orders (支付订单) ─────────────────────
-- 一行一笔支付意图. Stripe / WeChat Pay / Alipay / IAP 共表.
-- amount 主存原币种 (与 plans.price_currency 一致).
CREATE TABLE IF NOT EXISTS billing.payment_orders (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL,
    subscription_id     uuid REFERENCES billing.subscriptions(id),  -- 续费/升降级时关联; 单次充值 NULL

    order_type          text NOT NULL CHECK (order_type IN ('subscription', 'one_time', 'topup', 'refund')),
    provider            text NOT NULL CHECK (provider IN ('stripe', 'wechat_pay', 'alipay', 'apple_iap', 'google_play')),

    -- 金额
    amount              numeric(14, 2) NOT NULL CHECK (amount >= 0),
    currency            text NOT NULL CHECK (currency IN ('USD', 'CNY')),

    -- 状态
    status              text NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'succeeded', 'failed', 'refunded', 'canceled')),

    -- Provider 唯一键 (防重)
    provider_order_id   text NOT NULL,                           -- Stripe payment_intent / 微信 transaction_id / 苹果 transaction_id
    provider_event_id   text,                                    -- webhook event id 防回放

    -- 失败/退款详情
    failure_code        text,
    failure_message     text,
    refunded_at         timestamptz,
    refund_amount       numeric(14, 2) NOT NULL DEFAULT 0 CHECK (refund_amount >= 0),

    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at          timestamptz NOT NULL DEFAULT now(),
    paid_at             timestamptz,                             -- status='succeeded' 时填
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS payment_orders_provider_uniq
    ON billing.payment_orders (provider, provider_order_id);

CREATE INDEX IF NOT EXISTS payment_orders_user        ON billing.payment_orders (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS payment_orders_subscription ON billing.payment_orders (subscription_id) WHERE subscription_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS payment_orders_status      ON billing.payment_orders (status, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS billing.payment_orders;
DROP TABLE IF EXISTS billing.subscription_events;
DROP TABLE IF EXISTS billing.subscriptions;
DROP TABLE IF EXISTS billing.plans;

DROP SCHEMA IF EXISTS billing;

-- +goose StatementEnd
