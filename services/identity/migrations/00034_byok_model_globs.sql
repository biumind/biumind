-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00034 — BYOK custom provider 声明所用模型 (model_globs)
--
-- P1 of docs/BiuMind-BYOK-Unification-Design.md. model-relay CredsResolver
-- 在 catalog 失败时按 model 匹配 custom BYOK 记录 → 需要记录声明它用于
-- 哪些模型. 标准 provider 不需要 (走 provider 匹配).
--
--   * model_globs text[]: {'glm-*'} / {'gpt-4o','claude-3'} / {'*'}
--   * custom 必填 (CHECK); 标准 provider 空
--   * 已有 custom 行 (00033 后没声明) backfill {'*'} 兜底, 不破坏 CHECK
-- ═══════════════════════════════════════════════════════════════════

ALTER TABLE identity.user_api_keys
    ADD COLUMN IF NOT EXISTS model_globs text[] NOT NULL DEFAULT '{}'::text[];

-- backfill 已有 custom 行 (没声明模型) → {'*'} 匹配任意, 让 CHECK 通过
UPDATE identity.user_api_keys
SET model_globs = ARRAY['*']
WHERE provider = 'custom'
  AND (model_globs = '{}'::text[] OR model_globs IS NULL);

ALTER TABLE identity.user_api_keys ADD CONSTRAINT user_api_keys_custom_requires_models
    CHECK (provider <> 'custom' OR array_length(model_globs, 1) >= 1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE identity.user_api_keys DROP CONSTRAINT IF EXISTS user_api_keys_custom_requires_models;
ALTER TABLE identity.user_api_keys DROP COLUMN IF EXISTS model_globs;

-- +goose StatementEnd
