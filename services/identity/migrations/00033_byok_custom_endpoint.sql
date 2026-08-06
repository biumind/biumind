-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00033 — BYOK 支持 custom provider + base_url + protocol
--
-- P0 of docs/BiuMind-BYOK-Unification-Design.md. 纯增量, 不破坏现有 11 个
-- 标准 provider 行为:
--   1. 放宽 provider 枚举加 'custom' (用户自填代理 new-api/one-api/vLLM)
--   2. 加 base_url 列 (custom 必填, 标准可空走默认 endpoint)
--   3. 加 protocol 列 (openai_compat/anthropic/google/dashscope/volcengine)
--      —— P1 让 model-relay 用它选 adaptor, 替代模型名前缀猜测
--   4. backfill 旧数据 protocol (按 provider 原生协议映射)
--   5. unique 改造: 原 UNIQUE(user_id, provider) 对 custom 会冲突
--      (同用户多 custom), 改两个 partial unique index
--   6. custom_requires_endpoint: custom 必须配 base_url
-- ═══════════════════════════════════════════════════════════════════

-- 1. 新列
ALTER TABLE identity.user_api_keys
    ADD COLUMN IF NOT EXISTS base_url text,
    ADD COLUMN IF NOT EXISTS protocol text NOT NULL DEFAULT 'openai_compat'
        CHECK (protocol IN ('openai_compat','anthropic','google','dashscope','volcengine'));

-- 2. 放宽 provider 枚举 (00020 inline CHECK, 自动名 *_provider_check)
ALTER TABLE identity.user_api_keys DROP CONSTRAINT IF EXISTS user_api_keys_provider_check;
ALTER TABLE identity.user_api_keys ADD CONSTRAINT user_api_keys_provider_check
    CHECK (provider IN
        ('anthropic','openai','deepseek','doubao','dashscope',
         'volcengine','google','azure_openai','moonshot','qwen','baichuan',
         'custom'));

-- 3. backfill 旧数据 protocol (按 provider 原生协议; chat 模型 dashscope/
--    volcengine 标原生, P1 model-relay 可据此选 adaptor)
UPDATE identity.user_api_keys SET protocol = CASE
    WHEN provider = 'anthropic'  THEN 'anthropic'
    WHEN provider = 'google'     THEN 'google'
    WHEN provider = 'dashscope'  THEN 'dashscope'
    WHEN provider = 'volcengine' THEN 'volcengine'
    ELSE 'openai_compat'
END;

-- 4. unique 改造: drop 原 table constraint, 改 partial unique index
ALTER TABLE identity.user_api_keys DROP CONSTRAINT IF EXISTS user_api_keys_user_id_provider_key;

-- 标准 provider: 仍 (user_id, provider) 唯一
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_provider_std
    ON identity.user_api_keys (user_id, provider)
    WHERE provider <> 'custom';

-- custom: (user_id, base_url) 唯一 (同用户同代理地址一把 key)
CREATE UNIQUE INDEX IF NOT EXISTS identity_user_api_keys_user_baseurl_custom
    ON identity.user_api_keys (user_id, base_url)
    WHERE provider = 'custom';

-- 5. custom 必须配 base_url
ALTER TABLE identity.user_api_keys ADD CONSTRAINT user_api_keys_custom_requires_endpoint
    CHECK (provider <> 'custom' OR base_url IS NOT NULL);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 反向. 注意: 若已有 provider='custom' 行, 收回 CHECK 会失败 —— down 前需
-- 先清理 custom 数据 (DELETE WHERE provider='custom').

ALTER TABLE identity.user_api_keys DROP CONSTRAINT IF EXISTS user_api_keys_custom_requires_endpoint;
DROP INDEX IF EXISTS identity.user_api_keys_user_baseurl_custom;
DROP INDEX IF EXISTS identity.user_api_keys_user_provider_std;

-- 恢复原 table-level UNIQUE(user_id, provider)
ALTER TABLE identity.user_api_keys ADD CONSTRAINT user_api_keys_user_id_provider_key
    UNIQUE (user_id, provider);

ALTER TABLE identity.user_api_keys DROP CONSTRAINT IF EXISTS user_api_keys_provider_check;
ALTER TABLE identity.user_api_keys ADD CONSTRAINT user_api_keys_provider_check
    CHECK (provider IN
        ('anthropic','openai','deepseek','doubao','dashscope',
         'volcengine','google','azure_openai','moonshot','qwen','baichuan'));

ALTER TABLE identity.user_api_keys
    DROP COLUMN IF EXISTS protocol,
    DROP COLUMN IF EXISTS base_url;

-- +goose StatementEnd
