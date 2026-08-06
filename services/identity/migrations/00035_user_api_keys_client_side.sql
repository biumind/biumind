-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00035 — BYOK client-side 标记 (P5 of docs/BiuMind-BYOK-Unification-Design.md)
--
-- client-side BYOK: provider 元数据存 identity, key 仅存客户端 keychain
-- (flutter_secure_storage). is_client_side=true 的行 encrypted_value/nonce
-- 空占位, relay 永不解析 —— store.GetDecrypted/MatchCustomByModel 的 WHERE
-- 已加 is_client_side = false 过滤, relay 走 internalapi 只能见 server BYOK.
--
-- 方案 I: client-side 保留标准 provider slug (anthropic/openai/...) + custom.
-- 同一 (user, provider) 可同时存在 server 行 + client-side 行 → 把 00033 的
-- 两个 partial unique index 各按 is_client_side 拆成两条.
-- ═══════════════════════════════════════════════════════════════════

-- 1. 新列 (旧数据默认 false = server BYOK, 行为不变)
ALTER TABLE identity.user_api_keys
    ADD COLUMN IF NOT EXISTS is_client_side boolean NOT NULL DEFAULT false;

-- 2. 重拆 00033 的两个 partial unique index → 按 is_client_side 各拆 2 条
DROP INDEX IF EXISTS identity.identity_user_api_keys_user_provider_std;
CREATE UNIQUE INDEX identity_user_api_keys_user_provider_std_server
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom' AND is_client_side = false;
CREATE UNIQUE INDEX identity_user_api_keys_user_provider_std_client
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom' AND is_client_side = true;

DROP INDEX IF EXISTS identity.identity_user_api_keys_user_baseurl_custom;
CREATE UNIQUE INDEX identity_user_api_keys_user_baseurl_custom_server
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom' AND is_client_side = false;
CREATE UNIQUE INDEX identity_user_api_keys_user_baseurl_custom_client
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom' AND is_client_side = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 反向. down 前需确认无 is_client_side=true 行, 否则重建原 index 会因
-- 同 (user, provider) 两行撞 unique 失败.

DROP INDEX IF EXISTS identity.identity_user_api_keys_user_provider_std_client;
DROP INDEX IF EXISTS identity.identity_user_api_keys_user_provider_std_server;
DROP INDEX IF EXISTS identity.identity_user_api_keys_user_baseurl_custom_client;
DROP INDEX IF EXISTS identity.identity_user_api_keys_user_baseurl_custom_server;

-- 恢复 00033 原始 2 条 partial unique index
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_provider_std
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom';
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_baseurl_custom
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom';

ALTER TABLE identity.user_api_keys DROP COLUMN IF EXISTS is_client_side;

-- +goose StatementEnd
