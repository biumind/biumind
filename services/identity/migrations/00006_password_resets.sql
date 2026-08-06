-- +goose Up
-- +goose StatementBegin

-- 密码重置 — 跟 email_verifications 同形 (6 位 code, sha256 hash, 5 次错码作废,
-- TTL 600 秒). 单独建表是因为:
--   - 不应跟邮箱验证 code 混用 (一个 code 不能既验邮箱又改密码)
--   - 业务流程独立 — 已验证的老用户也能走忘记密码

CREATE TABLE IF NOT EXISTS identity.password_resets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    code_hash   bytea NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    attempts    int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS password_resets_user_idx
    ON identity.password_resets(user_id);

CREATE INDEX IF NOT EXISTS password_resets_expires_idx
    ON identity.password_resets(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.password_resets;
-- +goose StatementEnd
