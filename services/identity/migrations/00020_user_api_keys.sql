-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- user_api_keys — BYOK (Bring Your Own Key)
--
-- 用户在「设置 → API Keys」录入自己的上游 API Key (OpenAI / Anthropic /
-- DeepSeek / Doubao / DashScope / VolcEngine / Google / Azure 等). 命中
-- 时 model-relay / aigc-worker 直连上游, 跳过 Hold/Settle, 平台不扣费.
--
-- 加密:
--   AES-256-GCM, 主密钥 BYOK_MASTER_KEY 仅从 env 注入, 不进 git;
--   nonce 每次写新生成 (12 bytes), 与密文一并存;
--   API 永远不返回明文, 只返 last4 + 状态 + 元数据.
--   v2 切 KMS + 信封加密, 主密钥旋转用 KEK 表 (历史密钥保留可解旧密文).
--
-- 失效自动检测:
--   写入时异步 ping 上游 → status=valid/invalid;
--   resolver 命中后调用失败累计 failure_count, ≥5 自动 invalid;
--   客户端见 ❌ 标签提示用户更新 Key.
--
-- 默认行为: 命中时不 fallback 到平台 Key (尊重用户「别帮我花钱」本意);
-- 用户可在 settings 勾选 allow_platform_fallback (此开关在 user 表未来扩展).
--
-- 设计来源: docs/BiuMind-Billing-Redesign.md §5.4.
-- ═══════════════════════════════════════════════════════════════════

CREATE TABLE identity.user_api_keys (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,

    -- provider 枚举 (与 model-relay 上游 provider 对齐)
    provider            text NOT NULL CHECK (provider IN
                        ('anthropic','openai','deepseek','doubao','dashscope',
                         'volcengine','google','azure_openai','moonshot','qwen','baichuan')),

    -- 用户给的备注名 (e.g. "我的 OpenAI 主号")
    label               text,

    -- 加密存储 (AES-256-GCM)
    encrypted_value     bytea NOT NULL,
    nonce               bytea NOT NULL,
    -- 明文 last4 (用于客户端展示 "sk-...AbCd"), 不影响安全
    last4               text,

    -- 部分 provider 需要额外字段 (Azure endpoint / base_url 覆盖 / region 等)
    config_json         jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- 状态
    status              text NOT NULL DEFAULT 'valid' CHECK (status IN
                        ('valid','invalid','revoked','expired')),
    -- 健康检查 / 调用统计
    last_validated_at   timestamptz,
    last_used_at        timestamptz,
    failure_count       int NOT NULL DEFAULT 0 CHECK (failure_count >= 0),

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    -- 同 user 同 provider 仅一条 (PUT 覆盖更新)
    UNIQUE (user_id, provider)
);

-- resolver 主索引: 给定 user + provider 查 valid 的 Key
CREATE INDEX identity_user_api_keys_active
    ON identity.user_api_keys (user_id, provider)
    WHERE status = 'valid';

-- 后台 / 风控按 provider 看分布
CREATE INDEX identity_user_api_keys_provider
    ON identity.user_api_keys (provider, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS identity.user_api_keys;

-- +goose StatementEnd
