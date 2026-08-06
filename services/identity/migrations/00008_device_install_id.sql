-- +goose Up
-- +goose StatementBegin

-- "授权的是设备, 不是 token" — 用 installation_id (客户端首次启动生成的
-- 永久 UUID) 标识设备. 同 (user, install) 反复登入登出复用同一行
-- refresh_token (token_hash 原地 rotate, 行 id 不变); 撤销则永久失效,
-- 即便客户端再用同 installation_id 登录也会建新行.
--
-- partial unique index 只约束 "active 且 install_id 非空" 的行:
--   - 给 ON CONFLICT 落地点
--   - revoked 行不参与, 历史保留
--   - 老 installation_id='' 的行不参与, 不打架, graceful degradation

ALTER TABLE identity.refresh_tokens
    ADD COLUMN IF NOT EXISTS installation_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS refresh_tokens_active_device_idx
    ON identity.refresh_tokens(user_id, installation_id)
    WHERE revoked_at IS NULL AND installation_id <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS identity.refresh_tokens_active_device_idx;
ALTER TABLE identity.refresh_tokens DROP COLUMN IF EXISTS installation_id;
-- +goose StatementEnd
