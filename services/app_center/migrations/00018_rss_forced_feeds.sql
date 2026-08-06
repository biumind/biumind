-- M11.4 (v3): 组织强制订阅. org admin 可以给全 org 强制订阅一批 feeds —
-- 这些 feed 以 (scope='org', scope_id=org_id, forced=true) 落库, 每个 org
-- 成员的 feeds_list 都会 union 进来, 且成员不能取消 (客户端删除按钮
-- disabled, 后端 RemoveFeed 拒绝 forced 行).
--
-- forced 复用现有 rss.feeds 表 — 不另起一张表 (强制订阅本质就是一条
-- org-scope 的订阅, 只是多一个"成员不可删"标记).

-- +goose Up
ALTER TABLE rss.feeds
    ADD COLUMN IF NOT EXISTS forced bool NOT NULL DEFAULT false;

-- org 成员 feeds_list 时按 (scope='org', scope_id=org_id, forced) join 进
-- 本 org 的强制源. partial index 只覆盖 forced 行 (绝大多数 feed 非
-- forced, 全表索引浪费).
CREATE INDEX IF NOT EXISTS feeds_forced_idx
    ON rss.feeds (scope, scope_id)
    WHERE forced;

-- +goose Down
DROP INDEX IF EXISTS rss.feeds_forced_idx;
ALTER TABLE rss.feeds DROP COLUMN IF EXISTS forced;
