-- +goose Up
-- +goose StatementBegin

-- email 验证. 注册后用户处于"已建账户但未验证"状态:
--   - users.email_verified_at IS NULL → 不允许 login
--   - email_verifications 存待校验的 6 位 code (sha256 hash, 不存明文)
--
-- 现有账户视为已验证 — 用 created_at 兜底, 避免对存量用户造成"被强制验证"
-- 的中断. 之后注册的新用户走完整流程.

ALTER TABLE identity.users
    ADD COLUMN IF NOT EXISTS email_verified_at timestamptz;

UPDATE identity.users
   SET email_verified_at = created_at
 WHERE email_verified_at IS NULL;

CREATE TABLE IF NOT EXISTS identity.email_verifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    code_hash   bytea NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    attempts    int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 拿"该用户最新未消费 code" 是热路径 (verify + resend 都用) — 给 user_id 上索引
CREATE INDEX IF NOT EXISTS email_verifications_user_idx
    ON identity.email_verifications(user_id);

-- 后台清理已过期未消费的 code 时按 expires_at 排序
CREATE INDEX IF NOT EXISTS email_verifications_expires_idx
    ON identity.email_verifications(expires_at);

-- 邮件验证 SMTP + 模板配置占位 (superadmin 在后台 UI 填). 不存在时
-- 注册照常但 code 写日志 (dev 兜底), 不影响测试.
INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  ('auth.email',
   '{"enabled":false,"smtp_host":"","smtp_port":465,"smtp_user":"","smtp_pass":"","smtp_tls":true,"from":"","code_ttl_seconds":600,"subject":"[BiuMind] 邮箱验证码"}',
   true,
   '注册邮箱验证 (SMTP). enabled=false 时 code 仅写入日志 (dev 兜底).')
ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.email_verifications;
ALTER TABLE identity.users DROP COLUMN IF EXISTS email_verified_at;
DELETE FROM biumind_system.config WHERE key = 'auth.email';
-- +goose StatementEnd
