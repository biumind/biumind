-- +goose Up
-- +goose StatementBegin

-- identity.users 加 role / plan + role 变更追踪.
--
-- role:
--   后台 RBAC 主键. 默认 'user' 不能登后台. 'admin'/'superadmin' 等
--   是后台角色, 由 token 签发时写入 claims.Roles, 后端中间件检查.
--
-- plan:
--   billing.Plan 字符串 (free/pro/team). 之前硬编码 free, 现在落表
--   方便 admin 改套餐 + Stripe webhook 同步.
--
-- role_assigned_*:
--   审计角色变更. 谁改的, 什么时候, 为什么. 通过 admin UI 改 role 时
--   必须填 reason.

ALTER TABLE identity.users
    ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'user'
        CHECK (role IN ('user','support','finance','ops','admin','superadmin','viewer')),
    ADD COLUMN IF NOT EXISTS plan text NOT NULL DEFAULT 'free'
        CHECK (plan IN ('free','pro','team')),
    ADD COLUMN IF NOT EXISTS role_assigned_at     timestamptz,
    ADD COLUMN IF NOT EXISTS role_assigned_by     uuid REFERENCES identity.users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS role_assigned_reason text;

-- 仅给非 user 行建索引, 后台查列表只看后台角色
CREATE INDEX IF NOT EXISTS users_role_idx ON identity.users(role) WHERE role <> 'user';

-- 后台搜索按 email 模糊匹配 + plan 过滤. citext 已唯一索引, 这里加复合
-- 可选, 当前数据量低不必, 留作后期优化.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE identity.users
    DROP COLUMN IF EXISTS role_assigned_reason,
    DROP COLUMN IF EXISTS role_assigned_by,
    DROP COLUMN IF EXISTS role_assigned_at,
    DROP COLUMN IF EXISTS plan,
    DROP COLUMN IF EXISTS role;
DROP INDEX IF EXISTS identity.users_role_idx;
-- +goose StatementEnd
