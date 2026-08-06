-- +goose Up
-- +goose StatementBegin

-- ─── RBAC permissions for model_relay admin (MC-M1.4) ──────────────
--
-- Adds 6 permissions for the new model configuration backend. These
-- are intentionally narrow — broader concepts already exist:
--
--   * providers:read / providers:write / providers:exec  ← from
--     00003_rbac.sql ll. 99-101, originally seeded for "global LLM
--     provider" admin. We reuse them rather than create
--     model_providers:*. The model_relay.providers table IS what those
--     permissions guard.
--
--   * limits:read / limits:write are user-level quotas in identity, NOT
--     channel rpm_limit / tpm_limit in model_relay. Those are gated by
--     models:write (channel CRUD lives under model admin).
--
-- New permissions (6):
--   models:read              List/get models, channels, model_groups, pricing,
--                            fx_rates (everything an admin needs to *see*).
--   models:write             Create/edit/delete models, channels, groups,
--                            run sync-upstream, run channel test.
--   model_credentials:read   List credentials (returns only key_preview,
--                            never plaintext).
--   model_credentials:write  Create/edit/delete credentials, run cred test.
--   pricing:write            Edit per-model pricing rows. Read is folded
--                            into models:read.
--   fx_rates:write           Edit fx rates. Read is folded into models:read.
--
-- Why not finer (`channels:write`, `groups:write`, etc.): six is the
-- minimum that meaningfully separates duties (read vs write, sensitive
-- vs scrubbed) for an MVP. Splitting further now would require admin
-- UI gating logic with no real persona to gate against (single-operator
-- product). Phase 3 (Org/Team multi-tenant) is the natural time to fan
-- out into per-resource permissions.

INSERT INTO identity.permissions (name, resource, action, scope, description) VALUES
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

-- ─── 角色 → 权限映射 ─────────────────────────────────────────────
-- superadmin: 通过 '*' 通配自动包含，不需要显式行
-- admin: 全部 6 个新权限 + 已有 providers:*（00003 中已 grant）
-- support: models:read + model_credentials:read（仅脱敏读，不能改任何东西）
-- finance: pricing:write 用不上（财务不直接改单价），仅 models:read
-- ops: models:read（监控渠道健康用）
-- viewer: models:read

INSERT INTO identity.role_permissions (role_name, permission_name) VALUES
  ('admin',   'models:read'),
  ('admin',   'models:write'),
  ('admin',   'model_credentials:read'),
  ('admin',   'model_credentials:write'),
  ('admin',   'pricing:write'),
  ('admin',   'fx_rates:write'),
  ('support', 'models:read'),
  ('support', 'model_credentials:read'),
  ('finance', 'models:read'),
  ('ops',     'models:read'),
  ('viewer',  'models:read')
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM identity.role_permissions
    WHERE permission_name IN (
        'models:read', 'models:write',
        'model_credentials:read', 'model_credentials:write',
        'pricing:write', 'fx_rates:write'
    );

DELETE FROM identity.permissions
    WHERE name IN (
        'models:read', 'models:write',
        'model_credentials:read', 'model_credentials:write',
        'pricing:write', 'fx_rates:write'
    );

-- +goose StatementEnd
