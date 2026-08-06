-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- model_relay 基线 schema (squash 00001–00016)
--
-- 本文件是原 16 个连续 migration (00001_model_relay_schema …
-- 00016_hotparse_model_seed) 折叠后的单一基线，面向全新部署。
-- 最终 schema 与顺序执行 00001–00016 后的状态完全一致：
--
--   * providers.protocol CHECK 直接用 00011 的三值终态
--     ('openai_compat', 'anthropic', 'dashscope')。
--   * models 折叠 00006 的 mode / pricing_strategy / dispatch_mode
--     (mode CHECK 直接用 00009 的 10 值终态，含 rerank / responses)
--     与 00010 的 fallback_models。
--   * channels 折叠 00010 的 endpoint_capability 与 00014 的
--     cooldown_until (+ channels_cooldown_idx)。
--   * pricing 折叠 00006 的四列 (cost_per_image / cost_per_video_second /
--     cost_per_audio_second / cost_per_character) 与 00013 的四列
--     (markup_ratio / min_charge / max_charge_per_request /
--     cost_per_search_unit)。
--   * usage_log 折叠 00015 的 credits_charged。
--   * pricing_rules (00006) 原样；notify_config_changed() 触发器
--     (00002) 原样挂 9 张表 — pricing_rules / usage_log / route_rules
--     历史上就没挂，保持不挂。
--   * seed 原样搬运：00003 (default model_group 固定 UUID
--     00000000-0000-0000-0000-000000000001 + 4 条 fx_rates)、
--     00005 (9 providers)、00016 (hotparse-v1 模型 + 展示用 pricing)，
--     均 ON CONFLICT 幂等。
--
-- 消去的一次性 backfill（新库无行可改，不进基线）：
--   * 00007 / 00008 — models.mode 脏数据回填（纯 UPDATE，无 DDL）。
--   * 00012 — rerank mode 脏数据回填（纯 UPDATE，无 DDL）。
--   * 00013 §2 — 从 billing.pricing_book 跨服务迁数据的 DO 块
--     （新库该表不存在；pricing 的四列已折叠进本基线）。
--
-- gen_random_uuid() 为 PG13+ 内置函数，无需 pgcrypto 扩展
-- （沿用原 00001 风格，不 CREATE EXTENSION）。
-- ═══════════════════════════════════════════════════════════════════

CREATE SCHEMA IF NOT EXISTS model_relay;

-- ─── 1. providers ───────────────────────────────────────────────────
-- A "provider" is one upstream LLM service (OpenAI, Anthropic, DeepSeek,
-- a specific Azure deployment, etc.). One provider can back many
-- credentials and many channels.
-- protocol CHECK 为 00011 终态：dashscope = 阿里云 DashScope 私有协议
-- (cosyvoice / paraformer / wanx 等 native API)，由独立 dashscope.Adaptor
-- 路由处理。见 docs/BiuMind-Multimodal-Gateway-Design.md §3.
CREATE TABLE IF NOT EXISTS model_relay.providers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code         varchar(64)  NOT NULL UNIQUE,                 -- "openai" / "anthropic" / "azure-eastus" / "deepseek"
    name         varchar(128) NOT NULL,                        -- 显示名
    protocol     text NOT NULL CHECK (protocol IN ('openai_compat', 'anthropic', 'dashscope')),
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
--
-- mode 维度 (00006 + 00009 终态 10 值) 让同一张表承载所有 modality；
-- fallback_models (00010) 给 ModeRouter 提供同 mode 内的 fallback chain，
-- 见 docs/BiuMind-Multimodal-Gateway-Design.md §4.3.
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

    -- modality 维度（00006 引入，CHECK 为 00009 的 10 值终态）
    mode             text NOT NULL DEFAULT 'chat'
                          CHECK (mode IN (
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
                          )),

    -- 计费策略（00006）：token → 按 *_per_mtok；fixed → 按 cost_per_*；
    -- parameter → cost_per_* 作 base 叠加 pricing_rules 多维乘数
    pricing_strategy text NOT NULL DEFAULT 'token'
                          CHECK (pricing_strategy IN ('token', 'parameter', 'fixed')),

    -- 调用形态（00006）
    dispatch_mode    text NOT NULL DEFAULT 'streaming'
                          CHECK (dispatch_mode IN ('sync', 'streaming', 'async')),

    -- 同 mode 内的备用 model code 链（00010），按数组顺序尝试
    fallback_models  text[] NOT NULL DEFAULT '{}',

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS models_status_idx
    ON model_relay.models (status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS models_family_idx
    ON model_relay.models (family);

-- 列表查询热路径（00006）：创作页只查生成类，聊天页只查 chat
CREATE INDEX IF NOT EXISTS models_mode_status_idx
    ON model_relay.models (mode, status)
    WHERE status = 'active';

-- ─── 4. channels ────────────────────────────────────────────────────
-- Smallest routable unit: "this model is provided by this credential
-- as this upstream model name with this priority". Multi-account
-- failover and weighted routing all happen at this granularity.
--
-- endpoint_capability (00010) 给 ChannelResolver 提供 endpoint 能力声明；
-- cooldown_until (00014) 是 auto_disabled channel 的精确恢复时刻，
-- sweep 捞 cooldown_until <= now 的行重探；NULL = 立即可探。
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

    -- endpoint 能力（00010）：standard=HTTP 同步/SSE 走 modality adaptor；
    -- realtime=WS 双向；passthrough=不走 adaptor 原样代理
    endpoint_capability text NOT NULL DEFAULT 'standard'
                         CHECK (endpoint_capability IN ('standard', 'realtime', 'passthrough')),

    -- auto_disabled 精确恢复时刻（00014）；NULL = 老语义（立即可探）
    cooldown_until  timestamptz,

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

-- cooldown sweep 热路径（00014）
CREATE INDEX IF NOT EXISTS channels_cooldown_idx
    ON model_relay.channels (status, cooldown_until)
    WHERE status = 'auto_disabled';

-- ─── 5. pricing ─────────────────────────────────────────────────────
-- 多币种主存原币种 — 不在录入时折算，避免精度丢失（Design §3.1 / Q2）。
-- 一个 model 在同一时刻只有 1 条 "current" 记录（按 effective_at DESC LIMIT 1
-- 查询最新）；旧记录保留作回溯定价。
--
-- cost_per_* 四列（00006）为字段超集，按 model.pricing_strategy 选用；
-- markup_ratio / min_charge / max_charge_per_request / cost_per_search_unit
-- （00013）使本表成为 billing 系统 single source of truth
-- （原 billing.pricing_book 的字段，该表已随 identity 端 migration 废弃）。
CREATE TABLE IF NOT EXISTS model_relay.pricing (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id              uuid NOT NULL REFERENCES model_relay.models(id) ON DELETE CASCADE,

    currency              text NOT NULL CHECK (currency IN ('CNY', 'USD')),

    -- 原币种百万 token 单价
    input_per_mtok        numeric(14, 6) NOT NULL DEFAULT 0,
    output_per_mtok       numeric(14, 6) NOT NULL DEFAULT 0,
    cache_write_per_mtok  numeric(14, 6) NOT NULL DEFAULT 0,
    cache_read_per_mtok   numeric(14, 6) NOT NULL DEFAULT 0,

    -- 固定计费字段超集（00006），nullable，按 pricing_strategy 选用
    cost_per_image          numeric(14, 6),
    cost_per_video_second   numeric(14, 6),
    cost_per_audio_second   numeric(14, 6),
    cost_per_character      numeric(14, 6),

    -- billing 扣费字段（00013）
    markup_ratio            numeric(6, 4) NOT NULL DEFAULT 3.0,
    min_charge              bigint NOT NULL DEFAULT 0,
    max_charge_per_request  bigint,
    cost_per_search_unit    numeric(14, 6) NOT NULL DEFAULT 0,

    effective_at          timestamptz NOT NULL DEFAULT now(),

    -- 审计：谁录入的（uuid，不 FK 跨 schema）
    created_by            uuid,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- 取最新定价的热路径
CREATE INDEX IF NOT EXISTS pricing_model_effective_idx
    ON model_relay.pricing (model_id, effective_at DESC);

-- ─── 5b. pricing_rules (0006) ────────────────────────────────────────
-- 多维乘数表，仅当 model.pricing_strategy='parameter' 时使用。结构迁移自
-- aigc.models.pricing_rule jsonb (by_duration / by_resolution)。
-- 计算公式: final_cost = base_cost(pricing 表) × Π(matched_rule.multiplier)
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

-- ─── 11. usage_log (00004 + 00015) ───────────────────────────────────
-- Per-request billing detail，落在 Postgres（BiuMind 无 ClickHouse 实例，
-- 见 deploy/docker-compose/README.md）。两个消费者：Prometheus 聚合大盘
-- （不动）+ admin 明细/结算审计。credits_charged（00015）是按定价折算的
-- 积分快照，供客户端「数据统计 · 用量」页按次/日/月展示；权威积分账本
-- 仍是 identity.credit_logs。
--
-- 跨 schema 不 FK（服务边界）：user_id / model_id / channel_id 只存 uuid。
CREATE TABLE IF NOT EXISTS model_relay.usage_log (
    id                     bigserial PRIMARY KEY,

    -- Tenant + routing identity
    user_id                uuid NOT NULL,
    model_id               uuid NOT NULL,
    channel_id             uuid NOT NULL,
    model_code             varchar(128) NOT NULL,                -- denormalised for admin queries
    upstream_model         varchar(128) NOT NULL,                -- denormalised; pricing audit
    user_plan              text NOT NULL DEFAULT 'free'
                                CHECK (user_plan IN ('free', 'pro', 'team')),

    -- Token counts (provider-reported)
    input_tokens           bigint NOT NULL DEFAULT 0,
    output_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_tokens     bigint NOT NULL DEFAULT 0,
    cache_read_tokens      bigint NOT NULL DEFAULT 0,

    -- Cost in source currency (whatever pricing.currency was at request time)
    cost_origin_currency   text NOT NULL CHECK (cost_origin_currency IN ('CNY', 'USD')),
    cost_origin_amount     numeric(18, 8) NOT NULL DEFAULT 0,

    -- Cost in user's settlement currency, after fx_rates lookup
    cost_settle_currency   text NOT NULL CHECK (cost_settle_currency IN ('CNY', 'USD')),
    cost_settle_amount     numeric(18, 8) NOT NULL DEFAULT 0,

    -- Snapshot of fx_rates.rate at request time (for retroactive audit
    -- — fx_rates row may have been updated since)
    fx_rate                numeric(10, 6) NOT NULL DEFAULT 1.0,

    -- Latency / outcome
    latency_ms             int NOT NULL DEFAULT 0,
    status                 text NOT NULL DEFAULT 'ok'
                                CHECK (status IN ('ok', 'error', 'rate_limited', 'cancelled')),
    error_code             text NOT NULL DEFAULT '',

    -- request_id matches the trace id surfaced to the client; useful
    -- for correlating with structured logs.
    request_id             text NOT NULL DEFAULT '',

    -- 本次请求结算的标价积分（00015），由 writeUsageLogAsync 写入
    credits_charged        bigint NOT NULL DEFAULT 0,

    created_at             timestamptz NOT NULL DEFAULT now()
);

-- Hot paths:
-- 1. "Show me user X's last N requests" → (user_id, created_at desc)
-- 2. "Channel Y's recent failures" → (channel_id, created_at desc) WHERE status='error'
-- 3. "Aggregate spend over period for billing" → btree on (created_at) is
--    enough until table crosses ~10M rows.
CREATE INDEX IF NOT EXISTS usage_log_user_time_idx
    ON model_relay.usage_log (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_channel_time_idx
    ON model_relay.usage_log (channel_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_model_time_idx
    ON model_relay.usage_log (model_id, created_at DESC);

CREATE INDEX IF NOT EXISTS usage_log_errors_idx
    ON model_relay.usage_log (created_at DESC)
    WHERE status != 'ok';

-- +goose StatementEnd

-- +goose StatementBegin

-- ─── LISTEN/NOTIFY fan-out (0002 原样) ──────────────────────────────
-- model-relay 进程内缓存 model / channel / credential 配置（TTL 60s）；
-- 每次变更发 NOTIFY，多副本部署 <1s 失效缓存。channel 名
-- 'model_relay_config_changed' 固定，internal/registry/cache.go 的订阅方
-- 必须严格匹配。payload: {"table": "...", "id": "...", "op": "..."}.
CREATE OR REPLACE FUNCTION model_relay.notify_config_changed()
RETURNS trigger AS $$
DECLARE
    row_id     text;
    op         text := TG_OP;
    payload    jsonb;
    target     record;
BEGIN
    -- DELETE 走 OLD，其它走 NEW
    IF op = 'DELETE' THEN
        target := OLD;
    ELSE
        target := NEW;
    END IF;

    -- 按表名分支取主键串
    -- 复合 PK 的表合成 "a/b" 形式；单 uuid PK 直接用 id::text
    CASE TG_TABLE_NAME
        WHEN 'fx_rates' THEN
            row_id := target.from_currency || '/' || target.to_currency;
        WHEN 'model_group_bindings' THEN
            row_id := target.group_id::text || '/' || target.model_id::text;
        WHEN 'user_group_memberships' THEN
            row_id := target.user_id::text || '/' || target.group_id::text;
        ELSE
            row_id := target.id::text;
    END CASE;

    payload := jsonb_build_object(
        'table', TG_TABLE_NAME,
        'id',    row_id,
        'op',    op
    );

    PERFORM pg_notify('model_relay_config_changed', payload::text);

    IF op = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 给 9 张表挂触发器 — route_rules / pricing_rules / usage_log 历史上
-- 就没挂（见 00002 注释：route_rules MVP 不读；pricing_rules / usage_log
-- 无缓存消费者），保持不挂。
DROP TRIGGER IF EXISTS notify_providers ON model_relay.providers;
CREATE TRIGGER notify_providers
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.providers
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_credentials ON model_relay.credentials;
CREATE TRIGGER notify_credentials
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.credentials
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_models ON model_relay.models;
CREATE TRIGGER notify_models
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.models
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_channels ON model_relay.channels;
CREATE TRIGGER notify_channels
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.channels
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_pricing ON model_relay.pricing;
CREATE TRIGGER notify_pricing
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.pricing
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_fx_rates ON model_relay.fx_rates;
CREATE TRIGGER notify_fx_rates
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.fx_rates
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_model_groups ON model_relay.model_groups;
CREATE TRIGGER notify_model_groups
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.model_groups
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_model_group_bindings ON model_relay.model_group_bindings;
CREATE TRIGGER notify_model_group_bindings
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.model_group_bindings
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_user_group_memberships ON model_relay.user_group_memberships;
CREATE TRIGGER notify_user_group_memberships
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.user_group_memberships
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

-- +goose StatementEnd

-- +goose StatementBegin

-- ─── seed data (00003 + 00005 + 00016 原样搬运，ON CONFLICT 幂等) ────

-- 1) default model group（00003）。固定 UUID 供应用层当常量引用；
--    缺失时新装环境所有模型路由到 0 channels。
INSERT INTO model_relay.model_groups (id, code, name, owner_type, owner_id, description, status)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'default',
    'Default',
    'system',
    '',
    'Built-in group every authenticated user implicitly belongs to. MVP: all models default-bound here.',
    'active'
)
ON CONFLICT (code) DO NOTHING;

-- 2) 初始 fx_rates（00003）。USD↔CNY 7.20 手填参考价，admin 经
--    /v1/admin/fx-rates 更新；Pricing resolver / Usage writer 缺行会拒绝结算。
INSERT INTO model_relay.fx_rates (from_currency, to_currency, rate, source)
VALUES
    ('USD', 'CNY', 7.200000, 'manual'),
    ('CNY', 'USD', 0.138889, 'manual'),
    ('USD', 'USD', 1.000000, 'manual'),
    ('CNY', 'CNY', 1.000000, 'manual')
ON CONFLICT (from_currency, to_currency) DO NOTHING;

-- 3) 9 个常用 providers（00005）。缺 provider 则无法加 credential /
--    channel / 路由。codes 是稳定标识，name 仅展示可被 admin 修改。
INSERT INTO model_relay.providers (code, name, protocol, icon, description, status) VALUES
  ('openai',     'OpenAI',           'openai_compat', '', 'GPT-4o / o-series / o1 / o3',                  'active'),
  ('anthropic',  'Anthropic',        'anthropic',     '', 'Claude family',                                 'active'),
  ('deepseek',   'DeepSeek',         'openai_compat', '', '深度求索（DeepSeek）',                          'active'),
  ('qwen',       '通义千问',          'openai_compat', '', '阿里云通义千问',                                'active'),
  ('kimi',       'Kimi (Moonshot)',  'openai_compat', '', '月之暗面',                                       'active'),
  ('zhipu',      '智谱 GLM',          'openai_compat', '', '智谱 AI',                                        'active'),
  ('gemini',     'Google Gemini',    'openai_compat', '', '通过 OpenAI 兼容代理（如 LiteLLM/OpenRouter）',  'active'),
  ('azure',      'Azure OpenAI',     'openai_compat', '', '需 base_url + api-version header',               'active'),
  ('openrouter', 'OpenRouter',       'openai_compat', '', '一键聚合多家上游',                                'active')
ON CONFLICT (code) DO NOTHING;

-- 4) hotparse-v1 爆款解析字典模型（00016）。aigc 服务
--    GET /v1/models?type=hotparse 读 model_relay.models；这是纯字典项，
--    不需要 channel / credential —— 真实上游调用由 worker 经
--    /v1/internal/transcribe(whisper-1) + /v1/internal/chat 完成。
INSERT INTO model_relay.models
    (code, display_name, family, mode, pricing_strategy, dispatch_mode,
     context_window, max_output, capabilities, status, sort_order, manual_override)
VALUES
    ('hotparse-v1', '爆款解析', 'hotparse', 'hotparse', 'fixed', 'async',
     0, 0,
     '{"stt_model":"whisper-1","llm_model":"claude-opus-4-8","sources":["upload"]}'::jsonb,
     'active', 100, false)
ON CONFLICT (code) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    family       = EXCLUDED.family,
    mode         = EXCLUDED.mode,
    capabilities = EXCLUDED.capabilities,
    status       = EXCLUDED.status,
    updated_at   = now();

-- 5) hotparse-v1 展示用估值价格（00016）：仅供 aigc 前端「约 N 积分」
--    展示 (computeCostCredits → GREATEST(cost_per_*))，不参与真实扣费。
INSERT INTO model_relay.pricing (model_id, currency, cost_per_image)
SELECT m.id, 'CNY', 20
FROM model_relay.models m
WHERE m.code = 'hotparse-v1'
  AND NOT EXISTS (
      SELECT 1 FROM model_relay.pricing p WHERE p.model_id = m.id
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 触发器与函数（DROP TABLE 会连带删触发器，但显式 drop 保持与 Up 对称）
DROP TRIGGER IF EXISTS notify_user_group_memberships ON model_relay.user_group_memberships;
DROP TRIGGER IF EXISTS notify_model_group_bindings ON model_relay.model_group_bindings;
DROP TRIGGER IF EXISTS notify_model_groups ON model_relay.model_groups;
DROP TRIGGER IF EXISTS notify_fx_rates ON model_relay.fx_rates;
DROP TRIGGER IF EXISTS notify_pricing ON model_relay.pricing;
DROP TRIGGER IF EXISTS notify_channels ON model_relay.channels;
DROP TRIGGER IF EXISTS notify_models ON model_relay.models;
DROP TRIGGER IF EXISTS notify_credentials ON model_relay.credentials;
DROP TRIGGER IF EXISTS notify_providers ON model_relay.providers;

DROP FUNCTION IF EXISTS model_relay.notify_config_changed();

DROP TABLE IF EXISTS model_relay.usage_log;
DROP TABLE IF EXISTS model_relay.user_group_memberships;
DROP TABLE IF EXISTS model_relay.model_group_bindings;
DROP TABLE IF EXISTS model_relay.model_groups;
DROP TABLE IF EXISTS model_relay.route_rules;
DROP TABLE IF EXISTS model_relay.fx_rates;
DROP TABLE IF EXISTS model_relay.pricing_rules;
DROP TABLE IF EXISTS model_relay.pricing;
DROP TABLE IF EXISTS model_relay.channels;
DROP TABLE IF EXISTS model_relay.models;
DROP TABLE IF EXISTS model_relay.credentials;
DROP TABLE IF EXISTS model_relay.providers;

-- model_relay schema 由本 migration 创建，down 时一并清理。
DROP SCHEMA IF EXISTS model_relay;

-- +goose StatementEnd
