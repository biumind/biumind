-- BYOK P3: brain 彻底去 key —— 列删除**推迟**到迁移完成后。
--
-- brain P3 代码 (store.go scan/Create/Update) 已不再 select / insert 这三列
-- (key_vaults_encrypted / fetch_mode / internal), 故列留着对 P3 运行无害:
-- 新行用列默认值 (fetch_mode='server', internal=false, key_vaults=NULL),
-- 老行数据原地不动。
--
-- 为什么不在此 migration 直接 DROP:
--   Jenkins push → 自动部署 → brain 重启 → goose 跑本 migration。若此时
--   cmd/migrate-byok 还没把 key_vaults_encrypted 的数据迁到 identity,
--   列一删 = 用户 key 永久丢失。迁移脚本需 build-host DB 访问 + 两把
--   master key, 与自动部署存在竞态。
--   → 保守: 列先留, 迁移脚本任何时候跑都安全 (幂等 ON CONFLICT DO
--   NOTHING)。迁移确认完成后, 另起一条 migration (00048+) DROP 这三列
--   + providers_internal_requires_client 约束。
--
-- 设计: docs/BiuMind-BYOK-Unification-Design.md §7 P3。

-- +goose Up
-- Intentionally a no-op: columns retained until the byok data migration
-- (cmd/migrate-byok) has been confirmed. See comment above.
SELECT 1;

-- +goose Down
SELECT 1;
