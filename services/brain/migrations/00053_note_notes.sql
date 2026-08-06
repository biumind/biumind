-- +goose Up
-- +goose StatementBegin
-- Notes — 笔记主表（整篇 markdown 权威，无块层；is_todo + 完成时间戳统一
-- notes/todos；version 乐观锁；软删回收站；浮点 position 插值排序）。
-- 设计文档: docs/BiuMind-Notes-Design-Draft.md §4 D1 / §5 M2
CREATE TABLE brain.note_notes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid NOT NULL,
    -- NULL = 根（不属于任何笔记本）
    notebook_id       uuid REFERENCES brain.note_notebooks(id) ON DELETE SET NULL,
    title             text NOT NULL DEFAULT '',
    content_md        text NOT NULL DEFAULT '',
    is_todo           boolean NOT NULL DEFAULT false,
    todo_completed_at timestamptz,
    position          double precision NOT NULL DEFAULT 0,
    version           int NOT NULL DEFAULT 1,
    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX note_notes_user_nb_pos_idx
    ON brain.note_notes (user_id, notebook_id, position)
    WHERE deleted_at IS NULL;

-- 中文全文索引：zhparser biumind_zhcn 配置（同 00002_search.sql），
-- title 权重 A、content_md 权重 B。
ALTER TABLE brain.note_notes ADD COLUMN tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('biumind_zhcn', coalesce(title, '')), 'A')
    || setweight(to_tsvector('biumind_zhcn', coalesce(content_md, '')), 'B')
  ) STORED;
CREATE INDEX note_notes_tsv_gin ON brain.note_notes USING GIN (tsv) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS brain.note_notes;
