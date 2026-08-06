-- +goose Up
-- +goose StatementBegin
-- Notes — 笔记附件关联表，复用 files.objects + MinIO 通道；
-- 正文引用 URI 为 biu-file://<uuid>。is_associated 标记是否已被正文引用，
-- 供 files 既有孤儿清理 job 判定；last_seen_at 每次关联/引用刷新。
-- 设计文档: docs/BiuMind-Notes-Design-Draft.md §4 D1 / §5 M4
CREATE TABLE brain.note_attachments (
    note_id       uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    file_id       uuid NOT NULL REFERENCES files.objects(id) ON DELETE CASCADE,
    is_associated boolean NOT NULL DEFAULT false,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, file_id)
);

CREATE INDEX note_attachments_file_idx ON brain.note_attachments (file_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.note_attachments;
