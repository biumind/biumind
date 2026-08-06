-- +goose Up
-- +goose StatementBegin
-- Notes — 版本历史（Nowen revisions 模式）：保存前快照 + 恢复自动备份。
-- edit 版本有窗口合并与定期清理（PruneRevisions），restore 版本永久保留。
CREATE TABLE brain.note_revisions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id        uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL,
    title          text NOT NULL DEFAULT '',
    content_md     text NOT NULL DEFAULT '',
    change_type    text NOT NULL CHECK (change_type IN ('edit', 'restore')),
    -- restore 自动备份固定写「恢复前自动备份」；edit 快照为 NULL。
    change_summary text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX note_revisions_note_created_idx
    ON brain.note_revisions (note_id, created_at DESC);

CREATE INDEX note_revisions_user_idx
    ON brain.note_revisions (user_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.note_revisions;
