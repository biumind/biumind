-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- Phase 4 段 2 (P4.S2.1) — 多模态扩展
--
-- 在 model_relay v0.1 chat-only schema 基础上加 mode 维度, 让同一张
-- model_relay.models 表能承载所有 modality (chat / embedding /
-- image_generation / video_generation / digital_human / audio_speech /
-- audio_transcription / hotparse). 配套 pricing 字段超集 + pricing_rules
-- 多维乘数表.
--
-- 设计来源:
--   docs/BiuMind-Model-Config-Design.md §1.4 / §3.1 / §3.4
--   docs/BiuMind-Model-Config-Dev-Plan.md §7.5 P4.S2.1
--
-- 借鉴 LiteLLM:
--   model_prices_and_context_window.json 的 `mode` 字段命名 (8 枚举);
--   pricing 字段超集 (input_cost_per_token / output_cost_per_image /
--   input_cost_per_audio_per_second / 字符价)
--
-- 不变量:
--   * v0.1 任何字段 0 改动, 0 删除 — 仅追加列 / 加表
--   * 已存在的 chat 模型自动 mode='chat' / pricing_strategy='token' /
--     dispatch_mode='streaming' (DEFAULT 兜底), 无需手动迁移
--   * channels 表 1:1 不变 (Q10 决策, 不引入 supported_modes)
--   * pricing 表新字段全部 nullable, 按 model.pricing_strategy 选用
-- ═══════════════════════════════════════════════════════════════════

-- ─── models 表追加 3 列 ────────────────────────────────────────

ALTER TABLE model_relay.models
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'chat'
        CHECK (mode IN (
            'chat',
            'embedding',
            'image_generation',
            'video_generation',
            'digital_human',
            'audio_speech',
            'audio_transcription',
            'hotparse'
        ));

ALTER TABLE model_relay.models
    ADD COLUMN IF NOT EXISTS pricing_strategy text NOT NULL DEFAULT 'token'
        CHECK (pricing_strategy IN ('token', 'parameter', 'fixed'));

ALTER TABLE model_relay.models
    ADD COLUMN IF NOT EXISTS dispatch_mode text NOT NULL DEFAULT 'streaming'
        CHECK (dispatch_mode IN ('sync', 'streaming', 'async'));

-- 列表查询热路径: 创作页只查生成类, 聊天页只查 chat
CREATE INDEX IF NOT EXISTS models_mode_status_idx
    ON model_relay.models (mode, status)
    WHERE status = 'active';

-- ─── pricing 表追加 4 列 (字段超集) ────────────────────────────
-- 全部 nullable, 按 model.pricing_strategy 选用:
--   token     → input_per_mtok / output_per_mtok / cache_*_per_mtok (现有)
--   fixed     → cost_per_image / cost_per_video_second / cost_per_audio_second / cost_per_character
--   parameter → cost_per_* 作为 base, 叠加 pricing_rules 多维乘数

ALTER TABLE model_relay.pricing
    ADD COLUMN IF NOT EXISTS cost_per_image          numeric(14, 6);
ALTER TABLE model_relay.pricing
    ADD COLUMN IF NOT EXISTS cost_per_video_second   numeric(14, 6);
ALTER TABLE model_relay.pricing
    ADD COLUMN IF NOT EXISTS cost_per_audio_second   numeric(14, 6);
ALTER TABLE model_relay.pricing
    ADD COLUMN IF NOT EXISTS cost_per_character      numeric(14, 6);

-- ─── pricing_rules 多维乘数表 ────────────────────────────────
-- 仅当 model.pricing_strategy='parameter' 时使用. 结构直接迁移自
-- 现有 aigc.models.pricing_rule jsonb (by_duration / by_resolution).
-- 计算公式: final_cost = base_cost(pricing 表) × Π(matched_rule.multiplier)
--   例: 5s 720p 视频  = 40 积分 × 1.0 × 1.0 = 40
--   例: 15s 1080p 视频 = 40 积分 × 2.6 × 2.0 = 208

CREATE TABLE IF NOT EXISTS model_relay.pricing_rules (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id      uuid NOT NULL REFERENCES model_relay.models(id) ON DELETE CASCADE,
    rule_jsonb    jsonb NOT NULL,
    effective_at  timestamptz NOT NULL DEFAULT now(),
    created_by    uuid,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- 取最新规则的热路径 — 与 pricing 表同款索引
CREATE INDEX IF NOT EXISTS pricing_rules_model_effective_idx
    ON model_relay.pricing_rules (model_id, effective_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS model_relay.pricing_rules_model_effective_idx;
DROP TABLE IF EXISTS model_relay.pricing_rules;

ALTER TABLE model_relay.pricing DROP COLUMN IF EXISTS cost_per_character;
ALTER TABLE model_relay.pricing DROP COLUMN IF EXISTS cost_per_audio_second;
ALTER TABLE model_relay.pricing DROP COLUMN IF EXISTS cost_per_video_second;
ALTER TABLE model_relay.pricing DROP COLUMN IF EXISTS cost_per_image;

DROP INDEX IF EXISTS model_relay.models_mode_status_idx;
ALTER TABLE model_relay.models DROP COLUMN IF EXISTS dispatch_mode;
ALTER TABLE model_relay.models DROP COLUMN IF EXISTS pricing_strategy;
ALTER TABLE model_relay.models DROP COLUMN IF EXISTS mode;

-- +goose StatementEnd
