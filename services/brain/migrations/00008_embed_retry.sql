-- ============================================================================
-- 00008_embed_retry.sql — wiki embed worker 毒丸重试上限（P1）
--
-- 现状：embed worker 逐条 Embed，失败 chunk 的 embedding 保持 NULL，每个
-- tick 被 ClaimUnembedded 无限 reclaim 重试（毒丸：坏输入永远卡住队列头）。
--
-- 本 migration 给 brain.wiki_chunks 加 embed_attempts 失败计数：
--   * 每次 embed 失败 +1（worker 在 claim 事务内 MarkEmbedFailures）。
--   * ClaimUnembedded 只认领 embed_attempts < 5 的行（Go 侧常量
--     chunks.MaxEmbedAttempts，与下面的索引谓词保持一致，改要两边同步）。
--   * pending 部分索引同步收窄，毒丸行不再进 reclaim 扫描。
--
-- 不加 last_error 列：失败原因走 slog（含 chunk_id），DB 只留计数。
-- 不涉及 files.objects 引用，无需登记 FGC 孤儿清单。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.wiki_chunks
    ADD COLUMN IF NOT EXISTS embed_attempts int NOT NULL DEFAULT 0;

-- reclaim 查询只扫可重试行；毒丸（embed_attempts >= 5）不进索引。
DROP INDEX IF EXISTS brain.wiki_chunks_pending_idx;
CREATE INDEX IF NOT EXISTS wiki_chunks_pending_idx
    ON brain.wiki_chunks(created_at)
    WHERE embedding IS NULL AND embed_attempts < 5;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP INDEX IF EXISTS brain.wiki_chunks_pending_idx;
ALTER TABLE brain.wiki_chunks DROP COLUMN IF EXISTS embed_attempts;
CREATE INDEX IF NOT EXISTS wiki_chunks_pending_idx
    ON brain.wiki_chunks(created_at)
    WHERE embedding IS NULL;
-- +goose StatementEnd
