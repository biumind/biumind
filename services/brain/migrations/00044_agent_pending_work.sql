-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R7：agent 离线任务排队。给当前离线的设备发 agent 任务，设备上线
-- 后自动派发。work 按 env_id 路由、env_id 每次重启 churn + 7天 GC，故不能靠
-- JetStream 暂存——改为按稳定的 device_id 持久化请求参数，设备重连(handleRegister)
-- 时重建 WorkPayload(BYOK 派发时重解析、不落库)重新 enqueue。

-- session 多一个 pending 态：已创建、work 排队等设备上线。
ALTER TABLE agent_sessions DROP CONSTRAINT IF EXISTS agent_sessions_state_chk;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_state_chk
    CHECK (state IN ('active', 'paused', 'completed', 'failed', 'pending'));

-- agent_pending_work：排队的 agent work。只存重派所需请求参数，**不含 BYOK
-- 凭证**（派发时按 provider_id 重解析）。
CREATE TABLE IF NOT EXISTS agent_pending_work (
    pending_id       UUID PRIMARY KEY,
    session_id       UUID NOT NULL UNIQUE REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    user_id          UUID NOT NULL,
    device_id        UUID NOT NULL,         -- 重派锚点：设备重连按此匹配
    prompt           TEXT,
    model            TEXT,
    provider_id      TEXT,
    system_prompt    TEXT,
    thread_id        UUID,
    workdir          TEXT,
    runtime_env_mode TEXT,
    backend          TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL
);

-- 设备重连 sweep：按 device_id 取该设备的挂起 work。
CREATE INDEX IF NOT EXISTS agent_pending_work_device_idx ON agent_pending_work (device_id);
-- janitor 过期清理。
CREATE INDEX IF NOT EXISTS agent_pending_work_expiry_idx ON agent_pending_work (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_pending_work;
ALTER TABLE agent_sessions DROP CONSTRAINT IF EXISTS agent_sessions_state_chk;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_state_chk
    CHECK (state IN ('active', 'paused', 'completed', 'failed'));
-- +goose StatementEnd
