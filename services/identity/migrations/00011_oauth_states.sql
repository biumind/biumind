-- +goose Up
-- +goose StatementBegin

-- oauth_states — H5 OAuth 2.0 授权码流防 CSRF + 跳回原页面.
--
-- 流程:
--   1. 前端点"微信登录" → GET /v1/auth/wechat/h5-authorize?return=/page
--   2. 服务端生成 state + 落 oauth_states (5 min TTL), 302 跳微信授权 URL
--   3. 微信授权后回 /v1/auth/wechat/h5-callback?code=&state=
--   4. 服务端 SELECT oauth_states 校验 state → 取 return_url
--   5. 校验通过即 DELETE 该行 (一次性使用, 防 replay)
--
-- state 是 32 字节 random base64, 不可猜.
-- provider 字段为后续接入支付宝 / GitHub / 等做准备.

CREATE TABLE IF NOT EXISTS identity.oauth_states (
    state        text PRIMARY KEY,
    provider     text NOT NULL,           -- 'wechat_web' / 'alipay_web' / ...
    return_url   text NOT NULL DEFAULT '/',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);

-- expires_at 索引 — GC 清扫过期 state 用 (cron / 启动时).
CREATE INDEX IF NOT EXISTS oauth_states_expires_idx
    ON identity.oauth_states(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS identity.oauth_states_expires_idx;
DROP TABLE IF EXISTS identity.oauth_states;
-- +goose StatementEnd
