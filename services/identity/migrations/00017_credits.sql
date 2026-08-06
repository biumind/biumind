-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- credits subsystem (P1 — 双账户积分系统 baseline)
--
-- 引入"积分"作为 biumind 第一公民:
--   permanent  —— 单充买入 / 永不过期
--   time_limited —— 套餐附赠 / 活动赠送, 按 expires_at 过期
--
-- 扣减策略: 优先扣最早过期的时效包 → 扣完跳下一个 → 最后扣永久.
-- 退款策略: 按 credit_logs.consume_breakdown_json 找回原扣减明细, 原路径返还到 packages.
--
-- 设计来源:
--   docs/BiuMind-AIGC-Storage-Design.md §2.4
--   docs/BiuMind-AIGC-Migration-Plan.md §2.4
--   packages/proto/biumind/credits/v1/credits.proto
--   zhiying-backend/新用户积分设计文档.md (行为基线)
--
-- 跨 schema FK 不使用: credit_packages.user_id 不 FK 到 users(id).
-- ═══════════════════════════════════════════════════════════════════

-- ─── 余额 (聚合视图, UI 显示用) ───────────────────────────

-- 注意: user_credits 是衍生汇总, 真实账本以 credit_packages 为准.
-- 写路径: 每次 Consume / Refund / Grant 后通过事务更新该行 (避免每次查询都做聚合).
-- 校验: 后台 reconcile job 周期对账 user_credits vs SUM(credit_packages.remaining).
CREATE TABLE identity.user_credits (
    user_id                       uuid PRIMARY KEY,
    permanent_balance             bigint NOT NULL DEFAULT 0
                                  CHECK (permanent_balance >= 0),
    time_limited_balance          bigint NOT NULL DEFAULT 0
                                  CHECK (time_limited_balance >= 0),
    -- 最早过期的时效包过期时间 (UI 显示「时效积分将于 X 月 Y 日过期」)
    time_limited_earliest_expires timestamptz,
    updated_at                    timestamptz DEFAULT now()
);

-- ─── 积分包 (按 expires_at 升序扣减; 同 user 可多个时效包并存) ─

-- 'permanent' 包: expires_at 为 NULL, 优先级最低.
-- 'time_limited' 包: expires_at NOT NULL, 优先级按 expires_at 升序 (越早过期越先扣).
-- remaining: 当前剩余可扣数. 0 时该包视为耗尽 (但保留行做审计, 不删).
CREATE TABLE identity.credit_packages (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('permanent','time_limited')),
    source         text NOT NULL CHECK (source IN
                   ('recharge','plan_grant','reward','refund','admin')),
    initial_amount bigint NOT NULL CHECK (initial_amount >= 0),
    remaining      bigint NOT NULL CHECK (remaining >= 0),
    expires_at     timestamptz,
    metadata       jsonb DEFAULT '{}'::jsonb,
    created_at     timestamptz DEFAULT now(),

    -- 不变量: time_limited 必须有 expires_at; permanent 必须无.
    CONSTRAINT credit_packages_kind_expires_consistent CHECK (
        (kind = 'time_limited' AND expires_at IS NOT NULL)
        OR (kind = 'permanent' AND expires_at IS NULL)
    )
);
-- 扣减时按这个索引扫: kind=time_limited AND remaining>0 ORDER BY expires_at, created_at.
CREATE INDEX identity_credit_packages_consumption_order
    ON identity.credit_packages (user_id, kind, expires_at NULLS LAST, created_at)
    WHERE remaining > 0;
CREATE INDEX identity_credit_packages_user_kind
    ON identity.credit_packages (user_id, kind);

-- ─── 流水 (每笔出入账都记一行, 退款回溯靠它) ────────────

-- delta > 0: 入账 (recharge/plan_grant/reward/refund-入); delta < 0: 出账 (consume).
-- consume_breakdown_json: 出账时记录扣了哪些 package, 各扣多少, 用于退款时原路径返还.
--   形如: [{"package_id":"...","amount":40}, {"package_id":"...","amount":10}]
-- refund_of_log_id: 退款专用, 指向原扣减 log; 用于幂等 (同一原 log 同 idempotency_key 退款只生效一次).
-- idempotency_key: 调用方提供 (推荐 task_id / order_id), 同 (user_id, idempotency_key) 重复请求只生效一次.
CREATE TABLE identity.credit_logs (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  uuid NOT NULL,
    delta                    bigint NOT NULL,
    consume_breakdown_json   jsonb,
    balance_after            bigint NOT NULL,

    ref_type                 text NOT NULL CHECK (ref_type IN
                             ('aigc_task','chat_message','recharge','plan_grant','refund','reward','admin')),
    ref_id                   text,
    remark                   text,

    refund_of_log_id         uuid REFERENCES identity.credit_logs(id),
    idempotency_key          text,

    created_at               timestamptz DEFAULT now()
);
CREATE INDEX identity_credit_logs_user_created
    ON identity.credit_logs (user_id, created_at DESC);
CREATE INDEX identity_credit_logs_ref
    ON identity.credit_logs (ref_type, ref_id);
-- 幂等键唯一约束: 同 (user_id, idempotency_key) 只允许一条 (Consume / Recharge / Refund 共用此约束).
CREATE UNIQUE INDEX identity_credit_logs_idempotency
    ON identity.credit_logs (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
-- 退款幂等: 同 refund_of_log_id 同 idempotency_key 只允许一条退款.
CREATE INDEX identity_credit_logs_refund_of
    ON identity.credit_logs (refund_of_log_id)
    WHERE refund_of_log_id IS NOT NULL;

-- ─── 充值套餐选项 (运营配置, v1 mock) ─────────────────────

CREATE TABLE identity.credit_recharge_options (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name     text NOT NULL,
    credits_amount   bigint NOT NULL CHECK (credits_amount > 0),
    kind             text NOT NULL CHECK (kind IN ('permanent','time_limited')),
    price_micro_cny  bigint NOT NULL CHECK (price_micro_cny >= 0),
    valid_days       integer DEFAULT 0 CHECK (valid_days >= 0),
    enabled          boolean DEFAULT true,
    sort_order       integer DEFAULT 0,
    created_at       timestamptz DEFAULT now()
);
CREATE INDEX identity_credit_recharge_options_enabled
    ON identity.credit_recharge_options (enabled, sort_order);

-- ─── 用户存储配额 (跟 AIGC 文件存储绑定) ────────────────

-- 由 services/aigc 在转存 / 删除作品时调 identity 接口维护.
-- bytes_quota: free 5GB / pro 100GB / team 1TB (按 plan 计算, 由 billing 同步).
CREATE TABLE identity.user_storage (
    user_id      uuid PRIMARY KEY,
    bytes_used   bigint NOT NULL DEFAULT 0 CHECK (bytes_used >= 0),
    bytes_quota  bigint NOT NULL,
    file_count   integer NOT NULL DEFAULT 0,
    updated_at   timestamptz DEFAULT now()
);

-- ─── 默认充值套餐 seed (mock 充值用) ──────────────────────

INSERT INTO identity.credit_recharge_options
    (display_name, credits_amount, kind, price_micro_cny, valid_days, sort_order)
VALUES
    ('100 积分体验包',    100,  'permanent',    9900000,   0,  10),  -- ¥9.9
    ('500 积分基础包',    500,  'permanent',   39900000,   0,  20),  -- ¥39.9
    ('1500 积分超值包',  1500,  'permanent',   99900000,   0,  30),  -- ¥99.9
    ('5000 积分专业包',  5000,  'permanent',  299900000,   0,  40),  -- ¥299.9
    ('限时 1000 积分时效包', 1000, 'time_limited', 49900000, 30, 50); -- ¥49.9, 30 天

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS identity.user_storage;
DROP TABLE IF EXISTS identity.credit_recharge_options;
DROP TABLE IF EXISTS identity.credit_logs;
DROP TABLE IF EXISTS identity.credit_packages;
DROP TABLE IF EXISTS identity.user_credits;

-- +goose StatementEnd
