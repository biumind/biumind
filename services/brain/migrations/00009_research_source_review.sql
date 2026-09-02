-- ============================================================================
-- 00009_research_source_review.sql — Deep Research 任务溯源到审阅项
--
-- review → Deep Research 一键入口（reviews_page「研究」动作）创建任务时
-- 携带 source_review_id；orchestrator 在 savePage 成功后自动 resolve 该
-- 审阅项（参考 page_merger 合并后自动 resolve dedup 的既有做法）。
--
-- ON DELETE SET NULL：审阅项被清理时任务保留，溯源字段置空即可。
-- 无 files.objects 引用，不涉及 FGC 孤儿扫描清单。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.research_tasks
    ADD COLUMN IF NOT EXISTS source_review_id uuid
        REFERENCES brain.review_items(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
ALTER TABLE brain.research_tasks
    DROP COLUMN IF EXISTS source_review_id;
-- +goose StatementEnd
