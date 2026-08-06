-- +goose Up
-- +goose StatementBegin
-- Notes N3 — webclip 来源字段 + 归档 + 转入知识库回链。
--   source_url / author : webclip 剪藏来源（客户端创建时可选传入）。
--   archived_at         : 归档时间戳；区别于 deleted_at（回收站）。
--                         归档笔记默认从列表/搜索隐藏，archived=only 只看归档。
--   promoted_page_id    : 「转入知识库」后生成的 wiki page id（幂等键：
--                         非空表示已 promote，重复调用直接回既有 page）。
ALTER TABLE brain.note_notes
    ADD COLUMN source_url       text,
    ADD COLUMN author           text,
    ADD COLUMN archived_at      timestamptz,
    ADD COLUMN promoted_page_id uuid;

-- 归档列表（archived=only）的查询路径。
CREATE INDEX note_notes_user_archived_idx
    ON brain.note_notes (user_id, archived_at DESC)
    WHERE archived_at IS NOT NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.note_notes_user_archived_idx;
ALTER TABLE brain.note_notes
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS author,
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS promoted_page_id;
-- +goose StatementEnd
