-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- P4.S3.4 — aigc.tasks 解 FK 到 aigc.models, 准备段 3.6 删 aigc.models
--
-- 段 3.4 不直接 DROP TABLE — 因为 aigc 服务的 store/models.go 还在
-- SELECT aigc.providers/models. 真正 DROP 留给段 3.6 (aigc 服务代码
-- 瘦身后).  本 migration 仅做两件事:
--   1. ALTER TABLE aigc.tasks DROP CONSTRAINT tasks_model_code_fkey
--      → model_code 字段保留为 text 兜底回溯, 不再做完整性约束
--   2. 加注释提醒后人 aigc.{providers,models} 已被 mirror 到 model_relay
--
-- 前置: 必须先跑 services/model-relay/scripts/migrate_aigc_dictionary.sql
-- (P4.S3.3) 把 aigc 字典数据 backfill 到 model_relay; 否则段 3.6 删表时
-- 历史 aigc.tasks.model_code 会指向不存在的字典.
--
-- 验证: SELECT DISTINCT model_code FROM aigc.tasks WHERE deleted_at IS
--       NULL → 每个值都应该能在 model_relay.models 找到对应行 (此次实
--       测 3/3 全部对得上).
--
-- 备份: 删表前 (段 3.6) 必须 pg_dump 整个 aigc.{providers,models} 到本
-- 地 ~/biumind-backups/, 保留 72h. 见 BiuMind-Model-Config-Dev-Plan §10
-- 不变量 I10.
-- ═══════════════════════════════════════════════════════════════════

ALTER TABLE aigc.tasks
    DROP CONSTRAINT IF EXISTS tasks_model_code_fkey;

COMMENT ON COLUMN aigc.tasks.model_code IS
    'P4.S3.4: 不再 FK 到 aigc.models. 段 3.6 删 aigc.models 后此字段
    仍保留作文本兜底; 当前真实模型字典在 model_relay.models, 通过 code
    1:1 对应即可 join.';

COMMENT ON TABLE aigc.providers IS
    'DEPRECATED — Phase 4 段 3 cutover. 数据已 backfill 到 model_relay.providers
    (见 services/model-relay/scripts/migrate_aigc_dictionary.sql). 本表
    将在段 3.6 (aigc 服务代码瘦身) 完成后 DROP. 不要再写入新行.';

COMMENT ON TABLE aigc.models IS
    'DEPRECATED — Phase 4 段 3 cutover. 数据已 backfill 到 model_relay.models
    (mode 字段映射: image → image_generation, video → video_generation,
    digital_human / hotparse 同名). 段 3.6 后 DROP. 不要再写入新行.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 注意: 重新加 FK 仅在 aigc.models 仍存在时有意义. 段 3.6 之后此 Down
-- 不可逆 (aigc.models 已 DROP). 这里仅恢复 FK, 不重建表.
ALTER TABLE aigc.tasks
    ADD CONSTRAINT tasks_model_code_fkey
    FOREIGN KEY (model_code) REFERENCES aigc.models(code);

COMMENT ON COLUMN aigc.tasks.model_code IS NULL;
COMMENT ON TABLE aigc.providers IS NULL;
COMMENT ON TABLE aigc.models IS NULL;

-- +goose StatementEnd
