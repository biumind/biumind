-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00009 — mode 枚举扩展: 加 rerank + responses
--
-- 配套 v0.3 全模态网关设计 (docs/BiuMind-Multimodal-Gateway-Design.md §6.1).
-- rerank: RAG 场景刚需 (bge-reranker / cohere rerank / jina-reranker)
-- responses: OpenAI Stateful Responses API (chat 的演进, M6 启用)
--
-- 操作: drop 旧 CHECK, 加新 CHECK 含 10 个 mode 值. 不动数据.
-- ═══════════════════════════════════════════════════════════════════

ALTER TABLE model_relay.models
    DROP CONSTRAINT IF EXISTS models_mode_check;

ALTER TABLE model_relay.models
    ADD CONSTRAINT models_mode_check CHECK (mode IN (
        'chat',
        'embedding',
        'image_generation',
        'video_generation',
        'digital_human',
        'audio_speech',
        'audio_transcription',
        'hotparse',
        'rerank',
        'responses'
    ));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 回滚: 恢复 8 mode 旧 CHECK. 但前提是没有 mode='rerank'/'responses' 的存量行,
-- 否则 ALTER 会失败. 应用层在 down 前必须先 UPDATE 把这些行改为 'chat'.
ALTER TABLE model_relay.models
    DROP CONSTRAINT IF EXISTS models_mode_check;

ALTER TABLE model_relay.models
    ADD CONSTRAINT models_mode_check CHECK (mode IN (
        'chat',
        'embedding',
        'image_generation',
        'video_generation',
        'digital_human',
        'audio_speech',
        'audio_transcription',
        'hotparse'
    ));

-- +goose StatementEnd
