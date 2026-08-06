-- +goose Up
-- +goose StatementBegin

-- mp_subscriptions — 用户授权的小程序订阅消息记录.
--
-- 每个 (user_id, platform, template_id) 是一条授权; 用户可对一个 template 多次
-- 授权 (微信小程序"一次性订阅消息"每点一次只能用一次), times_remaining 记
-- 剩余可发次数. notify worker 发完一次扣 1, 归 0 后停发.
--
-- platform 枚举与 identity_providers.provider 一致 (wechat_mp / alipay_mp / ...).
-- openid 冗余存 — 避免发消息时还要 join identity_providers 一次.
--
-- 全平台模板都打到这一张表 — 各平台 API 调用差异在 worker 层处理.

CREATE TABLE IF NOT EXISTS identity.mp_subscriptions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    platform        text NOT NULL,
    openid          text NOT NULL,
    template_id     text NOT NULL,
    times_remaining int  NOT NULL DEFAULT 1,
    granted_at      timestamptz NOT NULL DEFAULT now(),
    last_sent_at    timestamptz
);

CREATE INDEX IF NOT EXISTS mp_subscriptions_user_idx
    ON identity.mp_subscriptions(user_id);

-- worker 选取待发: WHERE times_remaining > 0 AND template_id = ?
CREATE INDEX IF NOT EXISTS mp_subscriptions_dispatch_idx
    ON identity.mp_subscriptions(template_id, platform)
    WHERE times_remaining > 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS identity.mp_subscriptions_dispatch_idx;
DROP INDEX IF EXISTS identity.mp_subscriptions_user_idx;
DROP TABLE IF EXISTS identity.mp_subscriptions;
-- +goose StatementEnd
