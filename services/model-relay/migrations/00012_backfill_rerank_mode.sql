-- v0.3 M2.5 — backfill: 把 DB 里 code 含 'rerank' / 'reranker' 的模型
-- mode 修正为 'rerank'.
--
-- 起因: 00007/00008 backfill 跑的时候 inferMode 还没有 rerank 启发式,
-- bge-reranker-v2-m3 / qwen3-reranker-4b / cohere/rerank-v3.5 等 rerank
-- 模型被误归到 chat / embedding. M2.5 修了 inferMode 启发式 (rerank
-- 提到 embedding 之前), 但已入库的脏数据要这个 migration 兜底.
--
-- 安全护栏: 只动 manual_override=false 的行 — 管理员手工设的 mode 不动.

-- +goose Up
-- +goose StatementBegin
UPDATE model_relay.models
   SET mode = 'rerank',
       updated_at = now()
 WHERE manual_override = false
   AND mode <> 'rerank'
   AND (
       lower(code) LIKE '%rerank%'         -- bge-reranker / cohere/rerank /
                                            -- jina-reranker / voyage/rerank-2.5 /
                                            -- mxbai-rerank / nvidia rerank /
                                            -- qwen3-reranker / gte-rerank
       OR lower(code) LIKE '%reranker%'
   );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 不可逆: 单字段, 没办法精确还原原始 mode (chat / embedding). 留空.
SELECT 1;
-- +goose StatementEnd
