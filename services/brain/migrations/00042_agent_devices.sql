-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R6.1 / D5：设备配对 + 可吊销 device token。
-- 解决痛点：daemon 此前持完整用户 PAT（泄漏即冒充全账号、无法单独吊销某台
-- 设备）。改为：Mac daemon 用配对码换一个 scoped + 可吊销的 device token。

-- agent_pairings：pending 配对（daemon request → 用户 approve → daemon poll 取 token）。
-- 短命（TTL 5min），janitor sweep 清理。
CREATE TABLE IF NOT EXISTS agent_pairings (
    pairing_id          UUID PRIMARY KEY,
    code                TEXT NOT NULL,              -- 8 位数字配对码（用户在已登录设备输入）
    pairing_secret_hash BYTEA NOT NULL,             -- SHA256(pairing_secret)；poll 时校验 daemon 身份
    machine_name        TEXT NOT NULL,
    os_arch             TEXT,
    worker_kind         TEXT,
    user_id             UUID,                       -- approve 时填（绑定到批准的用户）
    status              TEXT NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    approved_at         TIMESTAMPTZ,

    CONSTRAINT agent_pairings_status_chk CHECK (status IN ('pending', 'approved', 'consumed'))
);

-- 按 code 查 pending 配对（approve 用）。
CREATE INDEX IF NOT EXISTS agent_pairings_code_idx
    ON agent_pairings (code) WHERE status = 'pending';
-- janitor 按 expires_at 清。
CREATE INDEX IF NOT EXISTS agent_pairings_expiry_idx ON agent_pairings (expires_at);

-- agent_devices：已签发的 device token（opaque，只存 hash，可吊销）。
CREATE TABLE IF NOT EXISTS agent_devices (
    device_id    UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    name         TEXT NOT NULL,                     -- 设备名（= machine_name）
    token_hash   BYTEA NOT NULL UNIQUE,             -- SHA256(full token)；鉴权时按此查
    prefix       TEXT NOT NULL,                     -- token 前缀（展示/排查用，非密）
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ
);

-- 鉴权热路径：按 token_hash 查（UNIQUE 已建索引）。列用户设备：
CREATE INDEX IF NOT EXISTS agent_devices_user_idx ON agent_devices (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_devices;
DROP TABLE IF EXISTS agent_pairings;
-- +goose StatementEnd
