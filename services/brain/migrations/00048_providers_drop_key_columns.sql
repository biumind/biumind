-- BYOK P3 收尾: 真正删除 chat.providers 的 key 存储 + fetch_mode + internal 三列。
--
-- 00047 故意留列 (no-op) 让 cmd/migrate-byok 有窗口把 key 数据迁到 identity。
-- 迁移已在测试环境验证 (scanned=1 migrated=1, relay byok_success is_byok=true —
-- 迁过去的 key 端到端可用)。现在删列零损失: brain P3 代码本就不引用这三列,
-- key 数据已在 identity.user_api_keys。
--
-- 删后 cmd/migrate-byok 的列存在守护返 false → 后续部署 migrate-byok 自动 no-op
-- 退出 (已加守护, 不报错)。
--
-- 顺序: providers_internal_requires_client CHECK 依赖 fetch_mode + internal,
-- 必须先 drop 约束再 drop 列。

-- +goose Up
ALTER TABLE chat.providers DROP CONSTRAINT IF EXISTS providers_internal_requires_client;

ALTER TABLE chat.providers DROP COLUMN IF EXISTS key_vaults_encrypted;
ALTER TABLE chat.providers DROP COLUMN IF EXISTS fetch_mode;
ALTER TABLE chat.providers DROP COLUMN IF EXISTS internal;

-- +goose Down
-- 反向: 恢复三列 + 约束 (回滚 schema 用)。key 数据不会从 identity 自动迁回 —
-- 回滚到 P3 前需 identity → brain 反向迁移或从备份恢复。
ALTER TABLE chat.providers
    ADD COLUMN IF NOT EXISTS key_vaults_encrypted bytea;

ALTER TABLE chat.providers
    ADD COLUMN IF NOT EXISTS fetch_mode text NOT NULL DEFAULT 'server'
        CHECK (fetch_mode IN ('server','client'));

ALTER TABLE chat.providers
    ADD COLUMN IF NOT EXISTS internal boolean NOT NULL DEFAULT false;

ALTER TABLE chat.providers
    ADD CONSTRAINT providers_internal_requires_client
        CHECK (NOT internal OR fetch_mode = 'client');
