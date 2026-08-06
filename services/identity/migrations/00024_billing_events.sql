-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W3-4 — billing.events 大宽表 (PG MVP)
--
-- 上游: identity 内 sink goroutine (W3-5) 订阅 NATS 'BIUMIND_BILLING_EVENTS'
-- stream, 批量 (1000/批 或 5s flush) INSERT 到本表.
--
-- 设计取舍:
-- 1. 单表 + jsonb 兼存 6 类 event (consume / refund / hold / settle /
--    release / subscription); 不分 6 张表, 否则 dashboard 统计要 UNION ALL
--    所有表, 写起来痛苦.
-- 2. event_id PK 而非 (user_id, occurred_at) — sink 重投递时 ON CONFLICT
--    DO NOTHING 干净去重 (NATS at-least-once 保证).
-- 3. 月分区: identity.billing.events_yyyymm. 老分区到期 detach/drop 不影
--    响主表; PG declarative partitioning 简单够用.
-- 4. 不上 CH 的理由: dev/早期生产 events < 1k/min, PG 单表查询 < 1s. 量
--    级到 5000w/月 时再切, 详见 dev plan §11.5 O-1.
-- 5. 索引: 仅建必要的 (user_id+occurred_at) + (kind, occurred_at) 两个;
--    每个索引在每分区上各占 ~table_size/(分区数*5) 体积, 多了拖慢 INSERT.
-- ═══════════════════════════════════════════════════════════════════

CREATE TABLE billing.events (
    -- 来自 publisher Common 头
    event_id        UUID NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN (
                        'consume', 'refund', 'hold', 'settle',
                        'release', 'subscription'
                    )),
    user_id         UUID NOT NULL,
    idempotency_key TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL,
    env             TEXT NOT NULL,

    -- 跨 kind 公共维度 (可空, 视 kind 决定哪些非空)
    log_id          UUID,           -- consume/refund/settle 引用的 credit_logs
    hold_id         UUID,           -- hold/settle/release 引用的 credit_holds
    amount          BIGINT,         -- 积分量 (正数, 单位见 ref_type)
    ref_type        TEXT,           -- chat_message / agent_step / aigc_task / refund / ...
    ref_id          TEXT,
    model_code      TEXT,           -- 模型分布 dashboard 用
    provider_code   TEXT,
    upstream_usd    DOUBLE PRECISION,  -- 毛利计算 (consume 时填)
    upstream_cny    DOUBLE PRECISION,

    -- subscription 专用
    subscription_id UUID,
    event_type      TEXT,           -- created / activated / upgraded / downgraded / canceled / ...
    plan_code       TEXT,
    old_plan_code   TEXT,
    amount_cents    BIGINT,
    currency        TEXT,
    source          TEXT,           -- stripe / wechat / alipay / iap

    -- settle/release 专用
    actual          BIGINT,         -- settle 实扣
    hold_delta      BIGINT,         -- settle 多预扣 (退还给余额)
    refund_of_log_id UUID,          -- refund 引用原 log
    expires_at      TIMESTAMPTZ,    -- hold 的预期过期
    reason          TEXT,           -- release: user_cancel / expired

    -- 原始 payload (调试 + 未来字段扩展前向兼容)
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,

    inserted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, occurred_at)
)
PARTITION BY RANGE (occurred_at);

-- 起始分区: 2026-06 / 2026-07 / 2026-08 (覆盖 W3 期间 + 缓冲).
-- 月度自动新分区由 cron job (W3-后续) 提前 2 个月创建; 初期手动维护.
CREATE TABLE billing.events_202606
    PARTITION OF billing.events
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE billing.events_202607
    PARTITION OF billing.events
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE billing.events_202608
    PARTITION OF billing.events
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');

-- 索引建在每个分区上 (PG 14+ 支持父表声明索引自动下传到分区).
CREATE INDEX events_user_time_idx
    ON billing.events (user_id, occurred_at DESC);
CREATE INDEX events_kind_time_idx
    ON billing.events (kind, occurred_at DESC);
-- model 维度 dashboard 高频查 — 给 consume + 模型代码做局部索引.
CREATE INDEX events_model_time_idx
    ON billing.events (model_code, occurred_at DESC)
    WHERE kind = 'consume';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS billing.events_202608;
DROP TABLE IF EXISTS billing.events_202607;
DROP TABLE IF EXISTS billing.events_202606;
DROP TABLE IF EXISTS billing.events;
-- +goose StatementEnd
