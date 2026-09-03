-- ============================================================================
-- 00020_user_settings.sql — per-user 通用设置 KV 表（B2 ingest 模型偏好）
--
-- identity.users 无 preferences 列（00001:108-130），且未来设置项会增长
-- （ingest 模型、创作默认参数 …），故建通用 KV 表而不是加列：
--   key = 设置名（如 'ingest_model'），value = jsonb 负载
--   （如 {"model":"claude-sonnet-4-6"}）。
-- ON DELETE CASCADE 随用户删除清理；PRIMARY KEY (user_id, key) 天然去重。
--
-- 读方：用户侧 GET/PUT /v1/identity/me/settings/ingest-model；
--       worker 侧 GET /v1/internal/settings/{user_id}/ingest-model（404=未设置）。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS identity.user_settings (
    user_id    uuid        NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    key        text        NOT NULL,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS identity.user_settings;
-- +goose StatementEnd
