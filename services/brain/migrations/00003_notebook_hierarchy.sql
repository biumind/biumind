-- ============================================================================
-- 00003_notebook_hierarchy.sql — 笔记本多级目录（parent_id 树）
--
-- baseline 的 note_notebooks 是单层（「不做树，组织靠标签」），且活本名在
-- 全用户范围唯一（note_notebooks_user_name_alive）。多级目录落地后：
--
--   1. parent_id 自引用成树（ON DELETE SET NULL 兜底；正常路径软删，
--      store 层 SoftDeleteNotebook 会先把子本 parent_id 上移一层）。
--   2. 名字唯一性收敛为「同一父目录下不区分大小写唯一」——不同目录允许
--      同名（工作/读书 与 生活/读书）。根级（parent_id IS NULL）靠
--      NULLS NOT DISTINCT 保持互相唯一（PG16，默认 NULLS DISTINCT 会让
--      NULL parent 的行永远判不冲突）。
--   3. 层级上限 5 层由 store 层在写路径校验（递归 CTE），DB 不加
--      CHECK/触发器 —— 与 events/软删一致，规则集中在 Go 侧演进。
--
-- 存量行 parent_id 全 NULL，等价于全部挂在根，行为与迁移前一致。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.note_notebooks
    ADD COLUMN IF NOT EXISTS parent_id uuid NULL
        REFERENCES brain.note_notebooks(id) ON DELETE SET NULL;

-- 按父目录列子项 / 递归 CTE 走 parent 链
CREATE INDEX IF NOT EXISTS note_notebooks_user_parent_idx
    ON brain.note_notebooks (user_id, parent_id);

-- 名字唯一性：全用户范围 → 同一父目录
DROP INDEX IF EXISTS brain.note_notebooks_user_name_alive;

CREATE UNIQUE INDEX IF NOT EXISTS note_notebooks_user_parent_name_alive
    ON brain.note_notebooks (user_id, parent_id, lower(name)) NULLS NOT DISTINCT
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down

-- 注意：若迁移后不同父目录下已出现同名活本，恢复全用户唯一索引会失败，
-- 需先人工改名/清理再回滚。
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.note_notebooks_user_parent_name_alive;
DROP INDEX IF EXISTS brain.note_notebooks_user_parent_idx;

CREATE UNIQUE INDEX IF NOT EXISTS note_notebooks_user_name_alive
    ON brain.note_notebooks (user_id, lower(name))
    WHERE deleted_at IS NULL;

ALTER TABLE brain.note_notebooks DROP COLUMN IF EXISTS parent_id;
-- +goose StatementEnd
