-- +goose Up
-- +goose StatementBegin
-- 统一 brain.sources (webclip, 00001) → brain.wiki_sources (00034)。
-- 复用现有列：raw→extracted_text, sha256(hex)→content_hash(bytea),
-- status→parse_status。保留原 id 使 ingest_tasks.source_id FK 平滑
-- repoint（现存值无需改）。新增 kind/url/user_id/page_id/metadata 列
-- 兼容 webclip 字段。去重改 partial unique（webclip 按 content_hash，
-- upload 按 rel_path），替代旧全表 UNIQUE(project_id, rel_path)。
--
-- 旧 brain.sources 是 webclip 抓取主路径（链路全通）；brain.wiki_sources
-- 是文件上传半成品（parse lifecycle 死字段）。合并后单一来源表覆盖
-- webclip+upload+voice，解锁 source overlap 信号（P1-4）+ upload ingest。

-- (1) 加 webclip 需要的列
ALTER TABLE brain.wiki_sources
    ADD COLUMN IF NOT EXISTS kind     text NOT NULL DEFAULT 'upload',
    ADD COLUMN IF NOT EXISTS url      text,
    ADD COLUMN IF NOT EXISTS user_id  uuid,
    ADD COLUMN IF NOT EXISTS title    text,
    ADD COLUMN IF NOT EXISTS page_id  uuid,
    ADD COLUMN IF NOT EXISTS metadata jsonb NOT NULL DEFAULT '{}'::jsonb;

-- (2) 先删旧 UNIQUE(project_id, rel_path) —— 回填的 webclip 行 rel_path
--     用 url 末段兜底，同项目可能重复，旧全表约束会挡。稍后建 partial
--     unique 替代（webclip 不走 rel_path 去重，走 content_hash）。
ALTER TABLE brain.wiki_sources
    DROP CONSTRAINT IF EXISTS wiki_sources_project_id_rel_path_key;

-- (3) 回填 brain.sources → wiki_sources（保留原 id）。rel_path/filename
--     NOT NULL 用 url 末段/title 兜底。sha256 合法 hex 才 decode（防异常数据）。
INSERT INTO brain.wiki_sources
    (id, project_id, user_id, kind, url, title, extracted_text, metadata,
     content_hash, page_id, parse_status, file_id, rel_path, filename,
     byte_size, created_at, updated_at)
SELECT
    s.id, s.project_id, s.user_id, 'webclip', s.url, s.title,
    s.raw, s.metadata,
    decode(s.sha256, 'hex'),
    s.page_id,
    'done',                                  -- webclip raw 即终态文本
    NULL,                                    -- file_id（webclip 无文件）
    COALESCE(NULLIF(substring(s.url from '[^/]+$'), ''), 'webclip/' || s.id::text),
    COALESCE(NULLIF(s.title, ''), 'webclip-' || substring(s.id::text, 1, 8)),
    length(s.raw)::bigint,                   -- byte_size 近似（char len）
    s.created_at, s.created_at
FROM brain.sources s
WHERE s.sha256 ~ '^[0-9a-f]{64}$'
ON CONFLICT (id) DO NOTHING;

-- (4) repoint ingest_tasks.source_id FK → wiki_sources（id 保留，现存值无需改）
ALTER TABLE brain.ingest_tasks DROP CONSTRAINT IF EXISTS ingest_tasks_source_id_fkey;
ALTER TABLE brain.ingest_tasks
    ADD CONSTRAINT ingest_tasks_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES brain.wiki_sources(id) ON DELETE SET NULL;

-- (5) 建 partial unique 替代旧全表 UNIQUE
CREATE UNIQUE INDEX IF NOT EXISTS wiki_sources_upload_path_uniq
    ON brain.wiki_sources(project_id, rel_path) WHERE kind = 'upload';
CREATE UNIQUE INDEX IF NOT EXISTS wiki_sources_webclip_hash_uniq
    ON brain.wiki_sources(project_id, content_hash)
    WHERE kind = 'webclip' AND content_hash IS NOT NULL;

-- (6) 补 file_id FK（00034 注释写 brain.files 实为 files.objects，原无 FK）
ALTER TABLE brain.wiki_sources
    DROP CONSTRAINT IF EXISTS wiki_sources_file_id_fkey;
ALTER TABLE brain.wiki_sources
    ADD CONSTRAINT wiki_sources_file_id_fkey
    FOREIGN KEY (file_id) REFERENCES files.objects(id) ON DELETE SET NULL;

-- (7) 删旧表
DROP TABLE IF EXISTS brain.sources;

-- (8) 辅助索引（按 kind 分页列表）
CREATE INDEX IF NOT EXISTS wiki_sources_kind_idx
    ON brain.wiki_sources(project_id, kind, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 不可逆：DROP brain.sources 丢失 webclip raw/sha256 hex 原始语义。
-- Down 仅重建空 brain.sources 骨架 + 回滚 FK + 删新列。生产回滚靠 pg_dump 备份。
CREATE TABLE IF NOT EXISTS brain.sources (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    user_id     uuid, kind text, url text, title text NOT NULL DEFAULT '',
    raw         text NOT NULL DEFAULT '', metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    sha256      text NOT NULL DEFAULT '', page_id uuid,
    status      text NOT NULL DEFAULT 'pending',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sources_project_idx ON brain.sources(project_id);
CREATE INDEX IF NOT EXISTS sources_sha_idx ON brain.sources(project_id, sha256);

ALTER TABLE brain.ingest_tasks DROP CONSTRAINT IF EXISTS ingest_tasks_source_id_fkey;
ALTER TABLE brain.ingest_tasks
    ADD CONSTRAINT ingest_tasks_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES brain.sources(id) ON DELETE SET NULL;

DROP INDEX IF EXISTS brain.wiki_sources_upload_path_uniq;
DROP INDEX IF EXISTS brain.wiki_sources_webclip_hash_uniq;
DROP INDEX IF EXISTS brain.wiki_sources_kind_idx;
ALTER TABLE brain.wiki_sources DROP CONSTRAINT IF EXISTS wiki_sources_file_id_fkey;
ALTER TABLE brain.wiki_sources
    DROP COLUMN IF EXISTS kind,
    DROP COLUMN IF EXISTS url,
    DROP COLUMN IF EXISTS user_id,
    DROP COLUMN IF EXISTS title,
    DROP COLUMN IF EXISTS page_id,
    DROP COLUMN IF EXISTS metadata;
-- +goose StatementEnd
