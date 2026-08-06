-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00010 — fallback_models + endpoint_capability
--
-- v0.3 全模态网关 M0.1: 给 ModeRouter 提供 fallback chain 数据 + 给
-- ChannelResolver 提供 endpoint 能力声明.
--
-- ① models.fallback_models text[]:
--    主模型/主渠道全部失败时按数组顺序尝试备用 model. 例:
--      claude-haiku-4-5 → ['claude-haiku-4-5-20251001', 'gpt-4o-mini']
--    在同一 mode 内 fallback (embedding 不能 fallback 到 chat).
--    见 docs/BiuMind-Multimodal-Gateway-Design.md §4.3.
--
-- ② channels.endpoint_capability text:
--    standard    — HTTP 同步/SSE 流, 走 modality adaptor (默认)
--    realtime    — WS 双向, 仅 chat / audio_speech / audio_transcription
--                  上有意义 (M5 启用)
--    passthrough — 不走 adaptor, 原样代理 (小众 provider 兜底, M6 启用)
--    见 docs/BiuMind-Multimodal-Gateway-Design.md §6.2.
--
-- 操作: 仅扩字段, 不动数据. 默认值让现有行天然兼容.
-- ═══════════════════════════════════════════════════════════════════

ALTER TABLE model_relay.models
    ADD COLUMN IF NOT EXISTS fallback_models text[] NOT NULL DEFAULT '{}';

ALTER TABLE model_relay.channels
    ADD COLUMN IF NOT EXISTS endpoint_capability text NOT NULL DEFAULT 'standard'
        CHECK (endpoint_capability IN ('standard', 'realtime', 'passthrough'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE model_relay.channels
    DROP COLUMN IF EXISTS endpoint_capability;

ALTER TABLE model_relay.models
    DROP COLUMN IF EXISTS fallback_models;

-- +goose StatementEnd
