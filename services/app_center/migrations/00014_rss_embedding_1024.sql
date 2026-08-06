-- M8.2 (v3): 把 entries.embedding / watch_rules.semantic_embedding 从 1536 维
-- 切到 1024 维, 对齐 admin 后台目前接的 bge-m3 (1024d). OpenAI text-embedding-3-*
-- 也支持 1024 dim output (dimensions 参数), 所以未来切 OpenAI 也无需 schema 改回.
--
-- 现状: dev 库 entries.embedding 全 NULL, watch_rules.semantic_embedding 全 NULL —
-- 无损切换. 多模型支持(同库 1024 + 1536 共存)目前不做; 未来真要切别的模型再
-- 决定迁移策略.
--
-- 同时加 embedding_model 列, 标记每行是哪个模型生成的, 方便后续模型切换时
-- 识别哪些行需要重算.
--
-- ivfflat 索引必须先 DROP 再重建 (维度变了原索引无效).

-- +goose Up
DROP INDEX IF EXISTS rss.entries_embedding_idx;

ALTER TABLE rss.entries
    ALTER COLUMN embedding TYPE vector(1024);

ALTER TABLE rss.entries
    ADD COLUMN IF NOT EXISTS embedding_model text;

ALTER TABLE rss.watch_rules
    ALTER COLUMN semantic_embedding TYPE vector(1024);

ALTER TABLE rss.watch_rules
    ADD COLUMN IF NOT EXISTS semantic_embedding_model text;

CREATE INDEX IF NOT EXISTS entries_embedding_idx
    ON rss.entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- 加一个偏函数索引方便 worker 找待 embed 的 entries: 没 embedding + 有 content.
CREATE INDEX IF NOT EXISTS entries_pending_embed_idx
    ON rss.entries (fetched_at DESC)
    WHERE embedding IS NULL
      AND (length(coalesce(content_text,''))+length(coalesce(title,''))) > 20;

-- +goose Down
DROP INDEX IF EXISTS rss.entries_pending_embed_idx;
DROP INDEX IF EXISTS rss.entries_embedding_idx;

ALTER TABLE rss.entries
    DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE rss.entries
    ALTER COLUMN embedding TYPE vector(1536);

ALTER TABLE rss.watch_rules
    DROP COLUMN IF EXISTS semantic_embedding_model;
ALTER TABLE rss.watch_rules
    ALTER COLUMN semantic_embedding TYPE vector(1536);

CREATE INDEX IF NOT EXISTS entries_embedding_idx
    ON rss.entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);
