-- +goose Up
-- +goose StatementBegin
-- Notes — 标签与笔记-标签关联。scope_key 预留多空间语义，
-- 个人空间固定为 'personal:<uid>'（Nowen tags v59 模式）。
-- 设计文档: docs/BiuMind-Notes-Design-Draft.md §4 D1 / §5 M3
CREATE TABLE brain.note_tags (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL,
    scope_key  text NOT NULL,               -- "personal:<uid>"
    name       text NOT NULL,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 同一 scope 下活标签名（大小写不敏感）唯一。
CREATE UNIQUE INDEX note_tags_scope_name_alive
    ON brain.note_tags (scope_key, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX note_tags_user_idx
    ON brain.note_tags (user_id)
    WHERE deleted_at IS NULL;

CREATE TABLE brain.note_note_tags (
    note_id uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    tag_id  uuid NOT NULL REFERENCES brain.note_tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);

CREATE INDEX note_note_tags_tag_idx ON brain.note_note_tags (tag_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.note_note_tags;
DROP TABLE IF EXISTS brain.note_tags;
