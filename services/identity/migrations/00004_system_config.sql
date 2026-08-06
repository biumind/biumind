-- +goose Up
-- +goose StatementBegin

-- system_config — 后台运维配置 KV 存储, superadmin 在 admin UI 改.
-- value 用 JSONB 保留结构 (邮件配置是嵌套对象, 简单 KV 不够).
-- secret 字段标识哪些 value 含敏感数据 (SMTP password / Stripe key 等),
-- API 返回时按 RBAC 决定脱敏.
--
-- 当前 commit 用例:
--   key='alert.email'   → {"smtp_host", "smtp_user", "smtp_pass", "from", "to"}
--
-- 后续可加: key='stripe.config' / key='feature_flags' / ...

CREATE SCHEMA IF NOT EXISTS biumind_system;

CREATE TABLE IF NOT EXISTS biumind_system.config (
    key         text PRIMARY KEY,
    value       jsonb NOT NULL,
    secret      boolean NOT NULL DEFAULT false,
    description text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  uuid REFERENCES identity.users(id) ON DELETE SET NULL
);

-- 内置 key 占位 (UI 不显示, 但 superadmin 可填)
INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  ('alert.email', '{"enabled":false,"smtp_host":"","smtp_port":465,"smtp_user":"","smtp_pass":"","smtp_tls":true,"from":"","to":[]}',
   true, 'Alertmanager webhook 通过这里发邮件 (SMTP 配置)')
ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS biumind_system.config;
DROP SCHEMA IF EXISTS biumind_system CASCADE;
-- +goose StatementEnd
