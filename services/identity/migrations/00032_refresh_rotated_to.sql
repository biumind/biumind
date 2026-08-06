-- +goose Up
-- +goose StatementBegin

-- ─── refresh_tokens.rotated_to / rotated_token_enc ────────────────────
-- refresh token rotation grace window (Auth0 Reuse Interval / Okta 30s grace
-- 同款机制): rotate 时把新行 id + 新 refresh_token 的 AES-256-GCM 密文回写到
-- 被 revoke 的老行上, 形成 A → B → C 的 rotation 链。
--
-- 宽限窗口 (IDENTITY_REFRESH_REUSE_GRACE, 默认 10s) 内重放老 token 时
-- (客户端丢响应重试 / app 被杀 / 并发刷新), handleRefresh 沿 rotated_to 链
-- 走到 revoked_at IS NULL 的 head, 解密出 head token 明文重新签发 access
-- 返回 200 — 不触发 reuse detection, 不整族撤销, 不写 security_events。
--
-- rotated_to IS NULL 的 revoked 行 = 非 rotate 撤销 (logout / 踢设备 /
-- reuse detection / 改密), 链断 → grace replay 不命中, 维持原 reuse 语义。
-- 老行 (本 migration 之前 revoke 的) 同样 NULL, 行为不变。

ALTER TABLE identity.refresh_tokens
    ADD COLUMN IF NOT EXISTS rotated_to uuid NULL,
    ADD COLUMN IF NOT EXISTS rotated_token_enc bytea NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE identity.refresh_tokens
    DROP COLUMN IF EXISTS rotated_to,
    DROP COLUMN IF EXISTS rotated_token_enc;

-- +goose StatementEnd
