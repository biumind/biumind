-- +goose Up
-- +goose StatementBegin

-- ─── refresh_tokens.absolute_expires_at ──────────────────────────────
-- 三层过期策略 (BiuMind-Identity-Session-Design §3.1) 的 "absolute cap":
--
--   - sliding expires_at:    每次 rotation 续 RefreshTTL (90d 默认), 让活
--                            跃用户永不掉线
--   - absolute_expires_at:   首次签发时定死 (created_at + 1y), rotation
--                            **不重置**, 防止永久泄漏的 token 被无限续期
--
-- 老行 backfill: 给 created_at + 1y, 最坏情况老用户最多用满 1 年绝对期。
-- 这避免了"强制所有人立刻重登"的不友好行为。

ALTER TABLE identity.refresh_tokens
    ADD COLUMN IF NOT EXISTS absolute_expires_at timestamptz;

UPDATE identity.refresh_tokens
   SET absolute_expires_at = created_at + INTERVAL '1 year'
 WHERE absolute_expires_at IS NULL;

ALTER TABLE identity.refresh_tokens
    ALTER COLUMN absolute_expires_at SET NOT NULL;

-- ─── security_events ────────────────────────────────────────────────
-- 安全事件审计表。第一类 kind = 'refresh_token_reuse' 由 A3 reuse
-- detection 写入: 当一个已 revoked 的 refresh_token 被再次提交, 说明
-- token 链路有泄漏 (合法客户端 rotate 后老 token 不会再被合法使用)。
--
-- detail 用 jsonb 留扩展空间; 当前写入的字段:
--   { "session_id": "uuid", "installation_id": "...", "family_revoked": N }
--
-- 索引: (user_id, created_at DESC) 给"我的安全活动"页用; 单用户读热路径。

CREATE TABLE IF NOT EXISTS identity.security_events (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    kind        text        NOT NULL,
    detail      jsonb,
    ip          inet,
    ua          text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS security_events_user_idx
    ON identity.security_events(user_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS identity.security_events_user_idx;
DROP TABLE IF EXISTS identity.security_events;

ALTER TABLE identity.refresh_tokens
    DROP COLUMN IF EXISTS absolute_expires_at;

-- +goose StatementEnd
