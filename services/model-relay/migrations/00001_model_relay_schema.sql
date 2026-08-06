-- +goose Up
-- +goose StatementBegin

-- ─── model_relay subsystem (MC-M1 — full schema baseline) ──────────
--
-- This migration lands the complete table set needed by the model
-- configuration backend. All 9 tables are created in one shot because
-- they are tightly cross-referenced (FK between providers/credentials/
-- channels/models/groups) and shipping them piecemeal would force
-- intermediate states that no application code can use.
--
-- Design provenance: docs/BiuMind-Model-Config-Design.md §3 "核心抽象".
-- Decision record: same doc §9 (7 decisions locked 2026-05-30).
--
-- Cross-schema FKs are deliberately NOT used:
--   * credentials does NOT FK to identity.users (created_by) — services
--     boundary, no hard coupling.
--   * user_group_memberships.user_id does NOT FK to identity.users —
--     same reason; orphan rows handled by cleanup job at the app layer.
--
-- ID convention: uuid + gen_random_uuid() (matches identity / authz);
-- app_center.apps uses text PK because it derives "app_<ulid>" deterministic
-- ids — model_relay rows have no such constraint, uuid is simpler.
--
-- Status enums use text + CHECK constraints rather than CREATE TYPE
-- so we can ALTER values without dropping dependent columns. Mirrors
-- existing app_center / identity convention.

CREATE SCHEMA IF NOT EXISTS model_relay;

-- ─── 1. providers ───────────────────────────────────────────────────
-- A "provider" is one upstream LLM service (OpenAI, Anthropic, DeepSeek,
-- a specific Azure deployment, etc.). One provider can back many
-- credentials and many channels.
CREATE TABLE IF NOT EXISTS model_relay.providers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code         varchar(64)  NOT NULL UNIQUE,                 -- "openai" / "anthropic" / "azure-eastus" / "deepseek"
    name         varchar(128) NOT NULL,                        -- 显示名
    protocol     text NOT NULL CHECK (protocol IN ('openai_compat', 'anthropic')),
    icon         text NOT NULL DEFAULT '',                     -- 公开 URL 或内置图标 key
    description  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'disabled')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS providers_status_idx
    ON model_relay.providers (status)
    WHERE status = 'active';

-- ─── 2. credentials ─────────────────────────────────────────────────
-- Encrypted upstream API keys. Layout mirrors keys.EncryptedKey
-- (services/model-relay/internal/keys/envelope.go): the 4 envelope
-- fields are stored as bytea columns; the application layer never
-- builds plaintext until the request hits the provider adaptor.
--
-- key_preview keeps a short non-sensitive summary ("sk-...abc1") so the
-- list page can render without decrypting. Generated once at insert.
CREATE TABLE IF NOT EXISTS model_relay.credentials (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id     uuid NOT NULL REFERENCES model_relay.providers(id) ON DELETE RESTRICT,
    label           varchar(128) NOT NULL,                      -- 自起名（"OpenAI 主账号"）

    -- envelope encryption fields (mirrors keys.EncryptedKey)
    ciphertext      bytea NOT NULL,                             -- AES-GCM(plaintext, dek, iv)
    wrapped_dek     bytea NOT NULL,                             -- AES-GCM(dek, kek, wrap_iv)
    iv              bytea NOT NULL,
    wrap_iv         bytea NOT NULL,

    -- non-sensitive display summary, e.g. "sk-...abc1"
    key_preview     varchar(32) NOT NULL DEFAULT '',

    -- per-credential overrides
    base_url        varchar(512) NOT NULL DEFAULT '',           -- 空 = provider 默认 endpoint
    header_override jsonb NOT NULL DEFAULT '{}'::jsonb,         -- 自定义请求头（Azure api-version 等）

    status          text NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'disabled', 'invalid')),
    last_test_at    timestamptz,
    last_test_error text NOT NULL DEFAULT '',

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS credentials_provider_idx
    ON model_relay.credentials (provider_id);

CREATE INDEX IF NOT EXISTS credentials_status_idx
    ON model_relay.credentials (status)
    WHERE status = 'active';

-- ─── 3. models ──────────────────────────────────────────────────────
-- Logical model alias surfaced to clients (the value of the `model`
-- request field). Decoupled from any specific provider — one alias can
-- be backed by multiple channels (multi-account / multi-region / fallback).
--
-- min_plan / model_groups bindings together implement the two-layer
-- visibility check (Design §3.2).
CREATE TABLE IF NOT EXISTS model_relay.models (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code             varchar(128) NOT NULL UNIQUE,              -- 客户端请求的 model 字段值
    display_name     varchar(128) NOT NULL,
    family           varchar(64)  NOT NULL DEFAULT '',          -- "gpt" / "claude" / "qwen" / "internal"

    context_window   int NOT NULL DEFAULT 0,                    -- 0 = 未知/未填
    max_output       int NOT NULL DEFAULT 0,

    -- {"vision": true, "tools": true, "thinking": false, "cache": true}
    -- 借鉴 litellm model_prices_and_context_window.json 的 supports_*
    capabilities     jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- 与 identity/billing.Plan 字面量对齐 — 不引入新枚举（Design §2.1）
    min_plan         text NOT NULL DEFAULT 'free'
                          CHECK (min_plan IN ('free', 'pro', 'team')),

    status           text NOT NULL DEFAULT 'disabled'
                          CHECK (status IN ('active', 'deprecated', 'disabled')),
    sort_order       int NOT NULL DEFAULT 0,

    -- 在线同步元数据：来源 vendor、source_etag、synced_at；手动新建为 NULL
    upstream_ref     jsonb,

    -- true = 再次同步不覆盖此模型字段（保护管理员手改）
    manual_override  boolean NOT NULL DEFAULT false,

    -- MVP 仅 'weighted'；P2 扩 'lowest_latency' / 'least_busy' / 'lowest_tpm_rpm' / 'cost_aware'
    routing_strategy text NOT NULL DEFAULT 'weighted'
                          CHECK (routing_strategy IN ('weighted', 'lowest_latency', 'least_busy', 'lowest_tpm_rpm', 'cost_aware')),

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS models_status_idx
    ON model_relay.models (status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS models_family_idx
    ON model_relay.models (family);

-- ─── 4. channels ────────────────────────────────────────────────────
-- Smallest routable unit: "this model is provided by this credential
-- as this upstream model name with this priority". Multi-account
-- failover and weighted routing all happen at this granularity.
CREATE TABLE IF NOT EXISTS model_relay.channels (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id        uuid NOT NULL REFERENCES model_relay.models(id) ON DELETE CASCADE,
    credential_id   uuid NOT NULL REFERENCES model_relay.credentials(id) ON DELETE RESTRICT,

    -- 上游真实模型 ID（如 "gpt-4o-2024-11-20"）
    upstream_model  varchar(128) NOT NULL,

    -- 路由参数 (weighted 策略) — priority 高者优先；同 priority 内按 weight 加权随机
    priority        int NOT NULL DEFAULT 0,
    weight          int NOT NULL DEFAULT 1 CHECK (weight >= 0),

    -- 通道维度限流（MVP 字段就位但 P2 才真正接 quota.Limiter）
    rpm_limit       int NOT NULL DEFAULT 0,
    tpm_limit       int NOT NULL DEFAULT 0,

    status          text NOT NULL DEFAULT 'disabled'
                         CHECK (status IN ('active', 'disabled', 'auto_disabled')),

    -- 健康监控
    failure_count   int NOT NULL DEFAULT 0,
    last_error_at   timestamptz,
    last_error      text NOT NULL DEFAULT '',
    last_test_at    timestamptz,
    latency_p50_ms  int NOT NULL DEFAULT 0,                    -- 留给 lowest_latency 策略

    -- 策略特定参数（cooldown_seconds / tags 等），按需扩展避免频繁迁移
    extra           jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- 路由热路径索引：按 model + status 过滤后，按 priority 排序
CREATE INDEX IF NOT EXISTS channels_model_status_priority_idx
    ON model_relay.channels (model_id, status, priority DESC)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS channels_credential_idx
    ON model_relay.channels (credential_id);

CREATE INDEX IF NOT EXISTS channels_auto_disabled_idx
    ON model_relay.channels (last_error_at)
    WHERE status = 'auto_disabled';

-- ─── 5. pricing ─────────────────────────────────────────────────────
-- 多币种主存原币种 — 不在录入时折算，避免精度丢失（Design §3.1 / Q2）。
-- 一个 model 在同一时刻只有 1 条 "current" 记录（按 effective_at DESC LIMIT 1
-- 查询最新）；旧记录保留作回溯定价。
CREATE TABLE IF NOT EXISTS model_relay.pricing (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id              uuid NOT NULL REFERENCES model_relay.models(id) ON DELETE CASCADE,

    currency              text NOT NULL CHECK (currency IN ('CNY', 'USD')),

    -- 原币种百万 token 单价
    input_per_mtok        numeric(14, 6) NOT NULL DEFAULT 0,
    output_per_mtok       numeric(14, 6) NOT NULL DEFAULT 0,
    cache_write_per_mtok  numeric(14, 6) NOT NULL DEFAULT 0,
    cache_read_per_mtok   numeric(14, 6) NOT NULL DEFAULT 0,

    effective_at          timestamptz NOT NULL DEFAULT now(),

    -- 审计：谁录入的（uuid，不 FK 跨 schema）
    created_by            uuid,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- 取最新定价的热路径
CREATE INDEX IF NOT EXISTS pricing_model_effective_idx
    ON model_relay.pricing (model_id, effective_at DESC);

-- ─── 6. fx_rates ────────────────────────────────────────────────────
-- 系统级汇率表。MVP 手填（source='manual'）；P2 接定时拉取（source='cron'）。
-- 主键 (from_currency, to_currency) 确保任意币种对只有一条 current rate。
-- 历史变动可通过审计日志 (identity.activity_events) 追溯，本表不留历史。
CREATE TABLE IF NOT EXISTS model_relay.fx_rates (
    from_currency text NOT NULL CHECK (from_currency IN ('USD', 'CNY')),
    to_currency   text NOT NULL CHECK (to_currency IN ('USD', 'CNY')),
    rate          numeric(10, 6) NOT NULL CHECK (rate > 0),
    source        text NOT NULL DEFAULT 'manual'
                       CHECK (source IN ('manual', 'cron')),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    uuid,                                         -- nullable; cron source 留空
    PRIMARY KEY (from_currency, to_currency)
);

-- ─── 7. route_rules ─────────────────────────────────────────────────
-- MVP 不开放编辑器（Design Q4），表预留以避免后期 schema 迁移。
-- ModelResolver 会跳过此表，按 channels.priority 默认路由。
CREATE TABLE IF NOT EXISTS model_relay.route_rules (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        varchar(128) NOT NULL,
    -- {"model_in": ["gpt-4o"], "user_plan": ["free"]}
    match_expr  jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- {"rewrite_model_to": "biu-fast"} 或 {"force_channel_id": "..."}
    action      jsonb NOT NULL DEFAULT '{}'::jsonb,
    priority    int NOT NULL DEFAULT 0,
    status      text NOT NULL DEFAULT 'disabled'
                     CHECK (status IN ('active', 'disabled')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- ─── 8. model_groups ────────────────────────────────────────────────
-- 自定义模型分组（Design §3.2）。MVP 阶段只用 system 级 'default' group;
-- owner_type='org' / 'user' 留给将来企业自定义模型组（Phase 3）。
CREATE TABLE IF NOT EXISTS model_relay.model_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        varchar(64) NOT NULL UNIQUE,                    -- "default" / "internal" / "acme_corp_premium"
    name        varchar(128) NOT NULL,
    owner_type  text NOT NULL DEFAULT 'system'
                     CHECK (owner_type IN ('system', 'org', 'user')),
    -- owner_type=org → org_id / user → user_id；system 留空。跨 schema 不 FK。
    owner_id    text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'archived')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS model_groups_owner_idx
    ON model_relay.model_groups (owner_type, owner_id)
    WHERE status = 'active';

-- ─── 9. model_group_bindings ────────────────────────────────────────
-- 模型 ↔ 分组多对多。MVP 阶段所有同步进来 + 手动新建的模型默认 bound 到
-- 'default' group（由 application 层 / seed 保证）。
CREATE TABLE IF NOT EXISTS model_relay.model_group_bindings (
    group_id    uuid NOT NULL REFERENCES model_relay.model_groups(id) ON DELETE CASCADE,
    model_id    uuid NOT NULL REFERENCES model_relay.models(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, model_id)
);

CREATE INDEX IF NOT EXISTS model_group_bindings_model_idx
    ON model_relay.model_group_bindings (model_id);

-- ─── 10. user_group_memberships ─────────────────────────────────────
-- 用户 ↔ 可见分组多对多。预留位 — MVP 阶段每个用户隐式属于 'default' group，
-- 不写显式记录；ModelResolver 在 'default' 分支短路。Phase 3 启用 Org 时
-- 才开始往这张表写实际行。
CREATE TABLE IF NOT EXISTS model_relay.user_group_memberships (
    user_id     uuid NOT NULL,                                  -- 跨 schema 不 FK 到 identity.users
    group_id    uuid NOT NULL REFERENCES model_relay.model_groups(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS user_group_memberships_group_idx
    ON model_relay.user_group_memberships (group_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS model_relay.user_group_memberships;
DROP TABLE IF EXISTS model_relay.model_group_bindings;
DROP TABLE IF EXISTS model_relay.model_groups;
DROP TABLE IF EXISTS model_relay.route_rules;
DROP TABLE IF EXISTS model_relay.fx_rates;
DROP TABLE IF EXISTS model_relay.pricing;
DROP TABLE IF EXISTS model_relay.channels;
DROP TABLE IF EXISTS model_relay.models;
DROP TABLE IF EXISTS model_relay.credentials;
DROP TABLE IF EXISTS model_relay.providers;

-- model_relay schema 由本 migration 创建，drop 时一并清理（首批表都在这里）。
DROP SCHEMA IF EXISTS model_relay;

-- +goose StatementEnd
