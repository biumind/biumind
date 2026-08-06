-- +goose Up
-- +goose StatementBegin

-- identity_providers — 第三方登录身份映射. 一个 user_id 可挂 N 个 provider
-- (微信 / 支付宝 / 抖音 / 百度 / QQ / 快手 / 京东 / 飞书 + email 等).
--
-- 设计要点:
--   1. (provider, provider_user_id) 唯一: 一个外部账号最多绑到一个 BiuMind user
--   2. unionid: 仅微信生态有. 同 unionid 的 wechat_mp / wechat_oa /
--      wechat_open 自动合并到同一 user (登录时查找命中即复用)
--   3. raw_profile_json: 缓存 nickname / avatar_url / 性别等 — 显示给用户
--      自己看, 不参与业务逻辑
--   4. 跨厂商 (微信 ↔ 支付宝) 不能自动合并; 用户在 me 页面手动绑定 (策略 B)
--
-- provider 取值约定 (字符串枚举, 不建 enum 表 — 加新平台只是加 row 不改 schema):
--   wechat_mp / wechat_oa / wechat_open
--   alipay_mp
--   toutiao_mp (抖音小程序)
--   baidu_mp / qq_mp / kuaishou_mp / jd_mp / lark_mp
--   email (本地账号占位; 兼容已有 users.email 流, 不一定写)

-- users.password_hash 已经是 nullable (00001), email 也是 NOT NULL —
-- 第三方登录用户 email 留 placeholder ("wx_<openid>@no-mail.biumind") 即可
-- 不动 users 表 schema, 减少老路径回归风险.

CREATE TABLE IF NOT EXISTS identity.identity_providers (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    provider          text NOT NULL,
    provider_user_id  text NOT NULL,
    unionid           text,
    raw_profile_json  jsonb NOT NULL DEFAULT '{}'::jsonb,
    bound_at          timestamptz NOT NULL DEFAULT now(),
    last_login_at     timestamptz,
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS identity_providers_user_idx
    ON identity.identity_providers(user_id);

-- unionid 查找索引 — 仅在非空时建, 跨 provider 合并查询用:
--   SELECT user_id FROM identity_providers
--   WHERE unionid = $1 AND provider IN ('wechat_mp','wechat_oa','wechat_open')
CREATE INDEX IF NOT EXISTS identity_providers_unionid_idx
    ON identity.identity_providers(unionid)
    WHERE unionid IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS identity.identity_providers_unionid_idx;
DROP INDEX IF EXISTS identity.identity_providers_user_idx;
DROP TABLE IF EXISTS identity.identity_providers;
-- +goose StatementEnd
