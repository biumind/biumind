-- +goose Up
-- +goose StatementBegin
-- B2.1: wiki 项目源文件元数据表。一行 = 一个上传到该项目的文档（PDF/MD/HTML
-- 等）。底层二进制走通用 brain.files (MinIO 后端) 存储，本表用 file_id 引用，
-- 外加 wiki 维度的 rel_path / parse_status / 文本抽取结果等元数据。
--
-- 唯一约束 (project_id, rel_path) 让"重复上传同名文件"行为可控：业务层
-- 用 ON CONFLICT 实现 upsert（更新 file_id + content_hash + parse_status）。
-- content_hash 用于跨项目去重（B4 dedup 模块会消费）。
CREATE TABLE IF NOT EXISTS brain.wiki_sources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    file_id         uuid,                            -- brain.files.id；nullable 兼容外部 URL 来源
    rel_path        text NOT NULL,                   -- project-relative，e.g. "papers/attention.pdf"
    filename        text NOT NULL,                   -- 文件名（rel_path 末段；冗余便于查询）
    mime            text,
    byte_size       bigint NOT NULL DEFAULT 0,
    content_hash    bytea,                           -- sha256(content)，跨项目 dedup 用
    extracted_text  text,                            -- parser 抽取的纯文本（pdf → text）；可能巨大
    parse_status    text NOT NULL DEFAULT 'queued',  -- queued / processing / done / error
    parse_error     text,
    external_id     text,                            -- 来源外键，e.g. notion page id / github URL
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (project_id, rel_path)
);
CREATE INDEX wiki_sources_project_idx ON brain.wiki_sources(project_id, created_at DESC);
CREATE INDEX wiki_sources_hash_idx ON brain.wiki_sources(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX wiki_sources_external_id_idx ON brain.wiki_sources(project_id, external_id)
    WHERE external_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.wiki_sources;
-- +goose StatementEnd
