-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W5-8 — 试用资格防刷三元组黑名单
--
-- 每次试用申请落一行 (succeeded=true 表示真起了订阅; false 表示 reject).
-- 后续申请按 (user_id / device_fp / ip) 三轴查 24h 历史决定是否给试用.
--
-- 规则 (默认值, 可在 internal/billing/trial.go 调):
--   1. 同 user_id 历史已 succeeded=true → 拒 (一辈子只能一次)
--   2. 同 device_fp 已被 ≥ 3 个不同 user_id succeeded → 拒
--   3. 同 ip 24h 内 ≥ 5 次申请 (任何 succeeded) → 拒
--
-- 三元组任意一项缺失 (老客户端没传) 不参与判定, 但已知项仍生效.
-- ═══════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS billing.trial_attempts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    device_fp       text NOT NULL DEFAULT '',
    ip              inet,
    succeeded       boolean NOT NULL DEFAULT false,
    reject_reason   text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS trial_attempts_user      ON billing.trial_attempts (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS trial_attempts_device    ON billing.trial_attempts (device_fp, created_at DESC) WHERE device_fp <> '';
CREATE INDEX IF NOT EXISTS trial_attempts_ip        ON billing.trial_attempts (ip, created_at DESC) WHERE ip IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS billing.trial_attempts;

-- +goose StatementEnd
