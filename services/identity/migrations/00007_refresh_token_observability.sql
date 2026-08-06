-- +goose Up
-- +goose StatementBegin

-- 给 refresh_token 加可观察性字段, 让"已登录设备"列表能展示有意义的信息.
--   last_used_at: 每次 refresh 时刷新, UI 展示"最近活跃"
--   last_ip:      refresh 时记录, "在哪登录的"
--   last_ua:      refresh 时记录, FE 解析出浏览器 / 设备类型
--
-- 老 row 三个字段都 NULL — 用户首次 refresh 后自动写入, UI 兜底显示"未知".

ALTER TABLE identity.refresh_tokens
    ADD COLUMN IF NOT EXISTS last_used_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_ip      inet,
    ADD COLUMN IF NOT EXISTS last_ua      text;

-- "列出该用户 active session" 是热路径 (Settings 页打开就调). 索引按
-- (user_id, revoked_at IS NULL) 已被 refresh_tokens_user_idx 覆盖, 不重建.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE identity.refresh_tokens
    DROP COLUMN IF EXISTS last_used_at,
    DROP COLUMN IF EXISTS last_ip,
    DROP COLUMN IF EXISTS last_ua;
-- +goose StatementEnd
