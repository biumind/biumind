-- +goose Up
-- +goose StatementBegin

-- ─── Brain.Wiki vector chunks ───────────────────────────────────
-- Embedding-bearing slices of wiki content. RRF 第三路（vector）就是
-- 在这张表上做 ANN 查询，再和 BM25(pages/blocks) 与 web 路融合。
--
-- 设计要点：
--
--   * page_id 必填：保证 cascade-on-page-delete + 命中后能回链页面
--   * block_id 可空：当切块对应到具体 block 时填，便于精确定位；page
--     级摘要 / 标题 chunk 留空
--   * ord 单页内单调递增：重切块时按 (page_id, ord) 覆盖即可
--   * embedding nullable：先入库后异步 embed（worker 按 NULL 扫描）
--   * token_count 用于上下文预算（context_budget），先存粗估值
--   * project_id 冗余存：检索时按 project 过滤不跨页 join
--
-- 为什么用一张 chunks 而不是直接给 brain.blocks 加 embedding 列：
--   1. 一个 block（如 paragraph）可能要切成多个 chunk
--   2. page 级摘要 / 标题不属于任何 block
--   3. 重切块策略可独立演进，不污染 blocks 这个 truth-source 表

CREATE TABLE IF NOT EXISTS brain.wiki_chunks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_id     uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    block_id    uuid          REFERENCES brain.blocks(id)   ON DELETE CASCADE,
    ord         int  NOT NULL DEFAULT 0,
    text        text NOT NULL,
    embedding   vector(1536),
    token_count int  NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 主键之外，按 page 重切块时按 (page_id, ord) 顺扫
CREATE INDEX IF NOT EXISTS wiki_chunks_page_ord_idx
    ON brain.wiki_chunks(page_id, ord);

-- 命中后按 project 过滤
CREATE INDEX IF NOT EXISTS wiki_chunks_project_idx
    ON brain.wiki_chunks(project_id);

-- worker 按 embedding IS NULL 扫描待 embed 队列
CREATE INDEX IF NOT EXISTS wiki_chunks_pending_idx
    ON brain.wiki_chunks(created_at)
    WHERE embedding IS NULL;

-- ANN 查询：cosine 距离 + ivfflat。lists=100 是 < 1M 行的合理默认；
-- 行数到百万级再考虑切到 hnsw 或调 lists。WHERE 子句让索引只覆盖
-- 已 embed 的行，避免空向量影响选择度。
CREATE INDEX IF NOT EXISTS wiki_chunks_embedding_ivf
    ON brain.wiki_chunks USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.wiki_chunks;
-- +goose StatementEnd
