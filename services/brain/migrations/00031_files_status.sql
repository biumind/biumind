-- +goose Up
-- files.objects.status — 区分两段式上传的中间态。
--
--   pending   presigned PUT 已发, 但 client 尚未 finalize / 字节可能没真的传上来
--   ready     finalize 通过 (Blob.Head 验证) — 业务可以引用
--   orphan    GC job 标记的待删 (短 TTL 后真删 MinIO 对象)
--
-- 旧路径 (POST /v1/files/upload multipart) 一次插入即写 'ready', 兼容无影响。

ALTER TABLE files.objects
    ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'ready';

-- pending / orphan 才需要扫描; ready 的对象不进这个索引省空间。
CREATE INDEX IF NOT EXISTS files_objects_status_created
    ON files.objects (status, created_at)
    WHERE status <> 'ready';

-- +goose Down
DROP INDEX IF EXISTS files_objects_status_created;
ALTER TABLE files.objects DROP COLUMN IF EXISTS status;
