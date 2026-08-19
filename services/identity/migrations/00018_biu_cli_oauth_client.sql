-- ═══════════════════════════════════════════════════════════════════
-- 00018 — 预注册 biu-cli OAuth 2.1 public client (BiuMind-CLI-OAuth-Login-Plan D2)
--
-- biu CLI 内置 client_id="biu-cli", 但 oauth_clients.client_id 是 uuid 主键
-- (DCR 设计), 可读字符串 id 放不下 — 这里加 client_alias 别名列, authorize /
-- token 端点按 "uuid 主键 → alias" 顺序解析 client_id (见
-- internal/api/oauth_authorize.go resolveOAuthClient).
--
-- 种子行: public client (token_endpoint_auth_method='none', 无 secret),
-- PKCE S256 由 /oauth/authorize 端点对**所有** client 强制 (无 per-client
-- 开关列); redirect_uris 注册 loopback 基址, 实际端口由 RFC 8252 §7.3
-- 端口放宽匹配 (matchRedirectURI).
--
-- 编号说明: 新 migration 必须 > cmd/identity/main.go 的 baselineMax (17),
-- 否则 legacy 库 (表在但 goose 表不在) baseline 时会被静默标记已跑.
-- ═══════════════════════════════════════════════════════════════════

-- +goose Up

-- +goose StatementBegin

ALTER TABLE identity.oauth_clients ADD COLUMN IF NOT EXISTS client_alias text;

CREATE UNIQUE INDEX IF NOT EXISTS oauth_clients_client_alias_uidx
    ON identity.oauth_clients (client_alias)
    WHERE client_alias IS NOT NULL;

-- 固定 uuid (尾段 6269752d636c69 = "biu-cli" 的 hex) 保证全环境同一行,
-- ON CONFLICT 幂等.
INSERT INTO identity.oauth_clients
    (client_id, client_alias, client_name, redirect_uris,
     grant_types, response_types, token_endpoint_auth_method, scope)
VALUES
    ('00000000-0000-4000-8000-6269752d636c69', 'biu-cli', 'BiuMind CLI',
     ARRAY['http://127.0.0.1/callback', 'http://localhost/callback']::text[],
     ARRAY['authorization_code', 'refresh_token']::text[],
     ARRAY['code']::text[],
     'none', '')
ON CONFLICT (client_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin

DELETE FROM identity.oauth_clients
 WHERE client_id = '00000000-0000-4000-8000-6269752d636c69';

DROP INDEX IF EXISTS identity.oauth_clients_client_alias_uidx;

ALTER TABLE identity.oauth_clients DROP COLUMN IF EXISTS client_alias;

-- +goose StatementEnd
