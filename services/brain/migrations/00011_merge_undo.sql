-- ============================================================================
-- 00011_merge_undo.sql — wiki merge undo（撤销页面合并）快照关联
-- （BiuMind-Agent-Experience-Design §6.1 P3-a）
--
-- page_revisions.merge_id：同一次 MergePages 的 canonical + duplicate 两条
-- 写前快照共享一个 merge_id，undo（Store.UnmergePages）据此精确找回两页
-- merge 前态，替代 ListAgentRunChanges 的 frontmatter 启发式推断。老数据
-- merge_id 为 NULL → undo 诚实降级 unavailable，不猜。
--
-- 无 files.objects 引用，不涉及 FGC 孤儿扫描清单。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.page_revisions
    ADD COLUMN IF NOT EXISTS merge_id uuid;

-- undo / 审计查询 = 按 merge_id 反查成对快照；partial 索引不为人工作业
-- （merge_id IS NULL 占绝大多数）付索引维护成本。
CREATE INDEX IF NOT EXISTS page_revisions_merge_idx
    ON brain.page_revisions (merge_id) WHERE merge_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP INDEX IF EXISTS brain.page_revisions_merge_idx;
ALTER TABLE brain.page_revisions DROP COLUMN IF EXISTS merge_id;
-- +goose StatementEnd
