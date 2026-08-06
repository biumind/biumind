-- +goose Up
-- +goose StatementBegin

-- ─── RBAC 完整模型 ────────────────────────────────────
--
-- 4 张表:
--   identity.roles            角色定义 (7 个内置)
--   identity.permissions      权限定义 (28 个细粒度)
--   identity.role_permissions 多对多关联
--   audit.events              持久化审计日志 (替换 admin 内存 ring buffer)
--
-- 后续 superadmin 可通过 /v1/admin/roles/{role}/permissions 修改关联,
-- 当前 commit seed 完整矩阵.

CREATE TABLE IF NOT EXISTS identity.roles (
    name         text PRIMARY KEY,
    display_name text NOT NULL,
    description  text NOT NULL DEFAULT '',
    is_system    boolean NOT NULL DEFAULT false,    -- true=不可删/不可改 system 内置
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity.permissions (
    name        text PRIMARY KEY,                   -- 'users:read:full' 格式
    resource    text NOT NULL,
    action      text NOT NULL,
    scope       text,
    description text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity.role_permissions (
    role_name       text NOT NULL REFERENCES identity.roles(name)       ON DELETE CASCADE,
    permission_name text NOT NULL REFERENCES identity.permissions(name) ON DELETE CASCADE,
    granted_at      timestamptz NOT NULL DEFAULT now(),
    granted_by      uuid,
    PRIMARY KEY (role_name, permission_name)
);

-- 让 identity.users.role 升级为外键 (commit A 是简单 CHECK)
-- 因为 commit A 已经写入了非 user role 的种子数据, 这里要先确认值都在 roles 表
ALTER TABLE identity.users
    DROP CONSTRAINT IF EXISTS users_role_check;

-- 持久化 audit (替换 admin/admin.go 的 ring buffer)
CREATE SCHEMA IF NOT EXISTS audit;
CREATE TABLE IF NOT EXISTS audit.events (
    id            bigserial PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    actor_id      uuid,
    actor_email   text,
    actor_role    text,
    actor_ip      inet,
    actor_ua      text,
    action        text NOT NULL,
    resource      text,
    target_id     text,
    target_type   text,
    detail        jsonb,
    success       boolean NOT NULL DEFAULT true,
    error_code    text,
    error_message text
);
CREATE INDEX IF NOT EXISTS audit_events_at_idx     ON audit.events(at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor_idx  ON audit.events(actor_id, at DESC);
CREATE INDEX IF NOT EXISTS audit_events_target_idx ON audit.events(target_type, target_id, at DESC);
CREATE INDEX IF NOT EXISTS audit_events_action_idx ON audit.events(action, at DESC);

-- ─── Seed 7 内置角色 ──────────────────────────────────
INSERT INTO identity.roles (name, display_name, description, is_system) VALUES
  ('superadmin', '超级管理员', '系统全部权限, 可分配任何角色',     true),
  ('admin',      '管理员',     '用户/套餐/Provider/限额管理',      true),
  ('support',    '客服',       '用户查询和基础信息修改',           true),
  ('finance',    '财务',       '订阅/发票/退款管理',               true),
  ('ops',        '运维',       '服务监控/任务/系统配置查看',        true),
  ('viewer',     '只读',       '所有模块只读',                     true),
  ('user',       '普通用户',   '终端用户 (无后台权限)',            true)
ON CONFLICT (name) DO NOTHING;

-- 现在把 users.role 升级为外键
ALTER TABLE identity.users
    ADD CONSTRAINT users_role_fkey FOREIGN KEY (role)
        REFERENCES identity.roles(name) ON UPDATE CASCADE;

-- ─── Seed 28 个 permission ───────────────────────────
INSERT INTO identity.permissions (name, resource, action, scope, description) VALUES
  -- users
  ('users:read:full',      'users', 'read',   'full',  '完整用户数据 (含 api_key/refresh_token/last_ip)'),
  ('users:read:safe',      'users', 'read',   'safe',  '脱敏用户数据'),
  ('users:write:plan',     'users', 'write',  'plan',  '修改用户套餐'),
  ('users:write:role',     'users', 'write',  'role',  '修改用户角色 (仅 superadmin)'),
  ('users:write:profile',  'users', 'write',  'profile','修改用户基础信息'),
  ('users:delete',         'users', 'delete', NULL,    '删除/封禁用户'),
  ('users:impersonate',    'users', 'exec',   'login', '以用户身份登录 (高敏)'),
  -- plans
  ('plans:read',           'plans', 'read',   NULL,    '查看套餐定义'),
  ('plans:write',          'plans', 'write',  NULL,    '修改套餐限额 (影响所有该 plan 用户)'),
  -- providers (全局 LLM)
  ('providers:read',       'providers', 'read', NULL,  '查看全局 provider 列表'),
  ('providers:write',      'providers', 'write',NULL,  '增删改全局 provider'),
  ('providers:exec',       'providers', 'exec', NULL,  '测试 provider 连通性'),
  -- limits
  ('limits:read',          'limits', 'read',  NULL,    '查看用户限额'),
  ('limits:write',         'limits', 'write', NULL,    '修改用户限额'),
  -- audit
  ('audit:read',           'audit',  'read',  NULL,    '查看审计日志'),
  -- monitor
  ('monitor:read',         'monitor','read',  NULL,    '查看服务健康/监控'),
  -- tasks
  ('tasks:read',           'tasks',  'read',  NULL,    '查看 runtime 任务'),
  ('tasks:exec',           'tasks',  'exec',  NULL,    '取消 runtime 任务'),
  ('tasks:delete',         'tasks',  'delete',NULL,    '删除 runtime 任务'),
  -- billing
  ('billing:read',         'billing','read',  NULL,    '查看订阅/发票'),
  ('billing:exec',         'billing','exec',  NULL,    '退款'),
  ('billing:write',        'billing','write', NULL,    '改订阅 (谨慎)'),
  -- sessions
  ('sessions:revoke',      'sessions','exec', NULL,    '撤用户 refresh_token'),
  -- roles
  ('roles:read',           'roles',  'read',  NULL,    '查看角色定义和谁是什么角色'),
  ('roles:write',          'roles',  'write', NULL,    '改用户 role / 改 role 权限矩阵'),
  -- system
  ('system:read',          'system', 'read',  NULL,    '查看系统配置'),
  ('system:write',         'system', 'write', NULL,    '修改系统配置 (feature flag)'),
  -- 通配 (super 用)
  ('*',                    '*',      '*',     NULL,    '所有权限')
ON CONFLICT (name) DO NOTHING;

-- ─── Seed role_permissions 关联 ────────────────────
-- superadmin: *
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('superadmin', '*')
ON CONFLICT DO NOTHING;

-- admin: 不能改 role / 不能看财务
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('admin', 'users:read:full'),  ('admin', 'users:write:plan'),
  ('admin', 'users:write:profile'), ('admin', 'users:delete'),
  ('admin', 'plans:read'),       ('admin', 'plans:write'),
  ('admin', 'providers:read'),   ('admin', 'providers:write'), ('admin', 'providers:exec'),
  ('admin', 'limits:read'),      ('admin', 'limits:write'),
  ('admin', 'audit:read'),       ('admin', 'monitor:read'),
  ('admin', 'tasks:read'),       ('admin', 'tasks:exec'),
  ('admin', 'sessions:revoke'),  ('admin', 'roles:read')
ON CONFLICT DO NOTHING;

-- support: 用户脱敏 + 改基础信息 + audit
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('support', 'users:read:safe'), ('support', 'users:write:profile'),
  ('support', 'audit:read')
ON CONFLICT DO NOTHING;

-- finance: 用户脱敏 + 财务全套
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('finance', 'users:read:safe'),
  ('finance', 'billing:read'), ('finance', 'billing:exec'),
  ('finance', 'audit:read')
ON CONFLICT DO NOTHING;

-- ops: 监控/任务/限额读/审计/系统
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('ops', 'monitor:read'),
  ('ops', 'tasks:read'),    ('ops', 'tasks:exec'),    ('ops', 'tasks:delete'),
  ('ops', 'limits:read'),
  ('ops', 'audit:read'),    ('ops', 'system:read')
ON CONFLICT DO NOTHING;

-- viewer: 全局只读 (不含敏感)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('viewer', 'users:read:safe'),  ('viewer', 'plans:read'),
  ('viewer', 'providers:read'),   ('viewer', 'limits:read'),
  ('viewer', 'audit:read'),       ('viewer', 'monitor:read'),
  ('viewer', 'tasks:read'),       ('viewer', 'billing:read')
ON CONFLICT DO NOTHING;

-- user: 无任何 admin 权限 (留空)

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit.events;
DROP SCHEMA IF EXISTS audit CASCADE;
ALTER TABLE identity.users DROP CONSTRAINT IF EXISTS users_role_fkey;
DROP TABLE IF EXISTS identity.role_permissions;
DROP TABLE IF EXISTS identity.permissions;
DROP TABLE IF EXISTS identity.roles;
-- +goose StatementEnd
