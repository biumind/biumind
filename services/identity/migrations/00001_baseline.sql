-- ═══════════════════════════════════════════════════════════════════
-- identity 服务数据库基线 (squash baseline)
--
-- 本文件由 00001–00036 共 36 个历史 migration squash 而成, 面向全新部署
-- (不考虑存量库, 所有 backfill / 中间态 ALTER / 数据修复语句已省略,
-- 各表直接以终态建出).
--
-- 折叠说明:
--   * billing.pricing_book 不进基线 — 该表由旧 00019 建 + seed, 00029 扩
--     CHECK, 00030 整体 DROP. 价格体系单 SoT 已迁往 model_relay.pricing
--     (跨服务因果: 原 00030 要求 model-relay 端 00013_pricing_unify.sql
--     先把生效行吸走; 基线里该历史不可见, 仅留此注释).
--   * 旧 00017 credit_recharge_options seed 原为裸 INSERT 非幂等, 基线
--     改为 WHERE NOT EXISTS 守卫 (表上无可用唯一约束).
--   * billing.events 分区不再硬编码 202606/202607/202608, 改为按部署
--     时间动态建当月 + 后 3 个月共 4 个月度分区. 运维仍需按原有 cron
--     机制定期提前建后续月份分区.
--   * 刻意不建跨 schema FK 的地方保持不建 (api_tokens / billing 各表,
--     服务边界, 与原注释一致).
-- ═══════════════════════════════════════════════════════════════════

-- +goose Up

-- ─── 扩展与 schema ──────────────────────────────────────
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS citext;    -- identity.users.email
CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- gen_random_uuid (PG13+ 已内置, 兜底)

CREATE SCHEMA IF NOT EXISTS identity;
CREATE SCHEMA IF NOT EXISTS biumind_system;
CREATE SCHEMA IF NOT EXISTS billing;
CREATE SCHEMA IF NOT EXISTS audit;

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- RBAC 表 (roles / permissions / role_permissions)
-- users.role 直接 FK 到 roles (历史: 00002 CHECK → 00003 升级 FK, 基线
-- 一步建终态, 故 roles 必须先于 users).
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

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

-- 持久化 audit (替换 admin/admin.go 的 ring buffer)
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

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- 核心用户表: users / refresh_tokens / virtual_keys /
--             email_verifications / password_resets
--
-- users 终态 = 00001 + 00002 (role/plan/role_assigned_*) + 00005
-- (email_verified_at); role 直接 FK roles (00003 定稿, 00002 的 role
-- CHECK 不进基线).
--
-- refresh_tokens 终态 = 00001 + 00007 (last_used_at/last_ip/last_ua) +
-- 00008 (installation_id + partial unique) + 00021 (absolute_expires_at,
-- 直接 NOT NULL, backfill 省略) + 00032 (rotated_to/rotated_token_enc).
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS identity.users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext UNIQUE NOT NULL,
    password_hash   text,
    display_name    text NOT NULL DEFAULT '',
    default_org_id  uuid,
    -- 后台 RBAC 主键. 默认 'user' 不能登后台; 后台角色由 token 签发时
    -- 写入 claims.Roles, 后端中间件检查.
    role            text NOT NULL DEFAULT 'user'
                    REFERENCES identity.roles(name) ON UPDATE CASCADE,
    -- billing.Plan 字符串 (free/pro/team), 落表方便 admin 改套餐 +
    -- Stripe webhook 同步. 真源在 billing.subscriptions, 本列是 denorm.
    plan            text NOT NULL DEFAULT 'free'
                    CHECK (plan IN ('free','pro','team')),
    -- 审计角色变更: 谁改的, 什么时候, 为什么.
    role_assigned_at     timestamptz,
    role_assigned_by     uuid REFERENCES identity.users(id) ON DELETE SET NULL,
    role_assigned_reason text,
    -- email 验证: NULL → 不允许 login (00005; 存量 backfill 省略).
    email_verified_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- 仅给非 user 行建索引, 后台查列表只看后台角色
CREATE INDEX IF NOT EXISTS users_role_idx ON identity.users(role) WHERE role <> 'user';

CREATE TABLE IF NOT EXISTS identity.refresh_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    token_hash      bytea NOT NULL UNIQUE,
    device_name     text NOT NULL DEFAULT '',
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- 可观察性 (00007): 已登录设备列表展示 "最近活跃 / 在哪登录 / 设备类型"
    last_used_at    timestamptz,
    last_ip         inet,
    last_ua         text,
    -- "授权的是设备, 不是 token" (00008): 客户端首次启动生成的永久 UUID.
    -- 同 (user, install) 反复登入登出复用同一行 (token_hash 原地 rotate).
    installation_id text NOT NULL DEFAULT '',
    -- 绝对过期上限 (00021): 首次签发定死 (created_at + 1y), rotation 不重置.
    absolute_expires_at timestamptz NOT NULL,
    -- rotation grace window 链 (00032): rotate 时把新行 id + 新 token 密文
    -- 回写到被 revoke 的老行, 形成 A → B → C 链. NULL = 非 rotate 撤销.
    rotated_to        uuid,
    rotated_token_enc bytea
);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_idx ON identity.refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_expires_idx ON identity.refresh_tokens(expires_at);
-- partial unique 只约束 "active 且 install_id 非空" 的行: 给 ON CONFLICT
-- 落地点; revoked 行 / 老 install_id='' 行不参与.
CREATE UNIQUE INDEX IF NOT EXISTS refresh_tokens_active_device_idx
    ON identity.refresh_tokens(user_id, installation_id)
    WHERE revoked_at IS NULL AND installation_id <> '';

CREATE TABLE IF NOT EXISTS identity.virtual_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    prefix          text UNIQUE NOT NULL,
    secret_hash     bytea NOT NULL,
    name            text NOT NULL DEFAULT '',
    scope           jsonb NOT NULL DEFAULT '{}'::jsonb,
    expires_at      timestamptz,
    revoked_at      timestamptz,
    last_used_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS virtual_keys_user_idx ON identity.virtual_keys(user_id);

-- email 验证 code (6 位, sha256 hash, 不存明文)
CREATE TABLE IF NOT EXISTS identity.email_verifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
    code_hash   bytea NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    attempts    int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- 拿"该用户最新未消费 code" 是热路径 (verify + resend 都用)
CREATE INDEX IF NOT EXISTS email_verifications_user_idx
    ON identity.email_verifications(user_id);
-- 后台清理已过期未消费的 code 时按 expires_at 排序
CREATE INDEX IF NOT EXISTS email_verifications_expires_idx
    ON identity.email_verifications(expires_at);

-- 密码重置 — 与 email_verifications 同形但独立建表: 一个 code 不能
-- 既验邮箱又改密码; 已验证的老用户也能走忘记密码.
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

-- ═══════════════════════════════════════════════════════════════════
-- RBAC seed (00003: 7 角色 + 28 权限 + 完整矩阵; 00016: model_relay 6 权限
-- + 映射). 全部 ON CONFLICT 幂等.
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

-- ─── 7 内置角色 ─────────────────────────────────────────
INSERT INTO identity.roles (name, display_name, description, is_system) VALUES
  ('superadmin', '超级管理员', '系统全部权限, 可分配任何角色',     true),
  ('admin',      '管理员',     '用户/套餐/Provider/限额管理',      true),
  ('support',    '客服',       '用户查询和基础信息修改',           true),
  ('finance',    '财务',       '订阅/发票/退款管理',               true),
  ('ops',        '运维',       '服务监控/任务/系统配置查看',        true),
  ('viewer',     '只读',       '所有模块只读',                     true),
  ('user',       '普通用户',   '终端用户 (无后台权限)',            true)
ON CONFLICT (name) DO NOTHING;

-- ─── 28 个 permission (00003) + 6 个 model_relay permission (00016) ──
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
  -- providers (全局 LLM; 复用给 model_relay.providers 后台, 见 00016)
  ('providers:read',       'providers', 'read', NULL,  '查看全局 provider 列表'),
  ('providers:write',      'providers', 'write',NULL,  '增删改全局 provider'),
  ('providers:exec',       'providers', 'exec', NULL,  '测试 provider 连通性'),
  -- limits (identity 用户级配额, 非 model_relay channel rpm/tpm)
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
  ('*',                    '*',      '*',     NULL,    '所有权限'),
  -- model_relay admin (00016, MC-M1.4)
  ('models:read',
   'models', 'read', NULL,
   '查看 model_relay 的模型 / 渠道 / 分组 / 定价 / 汇率（统一读权限）'),
  ('models:write',
   'models', 'write', NULL,
   '增删改 model_relay 的模型 / 渠道 / 分组，运行同步与一键测试'),
  ('model_credentials:read',
   'model_credentials', 'read', 'safe',
   '查看上游凭证列表（脱敏，仅 key_preview，不含明文）'),
  ('model_credentials:write',
   'model_credentials', 'write', NULL,
   '增删改上游凭证（明文写入，envelope 加密落库）'),
  ('pricing:write',
   'pricing', 'write', NULL,
   '修改模型定价（影响所有用户结算）'),
  ('fx_rates:write',
   'fx_rates', 'write', NULL,
   '修改汇率（影响 CNY ↔ USD 折算）')
ON CONFLICT (name) DO NOTHING;

-- ─── role_permissions 关联矩阵 ──────────────────────────
-- superadmin: *
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('superadmin', '*')
ON CONFLICT DO NOTHING;

-- admin: 不能改 role / 不能看财务 + model_relay 全部 6 权限 (00016)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('admin', 'users:read:full'),  ('admin', 'users:write:plan'),
  ('admin', 'users:write:profile'), ('admin', 'users:delete'),
  ('admin', 'plans:read'),       ('admin', 'plans:write'),
  ('admin', 'providers:read'),   ('admin', 'providers:write'), ('admin', 'providers:exec'),
  ('admin', 'limits:read'),      ('admin', 'limits:write'),
  ('admin', 'audit:read'),       ('admin', 'monitor:read'),
  ('admin', 'tasks:read'),       ('admin', 'tasks:exec'),
  ('admin', 'sessions:revoke'),  ('admin', 'roles:read'),
  ('admin', 'models:read'),      ('admin', 'models:write'),
  ('admin', 'model_credentials:read'), ('admin', 'model_credentials:write'),
  ('admin', 'pricing:write'),    ('admin', 'fx_rates:write')
ON CONFLICT DO NOTHING;

-- support: 用户脱敏 + 改基础信息 + audit + model_relay 脱敏读 (00016)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('support', 'users:read:safe'), ('support', 'users:write:profile'),
  ('support', 'audit:read'),
  ('support', 'models:read'), ('support', 'model_credentials:read')
ON CONFLICT DO NOTHING;

-- finance: 用户脱敏 + 财务全套 + models:read (00016)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('finance', 'users:read:safe'),
  ('finance', 'billing:read'), ('finance', 'billing:exec'),
  ('finance', 'audit:read'),
  ('finance', 'models:read')
ON CONFLICT DO NOTHING;

-- ops: 监控/任务/限额读/审计/系统 + models:read (00016, 监控渠道健康)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('ops', 'monitor:read'),
  ('ops', 'tasks:read'),    ('ops', 'tasks:exec'),    ('ops', 'tasks:delete'),
  ('ops', 'limits:read'),
  ('ops', 'audit:read'),    ('ops', 'system:read'),
  ('ops', 'models:read')
ON CONFLICT DO NOTHING;

-- viewer: 全局只读 (不含敏感) + models:read (00016)
INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('viewer', 'users:read:safe'),  ('viewer', 'plans:read'),
  ('viewer', 'providers:read'),   ('viewer', 'limits:read'),
  ('viewer', 'audit:read'),       ('viewer', 'monitor:read'),
  ('viewer', 'tasks:read'),       ('viewer', 'billing:read'),
  ('viewer', 'models:read')
ON CONFLICT DO NOTHING;

-- user: 无任何 admin 权限 (留空)

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- system_config — 后台运维配置 KV 存储 (00004 + 00005 auth.email +
-- 00026 支付通道). value 用 JSONB 保留结构; secret 字段标识含敏感数据
-- 的 value, API 返回时按 RBAC 决定脱敏.
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS biumind_system.config (
    key         text PRIMARY KEY,
    value       jsonb NOT NULL,
    secret      boolean NOT NULL DEFAULT false,
    description text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  uuid REFERENCES identity.users(id) ON DELETE SET NULL
);

-- 内置 key 占位 (UI 不显示, 但 superadmin 可填)
INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  ('alert.email', '{"enabled":false,"smtp_host":"","smtp_port":465,"smtp_user":"","smtp_pass":"","smtp_tls":true,"from":"","to":[]}',
   true, 'Alertmanager webhook 通过这里发邮件 (SMTP 配置)')
ON CONFLICT (key) DO NOTHING;

-- 邮件验证 SMTP + 模板配置占位. enabled=false 时注册照常但 code 写日志
-- (dev 兜底), 不影响测试.
INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  ('auth.email',
   '{"enabled":false,"smtp_host":"","smtp_port":465,"smtp_user":"","smtp_pass":"","smtp_tls":true,"from":"","code_ttl_seconds":600,"subject":"[BiuMind] 邮箱验证码"}',
   true,
   '注册邮箱验证 (SMTP). enabled=false 时 code 仅写入日志 (dev 兜底).')
ON CONFLICT (key) DO NOTHING;

-- 支付通道配置占位 (00026): secret=true → API 响应给非 superadmin 时全
-- 脱敏 ***; 实际值由 superadmin 在后台 UI 覆盖.
INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  (
    'payment.stripe',
    '{"enabled":false,"secret_key":"","webhook_secret":"","price_to_plan":{}}'::jsonb,
    true,
    'Stripe 支付 — secret_key (sk_live_...) + webhook secret + Price ID → Plan 映射'
  ),
  (
    'payment.wechat',
    '{"enabled":false,"app_id":"","mch_id":"","apiv3_key":"","cert_serial_no":"","apiclient_key_pem":"","platform_public_key":"","notify_url":""}'::jsonb,
    true,
    '微信支付 v3 — 商户号 + APIv3 密钥 (32B) + 商户私钥 PEM + 平台公钥 PEM + https 回调 URL'
  ),
  (
    'payment.alipay',
    '{"enabled":false,"app_id":"","private_key_pem":"","alipay_public_key_pem":"","notify_url":"","return_url":""}'::jsonb,
    true,
    '支付宝 — AppID + 应用私钥 PEM + 支付宝公钥 PEM + https 回调 URL'
  )
ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- oauth / tokens / 第三方身份 / 安全与活动事件
-- (00009–00015 + 00021 security_events)
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

-- identity_providers — 第三方登录身份映射. 一个 user_id 可挂 N 个 provider.
--   1. (provider, provider_user_id) 唯一: 一个外部账号最多绑到一个 user
--   2. unionid: 仅微信生态有, 同 unionid 的 wechat_mp/oa/open 自动合并
--   3. raw_profile_json: 缓存 nickname/avatar 等, 不参与业务逻辑
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
-- unionid 查找索引 — 仅在非空时建, 跨 provider 合并查询用
CREATE INDEX IF NOT EXISTS identity_providers_unionid_idx
    ON identity.identity_providers(unionid)
    WHERE unionid IS NOT NULL;

-- mp_subscriptions — 小程序订阅消息授权记录. times_remaining 记剩余可发
-- 次数, notify worker 发完一次扣 1, 归 0 后停发.
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

-- oauth_states — H5 OAuth 2.0 授权码流防 CSRF + 跳回原页面 (5 min TTL,
-- 一次性使用, 校验通过即 DELETE).
CREATE TABLE IF NOT EXISTS identity.oauth_states (
    state        text PRIMARY KEY,
    provider     text NOT NULL,           -- 'wechat_web' / 'alipay_web' / ...
    return_url   text NOT NULL DEFAULT '/',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
-- GC 清扫过期 state 用
CREATE INDEX IF NOT EXISTS oauth_states_expires_idx
    ON identity.oauth_states(expires_at);

-- api_tokens — long-lived programmatic-access tokens (PAT), 格式
-- `bm_<8-char-prefix>_<jwt>`. 行只存 metadata + jti, 不存 secret.
-- workspace_id/project_id 是授权 scope, NULL = whole-user.
-- 刻意不建跨 schema FK (owner_id 不 FK users — 服务边界).
CREATE TABLE IF NOT EXISTS identity.api_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      uuid NOT NULL,
    workspace_id  uuid,
    project_id    uuid,
    name          text NOT NULL,
    prefix        text NOT NULL UNIQUE,
    -- jti claim of the minted JWT; UNIQUE so a single token can't
    -- be split across two rows on re-issue.
    jti           text NOT NULL UNIQUE,
    -- scopes are free-form strings for now; brain checks ones it
    -- recognises and ignores the rest. v1 known: "read", "write".
    scopes        text[] NOT NULL DEFAULT ARRAY[]::text[],
    last_used_at  timestamptz,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS api_tokens_owner_idx
    ON identity.api_tokens(owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS api_tokens_jti_idx
    ON identity.api_tokens(jti);

-- activity_events — user-facing "what's happening" 事件流 (P2-I-3).
-- 区别于 audit.events (安全/合规, admin-only).
CREATE TABLE IF NOT EXISTS identity.activity_events (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id              uuid NOT NULL,
    audience_user_id      uuid,
    audience_workspace_id uuid,
    kind                  text NOT NULL,
    target_type           text,
    target_id             text,
    summary               text NOT NULL,
    detail                jsonb,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS activity_events_audience_user_at_idx
    ON identity.activity_events (audience_user_id, created_at DESC)
    WHERE audience_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activity_events_audience_workspace_at_idx
    ON identity.activity_events (audience_workspace_id, created_at DESC)
    WHERE audience_workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activity_events_actor_at_idx
    ON identity.activity_events (actor_id, created_at DESC);

-- oauth_clients — OAuth 2.1 third-party application registry (RFC 7591
-- Dynamic Client Registration). public clients (PKCE-only) 的
-- client_secret_hash 为 NULL; redirect_uris 精确匹配, 关闭 open redirect.
CREATE TABLE IF NOT EXISTS identity.oauth_clients (
    client_id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_secret_hash          text,
    client_name                 text NOT NULL,
    redirect_uris               text[] NOT NULL,
    grant_types                 text[] NOT NULL DEFAULT ARRAY['authorization_code','refresh_token']::text[],
    response_types              text[] NOT NULL DEFAULT ARRAY['code']::text[],
    token_endpoint_auth_method  text NOT NULL DEFAULT 'none',  -- 'none' | 'client_secret_post' | 'client_secret_basic'
    scope                       text NOT NULL DEFAULT '',
    contacts                    text[] NOT NULL DEFAULT ARRAY[]::text[],
    logo_uri                    text,
    client_uri                  text,
    tos_uri                     text,
    policy_uri                  text,
    software_id                 text,
    software_version            text,
    -- registration_access_token (RFC 7592 client management) lets the
    -- registrar later GET/PUT/DELETE the client. We hash it the same way
    -- as the secret so a DB read can't replay it.
    registration_access_token_hash text,
    -- The user who registered this app — useful for audit + per-user
    -- rate-limit on registrations. Null for system-seeded clients.
    created_by                  uuid,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS oauth_clients_created_by_idx
    ON identity.oauth_clients (created_by, created_at DESC)
    WHERE created_by IS NOT NULL;

-- oauth_authorization_codes — OAuth 2.1 authorization code with PKCE.
-- code ~10 min TTL; consumed_at 原子置位, 二次消费应触发派生 token
-- 全部撤销 (RFC 6819 §5.2.1.1 / OAuth 2.1 §7.5.1).
CREATE TABLE IF NOT EXISTS identity.oauth_authorization_codes (
    code                  text PRIMARY KEY,
    client_id             uuid NOT NULL,
    user_id               uuid NOT NULL,
    redirect_uri          text NOT NULL,
    scope                 text NOT NULL DEFAULT '',
    code_challenge        text NOT NULL,
    code_challenge_method text NOT NULL,
    expires_at            timestamptz NOT NULL,
    consumed_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS oauth_codes_expires_idx
    ON identity.oauth_authorization_codes (expires_at);
CREATE INDEX IF NOT EXISTS oauth_codes_client_idx
    ON identity.oauth_authorization_codes (client_id, created_at DESC);

-- security_events — 安全事件审计 (00021). 第一类 kind='refresh_token_reuse'
-- 由 reuse detection 写入: 已 revoked 的 refresh_token 被再次提交说明
-- token 链路有泄漏.
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

-- ═══════════════════════════════════════════════════════════════════
-- credits subsystem (00017 + 00018; CHECK 用 00029 终态)
--
-- 双账户积分: permanent (单充买入 / 永不过期) + time_limited (套餐附赠 /
-- 活动赠送, 按 expires_at 过期). 扣减: 优先最早过期的时效包 → 最后永久.
-- 退款: 按 credit_logs.consume_breakdown_json 原路径返还到 packages.
--
-- 跨 schema FK 不使用: credit_packages.user_id 等不 FK 到 users(id).
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

-- ─── 余额 (聚合视图, UI 显示用; 真实账本以 credit_packages 为准) ──
CREATE TABLE IF NOT EXISTS identity.user_credits (
    user_id                       uuid PRIMARY KEY,
    permanent_balance             bigint NOT NULL DEFAULT 0
                                  CHECK (permanent_balance >= 0),
    time_limited_balance          bigint NOT NULL DEFAULT 0
                                  CHECK (time_limited_balance >= 0),
    -- 最早过期的时效包过期时间 (UI 显示「时效积分将于 X 月 Y 日过期」)
    time_limited_earliest_expires timestamptz,
    updated_at                    timestamptz DEFAULT now()
);

-- ─── 积分包 (按 expires_at 升序扣减; 同 user 可多个时效包并存) ─
CREATE TABLE IF NOT EXISTS identity.credit_packages (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('permanent','time_limited')),
    source         text NOT NULL CHECK (source IN
                   ('recharge','plan_grant','reward','refund','admin')),
    initial_amount bigint NOT NULL CHECK (initial_amount >= 0),
    remaining      bigint NOT NULL CHECK (remaining >= 0),
    expires_at     timestamptz,
    metadata       jsonb DEFAULT '{}'::jsonb,
    created_at     timestamptz DEFAULT now(),

    -- 不变量: time_limited 必须有 expires_at; permanent 必须无.
    CONSTRAINT credit_packages_kind_expires_consistent CHECK (
        (kind = 'time_limited' AND expires_at IS NOT NULL)
        OR (kind = 'permanent' AND expires_at IS NULL)
    )
);
-- 扣减时按这个索引扫: kind=time_limited AND remaining>0 ORDER BY expires_at, created_at.
CREATE INDEX IF NOT EXISTS identity_credit_packages_consumption_order
    ON identity.credit_packages (user_id, kind, expires_at NULLS LAST, created_at)
    WHERE remaining > 0;
CREATE INDEX IF NOT EXISTS identity_credit_packages_user_kind
    ON identity.credit_packages (user_id, kind);

-- ─── 流水 (每笔出入账都记一行, 退款回溯靠它) ────────────
-- delta > 0: 入账; delta < 0: 出账 (consume).
-- consume_breakdown_json: [{"package_id":"...","amount":40}, ...]
-- refund_of_log_id: 退款专用, 指向原扣减 log (幂等).
-- idempotency_key: 同 (user_id, idempotency_key) 重复请求只生效一次.
-- ref_type CHECK 为 00029 终态 (含 v0.3 新增 modality 的 *_request 后缀).
CREATE TABLE IF NOT EXISTS identity.credit_logs (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  uuid NOT NULL,
    delta                    bigint NOT NULL,
    consume_breakdown_json   jsonb,
    balance_after            bigint NOT NULL,

    ref_type                 text NOT NULL CHECK (ref_type IN
                             ('aigc_task','chat_message','recharge','plan_grant','refund','reward','admin',
                              'embedding_request','rerank_request','audio_speech_request',
                              'image_request','video_request')),
    ref_id                   text,
    remark                   text,

    refund_of_log_id         uuid REFERENCES identity.credit_logs(id),
    idempotency_key          text,

    created_at               timestamptz DEFAULT now()
);
CREATE INDEX IF NOT EXISTS identity_credit_logs_user_created
    ON identity.credit_logs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS identity_credit_logs_ref
    ON identity.credit_logs (ref_type, ref_id);
-- 幂等键唯一约束: 同 (user_id, idempotency_key) 只允许一条
CREATE UNIQUE INDEX IF NOT EXISTS identity_credit_logs_idempotency
    ON identity.credit_logs (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
-- 退款幂等: 同 refund_of_log_id 同 idempotency_key 只允许一条退款.
CREATE INDEX IF NOT EXISTS identity_credit_logs_refund_of
    ON identity.credit_logs (refund_of_log_id)
    WHERE refund_of_log_id IS NOT NULL;

-- ─── credit_holds (00018; ref_type CHECK 为 00029 终态) ──────────
-- 流式预扣 / 结算: 提交时 Hold 预扣 max_amount, 流结束后 Settle 真实金额
-- (≤ max), 失败 / 取消则 Release. 5min TTL 兜底防 hold leak — Reaper
-- goroutine 周期扫 expires_at < now() AND status='held' 自动 release.
CREATE TABLE IF NOT EXISTS identity.credit_holds (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL,
    ref_type            text NOT NULL CHECK (ref_type IN
                        ('chat_message','agent_step','aigc_task',
                         'embedding_request','rerank_request','audio_speech_request',
                         'image_request','video_request')),
    ref_id              text,
    -- 金额: max 上限, actual 在 settle 后填
    max_amount          bigint NOT NULL CHECK (max_amount > 0),
    actual_amount       bigint CHECK (actual_amount IS NULL OR actual_amount >= 0),
    -- 状态机: held → settled / released / expired (后三者为终态)
    status              text NOT NULL CHECK (status IN
                        ('held','settled','released','expired')),
    -- 拆账明细 (持有时按优先级顺序记录占用了哪些 package)
    hold_breakdown_json jsonb,
    -- 幂等
    idempotency_key     text,
    -- TTL: hold 时 +300s, expired 后由 reaper 释放对应 packages
    expires_at          timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    settled_at          timestamptz,

    -- 不变量: settled 必须有 actual_amount + settled_at
    CONSTRAINT credit_holds_settled_has_actual CHECK (
        (status = 'settled' AND actual_amount IS NOT NULL AND settled_at IS NOT NULL)
        OR (status <> 'settled')
    ),
    -- 不变量: actual ≤ max
    CONSTRAINT credit_holds_actual_le_max CHECK (
        actual_amount IS NULL OR actual_amount <= max_amount
    )
);
-- 用户活跃 hold 集合 (Hold/Settle/余额检查时用)
CREATE INDEX IF NOT EXISTS identity_credit_holds_user_active
    ON identity.credit_holds (user_id, status)
    WHERE status = 'held';
-- Reaper 扫: WHERE status='held' AND expires_at < now()
CREATE INDEX IF NOT EXISTS identity_credit_holds_expiry
    ON identity.credit_holds (expires_at)
    WHERE status = 'held';
-- 幂等: 同 (user_id, ref_type, idempotency_key) 只允许一条
CREATE UNIQUE INDEX IF NOT EXISTS identity_credit_holds_idempotency
    ON identity.credit_holds (user_id, ref_type, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
-- 业务反查 (调试 / 客服)
CREATE INDEX IF NOT EXISTS identity_credit_holds_ref
    ON identity.credit_holds (ref_type, ref_id)
    WHERE ref_id IS NOT NULL;

-- ─── 充值套餐选项 (运营配置) ─────────────────────────────
CREATE TABLE IF NOT EXISTS identity.credit_recharge_options (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name     text NOT NULL,
    credits_amount   bigint NOT NULL CHECK (credits_amount > 0),
    kind             text NOT NULL CHECK (kind IN ('permanent','time_limited')),
    price_micro_cny  bigint NOT NULL CHECK (price_micro_cny >= 0),
    valid_days       integer DEFAULT 0 CHECK (valid_days >= 0),
    enabled          boolean DEFAULT true,
    sort_order       integer DEFAULT 0,
    created_at       timestamptz DEFAULT now()
);
CREATE INDEX IF NOT EXISTS identity_credit_recharge_options_enabled
    ON identity.credit_recharge_options (enabled, sort_order);

-- ─── 用户存储配额 (跟 AIGC 文件存储绑定) ────────────────
-- 由 services/aigc 在转存 / 删除作品时调 identity 接口维护.
-- bytes_quota: free 5GB / pro 100GB / team 1TB (按 plan 计算, 由 billing 同步).
CREATE TABLE IF NOT EXISTS identity.user_storage (
    user_id      uuid PRIMARY KEY,
    bytes_used   bigint NOT NULL DEFAULT 0 CHECK (bytes_used >= 0),
    bytes_quota  bigint NOT NULL,
    file_count   integer NOT NULL DEFAULT 0,
    updated_at   timestamptz DEFAULT now()
);

-- ─── 默认充值套餐 seed ──────────────────────────────────
-- 原 00017 为裸 INSERT 非幂等 (squash 时修复). 表上无可用唯一约束
-- (display_name 无 UNIQUE), 故用 WHERE NOT EXISTS 按 display_name 守卫.
INSERT INTO identity.credit_recharge_options
    (display_name, credits_amount, kind, price_micro_cny, valid_days, sort_order)
SELECT v.display_name, v.credits_amount, v.kind, v.price_micro_cny, v.valid_days, v.sort_order
FROM (VALUES
    ('100 积分体验包',    100::bigint,  'permanent'::text,    9900000::bigint,   0,  10),  -- ¥9.9
    ('500 积分基础包',    500,          'permanent',         39900000,           0,  20),  -- ¥39.9
    ('1500 积分超值包',  1500,          'permanent',         99900000,           0,  30),  -- ¥99.9
    ('5000 积分专业包',  5000,          'permanent',        299900000,           0,  40),  -- ¥299.9
    ('限时 1000 积分时效包', 1000,       'time_limited',      49900000,          30,  50)   -- ¥49.9, 30 天
) AS v(display_name, credits_amount, kind, price_micro_cny, valid_days, sort_order)
WHERE NOT EXISTS (
    SELECT 1 FROM identity.credit_recharge_options o
    WHERE o.display_name = v.display_name
);

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- user_api_keys — BYOK (Bring Your Own Key)
--
-- 终态 = 00020 基础 + 00033 (base_url/protocol/custom provider) + 00034
-- (model_globs) + 00035 (is_client_side + 4 个终态 partial unique index).
-- 00020 的表级 UNIQUE(user_id, provider) 与 00033 的两个中间索引不进基线.
-- 00036 的 DELETE (清理旧 client-side 空占位行) 是存量数据修复, 不进基线.
--
-- is_client_side 语义 (00036 重定义): key 统一加密存 identity,
-- is_client_side=true 表示「需本机出口」(relay 连不到的上游, 如内网
-- proxy), 桌面 daemon 取 key 本机直连.
--
-- 加密: AES-256-GCM, 主密钥 BYOK_MASTER_KEY 仅从 env 注入; API 永远不
-- 返回明文, 只返 last4 + 状态 + 元数据.
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS identity.user_api_keys (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,

    -- provider 枚举 (与 model-relay 上游 provider 对齐; 'custom' = 用户自填
    -- 代理 new-api/one-api/vLLM)
    provider            text NOT NULL CHECK (provider IN
                        ('anthropic','openai','deepseek','doubao','dashscope',
                         'volcengine','google','azure_openai','moonshot','qwen','baichuan',
                         'custom')),

    -- 用户给的备注名 (e.g. "我的 OpenAI 主号")
    label               text,

    -- 加密存储 (AES-256-GCM)
    encrypted_value     bytea NOT NULL,
    nonce               bytea NOT NULL,
    -- 明文 last4 (用于客户端展示 "sk-...AbCd"), 不影响安全
    last4               text,

    -- 部分 provider 需要额外字段 (Azure endpoint / region 等)
    config_json         jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- 状态
    status              text NOT NULL DEFAULT 'valid' CHECK (status IN
                        ('valid','invalid','revoked','expired')),
    -- 健康检查 / 调用统计
    last_validated_at   timestamptz,
    last_used_at        timestamptz,
    failure_count       int NOT NULL DEFAULT 0 CHECK (failure_count >= 0),

    -- custom endpoint (00033): custom 必填, 标准 provider 可空走默认 endpoint
    base_url            text,
    -- 协议 (00033): model-relay 用它选 adaptor, 替代模型名前缀猜测
    protocol            text NOT NULL DEFAULT 'openai_compat'
                        CHECK (protocol IN ('openai_compat','anthropic','google','dashscope','volcengine')),

    -- custom 声明所用模型 (00034): {'glm-*'} / {'gpt-4o','claude-3'} / {'*'}
    model_globs         text[] NOT NULL DEFAULT '{}'::text[],

    -- client-side 标记 (00035/00036): true = 需本机出口, 桌面 daemon 取 key 直连
    is_client_side      boolean NOT NULL DEFAULT false,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    -- custom 必须配 base_url (00033)
    CONSTRAINT user_api_keys_custom_requires_endpoint
        CHECK (provider <> 'custom' OR base_url IS NOT NULL),
    -- custom 必须声明模型 (00034)
    CONSTRAINT user_api_keys_custom_requires_models
        CHECK (provider <> 'custom' OR array_length(model_globs, 1) >= 1)
);

-- 唯一性 (00035 终态): 同 (user, provider) 可同时存在 server 行 +
-- client-side 行, 故 std / custom 各按 is_client_side 拆两条 partial unique.
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_provider_std_server
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom' AND is_client_side = false;
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_provider_std_client
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom' AND is_client_side = true;
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_baseurl_custom_server
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom' AND is_client_side = false;
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_baseurl_custom_client
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom' AND is_client_side = true;

-- resolver 主索引: 给定 user + provider 查 valid 的 Key
CREATE INDEX IF NOT EXISTS identity_user_api_keys_active
    ON identity.user_api_keys (user_id, provider)
    WHERE status = 'valid';

-- 后台 / 风控按 provider 看分布
CREATE INDEX IF NOT EXISTS identity_user_api_keys_provider
    ON identity.user_api_keys (provider, status);

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- billing: 会员体系 + 支付 + 月度配额 + events 大宽表 + 试用防刷
-- (00022 + 00023 seed + 00024 + 00025 + 00027)
--
-- 跨 schema FK 不用 — billing.subscriptions.user_id 等不 FK 到
-- identity.users (服务边界), 用 uuid 即可.
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

-- ─── 1. plans (套餐字典) ─────────────────────────────────
-- 一行一档. code 是业务键, 与 identity.billing.Plan 字面量对齐.
-- benefits 是 jsonb, 内部结构对应 PlanLimits struct.
CREATE TABLE IF NOT EXISTS billing.plans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code            text UNIQUE NOT NULL CHECK (code IN ('free', 'pro', 'team', 'enterprise')),
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    sort_order      int NOT NULL DEFAULT 0,

    -- 价格 (主存原币种, 与 model_relay.pricing 同口径)
    price_currency  text NOT NULL DEFAULT 'USD' CHECK (price_currency IN ('USD', 'CNY')),
    price_monthly   numeric(14, 2) NOT NULL DEFAULT 0,
    price_yearly    numeric(14, 2) NOT NULL DEFAULT 0,

    -- 月度积分配额 (月度结算用)
    monthly_credits bigint NOT NULL DEFAULT 0,

    -- benefits jsonb: {"hub_rpm": 60, "hub_tpm": 50000, "sandbox_daily":10, ...}
    -- 字段集对齐 services/identity/internal/billing/billing.go PlanLimits struct.
    benefits        jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Stripe 关联 (国内 IAP 不在本表)
    stripe_price_monthly text,
    stripe_price_yearly  text,

    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS plans_status_sort ON billing.plans (status, sort_order);

-- ─── 2. subscriptions (订阅状态机) ─────────────────────
-- 一行一订阅 (一个用户在某时刻只有一条 active 订阅 — 升降级是 update
-- 此行而不是新建). status 状态机:
--   trialing → active / active → canceled / active → past_due /
--   past_due → active|canceled / canceled → expired
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               uuid NOT NULL,                          -- 不 FK 到 identity.users
    plan_id               uuid NOT NULL REFERENCES billing.plans(id),

    status                text NOT NULL DEFAULT 'trialing'
                              CHECK (status IN ('trialing', 'active', 'past_due', 'canceled', 'expired')),

    -- 周期
    current_period_start  timestamptz NOT NULL,
    current_period_end    timestamptz NOT NULL,
    trial_end_at          timestamptz,                            -- NULL = 无试用期
    cancel_at             timestamptz,                            -- 用户预约取消时间; 一般 = current_period_end
    canceled_at           timestamptz,                            -- 实际取消时间 (操作发生时)
    expired_at            timestamptz,                            -- 服务停止时间

    -- 计费 (主存原币种)
    billing_cycle         text NOT NULL DEFAULT 'monthly'
                              CHECK (billing_cycle IN ('monthly', 'yearly', 'lifetime')),

    -- Stripe 关联
    stripe_customer_id    text,
    stripe_subscription_id text UNIQUE,                           -- 跨 webhook 唯一键

    -- 审计字段
    metadata              jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
-- 一个 user 一个 active 订阅 (其余 status 历史保留)
CREATE UNIQUE INDEX IF NOT EXISTS subscriptions_user_active
    ON billing.subscriptions (user_id)
    WHERE status IN ('trialing', 'active', 'past_due');
CREATE INDEX IF NOT EXISTS subscriptions_user_history ON billing.subscriptions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscriptions_status      ON billing.subscriptions (status, current_period_end);
CREATE INDEX IF NOT EXISTS subscriptions_stripe_cust ON billing.subscriptions (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;

-- ─── 3. subscription_events (审计流, 不可变 append-only) ──
CREATE TABLE IF NOT EXISTS billing.subscription_events (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id   uuid NOT NULL REFERENCES billing.subscriptions(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,                              -- denorm 方便按 user 查

    event_type        text NOT NULL,                              -- 'created' | 'activated' | 'renewed' | 'upgraded' | 'downgraded' | 'canceled' | 'expired' | 'past_due' | 'recovered' | 'payment_succeeded' | 'payment_failed' | 'refunded'

    -- 变更前后状态 (event_type='upgraded' / 'downgraded' 时 from/to 必填)
    from_plan_id      uuid REFERENCES billing.plans(id),
    to_plan_id        uuid REFERENCES billing.plans(id),
    from_status       text,
    to_status         text,

    -- Stripe 事件溯源
    stripe_event_id   text UNIQUE,                                -- 防 webhook 重复 (Stripe 可能多次投递)

    -- 自由 metadata (Stripe object snapshot / 操作人 / 备注)
    metadata          jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS subscription_events_user      ON billing.subscription_events (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscription_events_subscription ON billing.subscription_events (subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscription_events_type      ON billing.subscription_events (event_type, created_at DESC);

-- ─── 4. payment_orders (支付订单) ─────────────────────
-- 一行一笔支付意图. Stripe / WeChat Pay / Alipay / IAP 共表.
CREATE TABLE IF NOT EXISTS billing.payment_orders (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL,
    subscription_id     uuid REFERENCES billing.subscriptions(id),  -- 续费/升降级时关联; 单次充值 NULL

    order_type          text NOT NULL CHECK (order_type IN ('subscription', 'one_time', 'topup', 'refund')),
    provider            text NOT NULL CHECK (provider IN ('stripe', 'wechat_pay', 'alipay', 'apple_iap', 'google_play')),

    -- 金额
    amount              numeric(14, 2) NOT NULL CHECK (amount >= 0),
    currency            text NOT NULL CHECK (currency IN ('USD', 'CNY')),

    -- 状态
    status              text NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'succeeded', 'failed', 'refunded', 'canceled')),

    -- Provider 唯一键 (防重)
    provider_order_id   text NOT NULL,                           -- Stripe payment_intent / 微信 transaction_id / 苹果 transaction_id
    provider_event_id   text,                                    -- webhook event id 防回放

    -- 失败/退款详情
    failure_code        text,
    failure_message     text,
    refunded_at         timestamptz,
    refund_amount       numeric(14, 2) NOT NULL DEFAULT 0 CHECK (refund_amount >= 0),

    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at          timestamptz NOT NULL DEFAULT now(),
    paid_at             timestamptz,                             -- status='succeeded' 时填
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS payment_orders_provider_uniq
    ON billing.payment_orders (provider, provider_order_id);
CREATE INDEX IF NOT EXISTS payment_orders_user        ON billing.payment_orders (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS payment_orders_subscription ON billing.payment_orders (subscription_id) WHERE subscription_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS payment_orders_status      ON billing.payment_orders (status, created_at DESC);

-- ─── 5. plans seed — 4 档套餐 (00023) ────────────────────
-- benefits jsonb 字段对齐 services/identity/internal/billing/billing.go
-- 的 PlanLimits struct (snake_case key).
-- Stripe price ID 留 NULL — 当前 Stripe 集成走 env-driven, webhook 升级
-- 时再填 stripe_price_monthly/yearly.
INSERT INTO billing.plans
    (code, name, description, sort_order,
     price_currency, price_monthly, price_yearly, monthly_credits,
     benefits, status)
VALUES
    ('free',
     'Free',
     '免费体验, BYOK 自带 API key. 适合个人尝试.',
     0,
     'USD', 0, 0, 0,
     '{
       "hub_rpm": 60,
       "hub_tpm": 50000,
       "sandbox_daily": 10,
       "sandbox_concurrent": 1,
       "memory_quota": 100,
       "brain_projects": 3
     }'::jsonb,
     'active'),

    ('pro',
     'Pro',
     '个人专业版, 每月 10K 平台积分, BYOK 仍可用.',
     1,
     'USD', 19, 190, 10000,
     '{
       "hub_rpm": 600,
       "hub_tpm": 500000,
       "sandbox_daily": 100,
       "sandbox_concurrent": 5,
       "memory_quota": 5000,
       "brain_projects": 50
     }'::jsonb,
     'active'),

    ('team',
     'Team',
     '团队版, 每月 50K 积分, 高 RPM, 长上下文模型, 优先支持.',
     2,
     'USD', 99, 990, 50000,
     '{
       "hub_rpm": 6000,
       "hub_tpm": 5000000,
       "sandbox_daily": 1000,
       "sandbox_concurrent": 20,
       "memory_quota": 100000,
       "brain_projects": 1000
     }'::jsonb,
     'active'),

    ('enterprise',
     'Enterprise',
     '企业定制, 联系销售. 专属上下游, 私有部署, SLA 99.9%.',
     3,
     'USD', 0, 0, 0,
     '{
       "hub_rpm": 60000,
       "hub_tpm": 50000000,
       "sandbox_daily": 10000,
       "sandbox_concurrent": 100,
       "memory_quota": 10000000,
       "brain_projects": 100000
     }'::jsonb,
     'active')
ON CONFLICT (code) DO UPDATE SET
    name            = EXCLUDED.name,
    description     = EXCLUDED.description,
    sort_order      = EXCLUDED.sort_order,
    price_currency  = EXCLUDED.price_currency,
    price_monthly   = EXCLUDED.price_monthly,
    price_yearly    = EXCLUDED.price_yearly,
    monthly_credits = EXCLUDED.monthly_credits,
    benefits        = EXCLUDED.benefits,
    status          = EXCLUDED.status,
    updated_at      = now();

-- ─── 6. plan_quotas — 每档 plan 每个业务的月度配额字典 (00025) ──
-- 扣减优先级第一档: 每月免费额度 (quota), 配额走完才落到时效积分, 时效
-- 用完才到永久积分. 月度计数器, 扣到的 credits 蒸发不退还.
CREATE TABLE IF NOT EXISTS billing.plan_quotas (
    plan_id          uuid NOT NULL REFERENCES billing.plans(id) ON DELETE CASCADE,
    ref_type         text NOT NULL CHECK (ref_type IN ('chat_message', 'aigc_task')),
    monthly_amount   bigint NOT NULL DEFAULT 0 CHECK (monthly_amount >= 0),
    description      text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, ref_type)
);

-- ─── 7. user_quota_usage — 每用户每业务的当前周期用量 (00025) ──
-- period_start/period_end 锚定当前自然月 (UTC); cron 月初推进 + 重置
-- used_amount=0. 不存在的行视作 used=0 + period 落到当月.
CREATE TABLE IF NOT EXISTS identity.user_quota_usage (
    user_id          uuid NOT NULL,
    ref_type         text NOT NULL CHECK (ref_type IN ('chat_message', 'aigc_task')),
    period_start     timestamptz NOT NULL,
    period_end       timestamptz NOT NULL CHECK (period_end > period_start),
    used_amount      bigint NOT NULL DEFAULT 0 CHECK (used_amount >= 0),
    -- 周期内用过的最大 monthly_amount 缓存 — 用于 GET /v1/subscriptions/me
    -- 显示进度条; cron 重置时一并刷新成当时的 plan 配额.
    monthly_amount   bigint NOT NULL DEFAULT 0 CHECK (monthly_amount >= 0),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, ref_type)
);
CREATE INDEX IF NOT EXISTS user_quota_usage_period
    ON identity.user_quota_usage (period_end);

-- ─── 8. plan_quotas seed — 4 档 × 2 业务 = 8 行 (00025) ──
-- INSERT...SELECT 依赖 plans seed, 必须排在 plans seed 之后.
INSERT INTO billing.plan_quotas (plan_id, ref_type, monthly_amount, description)
SELECT id, 'chat_message',
    CASE code
        WHEN 'free'       THEN 0
        WHEN 'pro'        THEN 5000      -- ≈ 100K token / 月
        WHEN 'team'       THEN 30000     -- ≈ 600K token / 月
        WHEN 'enterprise' THEN 1000000   -- ≈ 20M token / 月 (定制可调)
    END,
    code || ' chat 每月免费配额'
FROM billing.plans
ON CONFLICT (plan_id, ref_type) DO UPDATE SET
    monthly_amount = EXCLUDED.monthly_amount,
    description    = EXCLUDED.description,
    updated_at     = now();

INSERT INTO billing.plan_quotas (plan_id, ref_type, monthly_amount, description)
SELECT id, 'aigc_task',
    CASE code
        WHEN 'free'       THEN 0
        WHEN 'pro'        THEN 1000      -- ≈ 10 张图 / 月 (按 100 credits/图)
        WHEN 'team'       THEN 5000
        WHEN 'enterprise' THEN 100000
    END,
    code || ' aigc 每月免费配额'
FROM billing.plans
ON CONFLICT (plan_id, ref_type) DO UPDATE SET
    monthly_amount = EXCLUDED.monthly_amount,
    description    = EXCLUDED.description,
    updated_at     = now();

-- ─── 9. billing.events 大宽表 (00024) ────────────────────
-- 单表 + jsonb 兼存 6 类 event (consume / refund / hold / settle /
-- release / subscription). event_id+occurred_at PK, sink 重投递时
-- ON CONFLICT DO NOTHING 去重 (NATS at-least-once).
CREATE TABLE IF NOT EXISTS billing.events (
    -- 来自 publisher Common 头
    event_id        UUID NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN (
                        'consume', 'refund', 'hold', 'settle',
                        'release', 'subscription'
                    )),
    user_id         UUID NOT NULL,
    idempotency_key TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL,
    env             TEXT NOT NULL,

    -- 跨 kind 公共维度 (可空, 视 kind 决定哪些非空)
    log_id          UUID,           -- consume/refund/settle 引用的 credit_logs
    hold_id         UUID,           -- hold/settle/release 引用的 credit_holds
    amount          BIGINT,         -- 积分量 (正数, 单位见 ref_type)
    ref_type        TEXT,           -- chat_message / agent_step / aigc_task / refund / ...
    ref_id          TEXT,
    model_code      TEXT,           -- 模型分布 dashboard 用
    provider_code   TEXT,
    upstream_usd    DOUBLE PRECISION,  -- 毛利计算 (consume 时填)
    upstream_cny    DOUBLE PRECISION,

    -- subscription 专用
    subscription_id UUID,
    event_type      TEXT,           -- created / activated / upgraded / downgraded / canceled / ...
    plan_code       TEXT,
    old_plan_code   TEXT,
    amount_cents    BIGINT,
    currency        TEXT,
    source          TEXT,           -- stripe / wechat / alipay / iap

    -- settle/release 专用
    actual          BIGINT,         -- settle 实扣
    hold_delta      BIGINT,         -- settle 多预扣 (退还给余额)
    refund_of_log_id UUID,          -- refund 引用原 log
    expires_at      TIMESTAMPTZ,    -- hold 的预期过期
    reason          TEXT,           -- release: user_cancel / expired

    -- 原始 payload (调试 + 未来字段扩展前向兼容)
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,

    inserted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, occurred_at)
)
PARTITION BY RANGE (occurred_at);

-- 索引建在父表上 (PG 14+ 自动下传到各分区).
CREATE INDEX IF NOT EXISTS events_user_time_idx
    ON billing.events (user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS events_kind_time_idx
    ON billing.events (kind, occurred_at DESC);
-- model 维度 dashboard 高频查 — 给 consume + 模型代码做局部索引.
CREATE INDEX IF NOT EXISTS events_model_time_idx
    ON billing.events (model_code, occurred_at DESC)
    WHERE kind = 'consume';

-- +goose StatementEnd

-- ─── billing.events 起始分区 (动态化) ────────────────────
-- 原 00024 硬编码 202606/202607/202608 三个分区, 新部署当月 INSERT 会
-- 失败. 基线改为按部署时间动态建分区: 从当前月份起向后建 4 个月 (含
-- 当月) 的月度分区, 命名沿用 events_yyyymm 风格.
--
-- 注意: 运维仍需按原有 cron 机制定期提前创建后续月份分区 (老分区到期
-- detach/drop 不影响主表), 本 DO 块只保证部署当月起 4 个月内可写.
-- +goose StatementBegin

DO $$
DECLARE
    i         int;
    m_start   date;
    m_end     date;
    part_name text;
BEGIN
    FOR i IN 0..3 LOOP
        m_start   := (date_trunc('month', now()) + (i || ' month')::interval)::date;
        m_end     := (m_start + interval '1 month')::date;
        part_name := 'events_' || to_char(m_start, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS billing.%I PARTITION OF billing.events FOR VALUES FROM (%L) TO (%L)',
            part_name, m_start, m_end
        );
    END LOOP;
END $$;

-- +goose StatementEnd

-- ─── 10. trial_attempts — 试用资格防刷三元组黑名单 (00027) ──
-- 规则 (默认值, 可在 internal/billing/trial.go 调):
--   1. 同 user_id 历史已 succeeded=true → 拒 (一辈子只能一次)
--   2. 同 device_fp 已被 ≥ 3 个不同 user_id succeeded → 拒
--   3. 同 ip 24h 内 ≥ 5 次申请 (任何 succeeded) → 拒
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS billing.trial_attempts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    device_fp       text NOT NULL DEFAULT '',
    ip              inet,
    succeeded       boolean NOT NULL DEFAULT false,
    reject_reason   text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS trial_attempts_user      ON billing.trial_attempts (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS trial_attempts_device    ON billing.trial_attempts (device_fp, created_at DESC) WHERE device_fp <> '';
CREATE INDEX IF NOT EXISTS trial_attempts_ip        ON billing.trial_attempts (ip, created_at DESC) WHERE ip IS NOT NULL;

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- 营销: 优惠券 + 邀请奖励 (00028) 与公告 inbox (00031)
-- ═══════════════════════════════════════════════════════════════════
-- +goose StatementBegin

-- ─── 1. coupons (券模板) ──────────────────────────
-- kind 决定执行逻辑: amount_off (立减 N 分) / percent_off (立减 N%,
-- 上限 max_amount_cents) / credits_grant (发 N 积分) / trial_extend
-- (试用期延长 N 天).
CREATE TABLE IF NOT EXISTS billing.coupons (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code              text UNIQUE NOT NULL,
    kind              text NOT NULL CHECK (kind IN
                          ('amount_off', 'percent_off', 'credits_grant', 'trial_extend')),

    -- value 通用字段 — 含义按 kind 解释:
    --   amount_off    → cents 数额
    --   percent_off   → 百分比 0-100 (整数, 5 表示 5%)
    --   credits_grant → 积分数
    --   trial_extend  → 天数
    value             bigint NOT NULL CHECK (value > 0),
    -- percent_off 专用 cap (无 cap 设很大值)
    max_amount_cents  bigint NOT NULL DEFAULT 0 CHECK (max_amount_cents >= 0),

    -- 适用范围
    plan_codes        text[] NOT NULL DEFAULT '{}',  -- 空数组 = 不限 plan
    currency          text,                          -- amount_off 时必填; 其他类型 NULL
    once_per_user     boolean NOT NULL DEFAULT true, -- 同 user 只能用一次
    max_total_uses    bigint NOT NULL DEFAULT 0 CHECK (max_total_uses >= 0), -- 0=不限

    -- 时效
    valid_from        timestamptz NOT NULL DEFAULT now(),
    valid_until       timestamptz,                   -- NULL = 永不过期

    -- 状态
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'archived')),

    -- 审计
    description       text NOT NULL DEFAULT '',
    created_by        uuid REFERENCES identity.users(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS coupons_status      ON billing.coupons (status, valid_until);
CREATE INDEX IF NOT EXISTS coupons_kind        ON billing.coupons (kind);

-- ─── 2. coupon_redemptions ─────────────────────────
-- 一行一兑换. once_per_user=true 时由应用层挡; 表本身允许多次.
CREATE TABLE IF NOT EXISTS billing.coupon_redemptions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id       uuid NOT NULL REFERENCES billing.coupons(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL,
    -- 关联订单 (amount_off / percent_off 减价时关联到具体支付单)
    payment_order_id uuid REFERENCES billing.payment_orders(id) ON DELETE SET NULL,
    -- 关联订阅 (trial_extend 时记到订阅)
    subscription_id uuid REFERENCES billing.subscriptions(id) ON DELETE SET NULL,
    -- 实际生效金额 (amount_off 直接 = value; percent_off 算出 cents; credits_grant 0)
    discount_cents  bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    -- 关联 credit_logs.id (credits_grant 时写; 用于回溯)
    credit_log_id   uuid,
    -- 关联 trial extend 天数 (trial_extend 时写)
    extra_days      int NOT NULL DEFAULT 0,
    redeemed_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS redemptions_user      ON billing.coupon_redemptions (user_id, redeemed_at DESC);
CREATE INDEX IF NOT EXISTS redemptions_coupon    ON billing.coupon_redemptions (coupon_id, redeemed_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS redemptions_unique_per_user
    ON billing.coupon_redemptions (coupon_id, user_id);

-- ─── 3. referrals (邀请关系) ─────────────────────
-- (inviter_user_id, invitee_user_id) 唯一. invite_code 是邀请人的长期
-- 邀请码 (一人一码), 多人共享同 code.
CREATE TABLE IF NOT EXISTS billing.referrals (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inviter_user_id uuid NOT NULL,
    invitee_user_id uuid NOT NULL,
    invite_code     text NOT NULL,

    -- 防刷三元组
    invitee_device_fp text NOT NULL DEFAULT '',
    invitee_ip      inet,

    -- 状态机: pending → rewarded / reverted
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'rewarded', 'reverted')),

    -- 奖励落账 (审计)
    inviter_credit_log_id uuid,
    invitee_credit_log_id uuid,

    rewarded_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- 一对邀请人/被邀请人只能记一条
CREATE UNIQUE INDEX IF NOT EXISTS referrals_pair_unique
    ON billing.referrals (inviter_user_id, invitee_user_id);
CREATE INDEX IF NOT EXISTS referrals_inviter ON billing.referrals (inviter_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS referrals_invitee ON billing.referrals (invitee_user_id);
CREATE INDEX IF NOT EXISTS referrals_code    ON billing.referrals (invite_code);
CREATE INDEX IF NOT EXISTS referrals_device  ON billing.referrals (invitee_device_fp) WHERE invitee_device_fp <> '';
CREATE INDEX IF NOT EXISTS referrals_ip      ON billing.referrals (invitee_ip) WHERE invitee_ip IS NOT NULL;

-- ─── 4. seed 4 类券模板 (运营 demo, 可在后台增删) ─
INSERT INTO billing.coupons (code, kind, value, max_amount_cents, currency, once_per_user, valid_until, description)
VALUES
  ('NEWUSER20', 'percent_off',   20,  10000, 'CNY', true, NULL, '新人 20% 折扣 (上限 ¥100)'),
  ('GIFT500',   'credits_grant', 500, 0,     NULL,  true, NULL, '500 积分礼包'),
  ('OFF10',     'amount_off',    1000, 0,    'CNY', true, NULL, '立减 ¥10'),
  ('TRIAL14',   'trial_extend',  14,  0,     NULL,  true, NULL, '延长 14 天试用期')
ON CONFLICT (code) DO NOTHING;

-- ─── 5. 公告 / 通知 inbox (00031) ──────────────────────
-- admin 在后台发布公告, 客户端 NotificationBell 拉取 + 显示未读角标.
-- 读态服务端 per-user 入库 (跨设备同步). min/max_app_version 控制公告
-- 对哪些客户端可见; published=false 为草稿; expires_at 过期后不返回.
CREATE TABLE IF NOT EXISTS identity.announcements (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    level            text NOT NULL DEFAULT 'info',   -- info | warning | error
    title            text NOT NULL,
    body             text NOT NULL DEFAULT '',
    body_zh          text NOT NULL DEFAULT '',
    url              text NOT NULL DEFAULT '',
    min_app_version  text NOT NULL DEFAULT '',
    max_app_version  text NOT NULL DEFAULT '',
    published        boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz
);
CREATE INDEX IF NOT EXISTS announcements_active
    ON identity.announcements (published, created_at DESC);

-- per-user 读态: 一行表示该用户读过该公告. 无行 = 未读.
CREATE TABLE IF NOT EXISTS identity.announcement_reads (
    user_id          uuid NOT NULL,
    announcement_id  uuid NOT NULL REFERENCES identity.announcements (id) ON DELETE CASCADE,
    read_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, announcement_id)
);
CREATE INDEX IF NOT EXISTS announcement_reads_user
    ON identity.announcement_reads (user_id);

-- +goose StatementEnd

-- ═══════════════════════════════════════════════════════════════════
-- Down — 删除全部对象与 schema.
-- billing.events 是分区父表, DROP 父表自动连带全部月度分区, 无需逐个
-- DROP 分区. 索引随表删除, 不单独 DROP.
-- ═══════════════════════════════════════════════════════════════════
-- +goose Down
-- +goose StatementBegin

-- 营销 / 公告
DROP TABLE IF EXISTS identity.announcement_reads;
DROP TABLE IF EXISTS identity.announcements;
DROP TABLE IF EXISTS billing.referrals;
DROP TABLE IF EXISTS billing.coupon_redemptions;
DROP TABLE IF EXISTS billing.coupons;

-- billing
DROP TABLE IF EXISTS billing.trial_attempts;
DROP TABLE IF EXISTS billing.events;            -- 分区父表, 连带全部分区
DROP TABLE IF EXISTS identity.user_quota_usage;
DROP TABLE IF EXISTS billing.plan_quotas;
DROP TABLE IF EXISTS billing.payment_orders;
DROP TABLE IF EXISTS billing.subscription_events;
DROP TABLE IF EXISTS billing.subscriptions;
DROP TABLE IF EXISTS billing.plans;

-- BYOK
DROP TABLE IF EXISTS identity.user_api_keys;

-- credits
DROP TABLE IF EXISTS identity.user_storage;
DROP TABLE IF EXISTS identity.credit_recharge_options;
DROP TABLE IF EXISTS identity.credit_holds;
DROP TABLE IF EXISTS identity.credit_logs;
DROP TABLE IF EXISTS identity.credit_packages;
DROP TABLE IF EXISTS identity.user_credits;

-- oauth / tokens / 第三方身份
DROP TABLE IF EXISTS identity.security_events;
DROP TABLE IF EXISTS identity.oauth_authorization_codes;
DROP TABLE IF EXISTS identity.oauth_clients;
DROP TABLE IF EXISTS identity.activity_events;
DROP TABLE IF EXISTS identity.api_tokens;
DROP TABLE IF EXISTS identity.oauth_states;
DROP TABLE IF EXISTS identity.mp_subscriptions;
DROP TABLE IF EXISTS identity.identity_providers;

-- 系统配置
DROP TABLE IF EXISTS biumind_system.config;

-- 核心用户
DROP TABLE IF EXISTS identity.password_resets;
DROP TABLE IF EXISTS identity.email_verifications;
DROP TABLE IF EXISTS identity.virtual_keys;
DROP TABLE IF EXISTS identity.refresh_tokens;
DROP TABLE IF EXISTS identity.users;

-- RBAC / audit
DROP TABLE IF EXISTS identity.role_permissions;
DROP TABLE IF EXISTS identity.permissions;
DROP TABLE IF EXISTS identity.roles;
DROP TABLE IF EXISTS audit.events;

-- schema
DROP SCHEMA IF EXISTS audit;
DROP SCHEMA IF EXISTS billing;
DROP SCHEMA IF EXISTS biumind_system;
DROP SCHEMA IF EXISTS identity;

-- +goose StatementEnd
