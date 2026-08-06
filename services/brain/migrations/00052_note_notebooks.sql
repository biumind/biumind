-- +goose Up
-- +goose StatementBegin
-- Notes — 笔记本（单层，不做树；组织靠标签，笔记本只作粗分桶）。
-- 设计文档: docs/BiuMind-Notes-Design-Draft.md §4 D1 / §5 M1
CREATE TABLE brain.note_notebooks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL,
    name        text NOT NULL,
    position    double precision NOT NULL DEFAULT 0,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 同一用户下活笔记本名（大小写不敏感）唯一。
CREATE UNIQUE INDEX note_notebooks_user_name_alive
    ON brain.note_notebooks (user_id, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX note_notebooks_user_pos_idx
    ON brain.note_notebooks (user_id, position)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.note_notebooks;
