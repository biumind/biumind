-- Phase 3: parse_status 状态机硬化 + retries 列。
--
-- 00034 只在注释里写 parse_status 取 queued/processing/done/error，
-- 没有 DB 层 CHECK 约束（应用层约束漏洞：误写任意字符串不会报错）。
-- 本 migration 补 CHECK，并加 retries 列（parse 失败重试计数，
-- wiki-parse worker 用：retries < 3 才重入队列）。
--
-- 升级前预检（生产）：SELECT DISTINCT parse_status FROM brain.wiki_sources
-- 应只在 4 个合法值内；下方 UPDATE 兜底把任何越界值归为 'error'。

-- +goose Up

-- 越界值兜底（理论上空集，防御性归一）。
UPDATE brain.wiki_sources
   SET parse_status = 'error'
 WHERE parse_status NOT IN ('queued', 'processing', 'done', 'error');

ALTER TABLE brain.wiki_sources
    ADD COLUMN IF NOT EXISTS retries int NOT NULL DEFAULT 0;

ALTER TABLE brain.wiki_sources
    ADD CONSTRAINT wiki_sources_parse_status_check
    CHECK (parse_status IN ('queued', 'processing', 'done', 'error'));

CREATE INDEX IF NOT EXISTS wiki_sources_parse_queue_idx
    ON brain.wiki_sources (parse_status, retries, created_at)
    WHERE kind = 'upload' AND file_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS brain.wiki_sources_parse_queue_idx;

ALTER TABLE brain.wiki_sources
    DROP CONSTRAINT IF EXISTS wiki_sources_parse_status_check;

ALTER TABLE brain.wiki_sources
    DROP COLUMN IF EXISTS retries;
