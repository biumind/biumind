-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00036 — client-side BYOK key 统一加密存 identity (语义重定义)
--
-- 原 00035: client-side (is_client_side=true) key 仅存客户端 keychain,
-- identity 仅空占位行 (encrypted_value/nonce 空 bytea). 现: key 统一加密
-- 存 identity, is_client_side 语义改为「需本机出口」(relay 连不到的上游,
-- 如内网 proxy), 桌面 daemon 取 key 本机直连.
--
-- schema 不变 (is_client_side 列 + 00035 的 4 partial unique index 保留).
-- 仅清理: DELETE 旧 client-side 空占位行 (encrypted_value 空, 新 Upsert
-- 要求明文, 旧行无效). 用户需重配 client-side 凭据 (key 上传 identity).
-- 无外键引用 user_api_keys.id, DELETE 安全.
-- ═══════════════════════════════════════════════════════════════════

DELETE FROM identity.user_api_keys
WHERE is_client_side = true AND octet_length(encrypted_value) = 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 不可逆: 旧空占位行已删 (其 key 仅存客户端 keychain, 服务端从未持有明文,
-- 无法重建). Down 为空操作.

-- +goose StatementEnd
