-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- P4.S3.6 — DROP TABLE aigc.{providers,models}
--
-- 前置 (全部已完成):
--   ✅ P4.S3.3: 数据 backfill 到 model_relay.{providers,models,pricing_rules}
--   ✅ P4.S3.4: aigc.tasks FK to aigc.models 已解除
--   ✅ S3.6 上半段: aigc credentials Resolver 切到读 model_relay.providers
--   ✅ S3.6 上半段: aigc/internal/adminapi 包整体删除
--   ✅ S3.6 上半段: aigc/internal/store/{models,providers}.go 不再被引用
--
-- 删表前已 pg_dump:
--   ~/biumind-backups/aigc-dictionary-20260607-210723.sql (49 行 INSERT)
--   保留 72h, 真出问题可手动 \i 还原.
--
-- aigc.tasks.model_code 留作文本兜底, 真实模型字典在 model_relay.models
-- (1:1 join via code).
-- ═══════════════════════════════════════════════════════════════════

DROP TABLE IF EXISTS aigc.models CASCADE;
DROP TABLE IF EXISTS aigc.providers CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 不可逆 — Down 仅起记号作用, 真要回滚得从 ~/biumind-backups/ 还原
-- aigc.{providers,models} 全部数据 + 重建 store/{models,providers}.go +
-- adminapi/ 包. 现实场景下不会触发.
SELECT 'irrecoverable: see commit message for restore steps';

-- +goose StatementEnd
