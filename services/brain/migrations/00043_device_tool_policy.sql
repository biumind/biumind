-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R6.3 / D7：per-device 工具权限 + daemon 纵深防御。
-- agent_devices 上挂每台设备可远程调用的工具范围（preset），brain 在 createSession
-- 时映射 device→policy stamp 进 work payload；daemon 取它与本地 --tool-policy 的
-- 交集做能力地板。链路：device token 注册 environment 时把 device_id 落到
-- agent_environments，session 经 environment 找回 device 的 tool_policy。

-- 每设备工具权限 preset（已拍板）：readonly / workspace-write / full。
-- 默认 workspace-write（文件改限定在 allowed-roots，shell 须显式 full）。
ALTER TABLE agent_devices
    ADD COLUMN IF NOT EXISTS tool_policy TEXT NOT NULL DEFAULT 'workspace-write';

ALTER TABLE agent_devices
    ADD CONSTRAINT agent_devices_tool_policy_chk
    CHECK (tool_policy IN ('readonly', 'workspace-write', 'full'));

-- environment 反向关联签发它的 device（device token 注册时 stamp）。nullable：
-- JWT / PAT 注册（非 device token）以及 runtime pool environment 无 device。
ALTER TABLE agent_environments
    ADD COLUMN IF NOT EXISTS device_id UUID;

-- session 创建按 environment→device 查 policy 的辅助索引。
CREATE INDEX IF NOT EXISTS agent_environments_device_idx
    ON agent_environments (device_id) WHERE device_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS agent_environments_device_idx;
ALTER TABLE agent_environments DROP COLUMN IF EXISTS device_id;
ALTER TABLE agent_devices DROP CONSTRAINT IF EXISTS agent_devices_tool_policy_chk;
ALTER TABLE agent_devices DROP COLUMN IF EXISTS tool_policy;
-- +goose StatementEnd
