-- +goose Up
-- Files — 通用文件存储元数据 (artifacts L3 / chat 附件 / 未来通用 file ref)
-- 实际 blob 落 MinIO (object_key 路径, bucket 一般 'biumind-files'),
-- Postgres 仅记录元数据 + sha256 dedup 索引。
--
-- 设计文档: docs/BiuMind-Code-Artifacts-Sync-Design.md §3.3
CREATE SCHEMA IF NOT EXISTS files;

CREATE TABLE files.objects (
    id              uuid        PRIMARY KEY,
    user_id         uuid        NOT NULL,
    sha256          text        NOT NULL,
    size_bytes      bigint      NOT NULL,
    mime_type       text,
    bucket          text        NOT NULL,
    object_key      text        NOT NULL,
    -- 来源 (e.g. 'code-artifact' / 'chat-attachment') — 给清理 job 按
    -- 来源删 / 给统计用; 业务侧不强约束。
    source          text        NOT NULL DEFAULT 'unknown',
    -- 任意业务元数据 (e.g. 'artifact_id': '...', 'task_id': '...')
    metadata        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- soft delete; 实际从 MinIO 删走清理 job 异步做
    deleted_at      timestamptz
);

-- Sha256 dedup: 同一用户同一 sha256 复用同一 object_key, 避免重复占空间。
-- 不跨用户 dedup — 隐私 + 配额隔离。
CREATE UNIQUE INDEX files_objects_user_sha256_alive
    ON files.objects (user_id, sha256)
    WHERE deleted_at IS NULL;

-- 用户级 list (按时间倒序)
CREATE INDEX files_objects_user_created
    ON files.objects (user_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS files.objects;
DROP SCHEMA IF EXISTS files;
