-- M11.3 (v3): 公开只读分享 view. 任意 view (Today / 雷达 / Saved) 可生成
-- 一个不可猜的 token, 经公开路由 GET /share/rss/{token} 渲染只读 HTML —
-- token 即认证 (对照 app_center webhook 的 HMAC 范式), 不需要登录.
--
-- 设计:
--   - token 是 PK, 22+ 字符 base64url 随机串 (crypto/rand), 不可枚举.
--   - filter_json 存生成时的 view 过滤参数 (feed_id / rule_id / 等), 渲染
--     时按 view_kind + filter 拉当前数据 (live, 不快照 — 分享的是"视图"
--     不是"快照", 内容随源更新).
--   - expires_at 默认 +30d; revoked_at 非空表示主动撤销. 渲染路由两者
--     都要校验.
--   - scope/scope_id 记生成者当时的租户 (user 或 org), 拉数据时复用.

-- +goose Up
CREATE TABLE IF NOT EXISTS rss.shared_views (
    token           text        PRIMARY KEY,
    owner_user_id   text        NOT NULL,
    owner_org_id    text        NOT NULL DEFAULT '',
    -- 'today' | 'radar' | 'saved' | 'inbox' — 渲染路由按此选数据源.
    view_kind       text        NOT NULL,
    -- 生成时的过滤参数 (feed_id / rule_id / unread_only ...). 空对象合法.
    filter_json     jsonb       NOT NULL DEFAULT '{}',
    -- 数据租户. 'user'+user_id 或 'org'+org_id, 拉只读数据时复用.
    scope           text        NOT NULL DEFAULT 'user',
    scope_id        text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz
);

-- shares_list: 按 owner 拉"我创建的分享".
CREATE INDEX IF NOT EXISTS shared_views_owner_idx
    ON rss.shared_views (owner_user_id, created_at DESC);

-- 渲染路由按 token 命中后还要判活. 给定期清理 / 活跃分享查询用 — 只
-- 索引未撤销的, 过期清理走 expires_at.
CREATE INDEX IF NOT EXISTS shared_views_active_idx
    ON rss.shared_views (expires_at)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS rss.shared_views_active_idx;
DROP INDEX IF EXISTS rss.shared_views_owner_idx;
DROP TABLE IF EXISTS rss.shared_views;
