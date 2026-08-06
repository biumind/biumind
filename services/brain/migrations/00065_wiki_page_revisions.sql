-- +goose Up
-- +goose StatementBegin
-- Wiki 页版本历史（镜像 note_revisions 00059，适配多 block 正文）：
--   edit    = 5 写入口（UpdatePage/SoftDeletePage/CreateBlock/UpdateBlock/SoftDeleteBlock）
--             写前旧态全量快照（页 frontmatter + 全部 live blocks 序列化为 blocks_json）。
--             5 分钟窗口合并 + PrunePageRevisions 定期清理。
--   restore = 恢复前自动备份当前态（永久保留，不参与窗口合并与 Prune）。
-- wiki store 无 user 概念（project-scoped，ownership 在 api ownsProject），
-- 故用 actor_id 记录操作者，不带 user_id（与 note_revisions 的差异）。
CREATE TABLE brain.page_revisions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id        uuid NOT NULL REFERENCES brain.pages(id) ON DELETE CASCADE,
    project_id     uuid NOT NULL,
    actor_id       text  NOT NULL DEFAULT '',
    title          text  NOT NULL DEFAULT '',
    frontmatter    jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 写前全部 live blocks 序列化（[]*Block），restore 时反序列化对账回写。
    blocks_json    jsonb NOT NULL DEFAULT '[]'::jsonb,
    change_type    text  NOT NULL CHECK (change_type IN ('edit', 'restore')),
    -- restore 自动备份固定写「恢复前自动备份」；edit 快照为 NULL。
    change_summary text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX page_revisions_page_created_idx
    ON brain.page_revisions (page_id, created_at DESC);

CREATE INDEX page_revisions_project_idx
    ON brain.page_revisions (project_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.page_revisions;
