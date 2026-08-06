-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- credit_holds (R1 — 流式预扣 / 结算)
--
-- 适用 chat / agent 等流式响应: 提交时 Hold 预扣 max_amount, 流结束后
-- Settle 真实金额 (≤ max), 失败 / 取消则 Release. 5min TTL 兜底防 hold
-- leak — Reaper goroutine 周期扫 expires_at < now() AND status='held'
-- 自动 release 对应预占的 packages.
--
-- hold_breakdown_json: 持有时按优先级 (plan_quota → 时效 → 永久) 预占了
-- 哪些 packages, settle 时按相同顺序结算. 形如:
--   [{"package_id":"...","amount":40,"kind":"time_limited"},
--    {"package_id":"...","amount":10,"kind":"permanent"}]
--
-- idempotency_key: 与 credit_logs 同语义. 同 (user_id, ref_type, key) 重复
-- Hold 直接返回原 hold_id, 不重复预占.
--
-- 设计来源: docs/BiuMind-Billing-Redesign.md §5.2.
-- ═══════════════════════════════════════════════════════════════════

CREATE TABLE identity.credit_holds (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL,
    ref_type            text NOT NULL CHECK (ref_type IN
                        ('chat_message','agent_step','aigc_task')),
    ref_id              text,
    -- 金额: max 上限, actual 在 settle 后填
    max_amount          bigint NOT NULL CHECK (max_amount > 0),
    actual_amount       bigint CHECK (actual_amount IS NULL OR actual_amount >= 0),
    -- 状态机: held → settled / released / expired (后三者为终态)
    status              text NOT NULL CHECK (status IN
                        ('held','settled','released','expired')),
    -- 拆账明细 (持有时按优先级顺序记录占用了哪些 package)
    hold_breakdown_json jsonb,
    -- 幂等
    idempotency_key     text,
    -- TTL: hold 时 +300s, expired 后由 reaper 释放对应 packages
    expires_at          timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    settled_at          timestamptz,

    -- 不变量: settled 必须有 actual_amount + settled_at
    CONSTRAINT credit_holds_settled_has_actual CHECK (
        (status = 'settled' AND actual_amount IS NOT NULL AND settled_at IS NOT NULL)
        OR (status <> 'settled')
    ),
    -- 不变量: actual ≤ max
    CONSTRAINT credit_holds_actual_le_max CHECK (
        actual_amount IS NULL OR actual_amount <= max_amount
    )
);

-- 用户活跃 hold 集合 (Hold/Settle/余额检查时用)
CREATE INDEX identity_credit_holds_user_active
    ON identity.credit_holds (user_id, status)
    WHERE status = 'held';

-- Reaper 扫: WHERE status='held' AND expires_at < now()
CREATE INDEX identity_credit_holds_expiry
    ON identity.credit_holds (expires_at)
    WHERE status = 'held';

-- 幂等: 同 (user_id, ref_type, idempotency_key) 只允许一条
CREATE UNIQUE INDEX identity_credit_holds_idempotency
    ON identity.credit_holds (user_id, ref_type, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- 业务反查 (调试 / 客服)
CREATE INDEX identity_credit_holds_ref
    ON identity.credit_holds (ref_type, ref_id)
    WHERE ref_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS identity.credit_holds;

-- +goose StatementEnd
