-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- aigc subsystem (P1 — full schema baseline)
--
-- 文生图 / 文生视频 / 数字人 / 爆款解析的统一存储 schema。
--
-- 设计来源:
--   docs/BiuMind-AIGC-Migration-Plan.md §2.3
--   docs/BiuMind-AIGC-Storage-Design.md §6
--   docs/BiuMind-AIGC-Client-Progress-Design.md §3
--
-- 跨 schema FK 不使用:
--   * tasks.user_id 不 FK 到 identity.users (服务边界, 不硬耦合)
--   * task_outputs.sha256 是 CAS 主键, 与 brain.files 共用同一份语义但
--     物理分桶 (biumind-aigc-* vs biumind-brain-*), 不做 FK.
--
-- ID 约定: uuid + gen_random_uuid() (匹配 identity / authz / model_relay)
-- 状态枚举: text + CHECK 约束 (不用 CREATE TYPE, 便于平滑加值)
-- 时间字段: 全部 timestamptz, 默认 now()
-- ═══════════════════════════════════════════════════════════════════

CREATE SCHEMA IF NOT EXISTS aigc;

-- ─── 模型字典 / 供应商 ────────────────────────────────────

-- 供应商: dashscope (阿里通义) / volcengine (火山豆包) / openai / replicate / ...
-- credentials_ref 指向 model_relay.credentials 表的 id, 复用统一的 BYOK
-- 信封加密机制 (services/model-relay/internal/keys), 不重复实现.
CREATE TABLE aigc.providers (
    code            text PRIMARY KEY,
    name            text NOT NULL,
    base_url        text NOT NULL,
    credentials_ref text,
    priority        integer DEFAULT 0,
    enabled         boolean DEFAULT true,
    config          jsonb DEFAULT '{}'::jsonb,
    created_at      timestamptz DEFAULT now(),
    updated_at      timestamptz DEFAULT now()
);

-- 模型: 'wanx-2.6-t2v' / 'doubao-seedream-4.0' / ...
-- price_credits 是基础积分, pricing_rule 描述按参数加价规则 (如视频按时长).
-- config JSON 给前端 UI 渲染参数面板 (aspect_ratios / resolutions / duration / features).
CREATE TABLE aigc.models (
    code            text PRIMARY KEY,
    type            text NOT NULL CHECK (type IN ('image','video','digital_human','hotparse')),
    display_name    text NOT NULL,
    provider_code   text NOT NULL REFERENCES aigc.providers(code),
    price_credits   bigint NOT NULL DEFAULT 0,
    pricing_rule    jsonb,
    config          jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled         boolean DEFAULT true,
    sort_order      integer DEFAULT 0,
    created_at      timestamptz DEFAULT now(),
    updated_at      timestamptz DEFAULT now()
);
CREATE INDEX aigc_models_type_enabled_sort
    ON aigc.models (type, enabled, sort_order);

-- ─── 任务 ────────────────────────────────────────────────

-- 一行一任务. status 状态机: pending → queued → running → completed/failed/blocked/cancelled.
-- cache_key (v2): 推理缓存键, 命中时 cache_hit=true, 不调上游, 直接复用历史 output_sha.
-- deleted_at: 软删 30d 缓冲, 后台 GC job 物理删除.
CREATE TABLE aigc.tasks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL,
    org_id           uuid,
    type             text NOT NULL CHECK (type IN ('image','video','digital_human','hotparse')),
    model_code       text NOT NULL REFERENCES aigc.models(code),
    provider_code    text NOT NULL,
    prompt           text,
    negative_prompt  text,
    params           jsonb NOT NULL DEFAULT '{}'::jsonb,

    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','queued','running','completed','failed','blocked','cancelled')),
    progress         smallint DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error_code       text,
    error_message    text,

    cost_credits     bigint NOT NULL DEFAULT 0,
    refunded_credits bigint NOT NULL DEFAULT 0,
    is_public        boolean DEFAULT false,

    cache_key        text,
    cache_hit        boolean DEFAULT false,

    external_task_id text,
    parent_sha       text,
    lineage_op       text,

    deleted_at       timestamptz,
    created_at       timestamptz DEFAULT now(),
    queued_at        timestamptz,
    started_at       timestamptz,
    completed_at     timestamptz
);
CREATE INDEX aigc_tasks_user_created
    ON aigc.tasks (user_id, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX aigc_tasks_status_active
    ON aigc.tasks (status)
    WHERE status IN ('pending','queued','running');
CREATE INDEX aigc_tasks_public
    ON aigc.tasks (is_public, created_at DESC)
    WHERE is_public AND status = 'completed' AND deleted_at IS NULL;
CREATE INDEX aigc_tasks_cache_key
    ON aigc.tasks (cache_key)
    WHERE cache_key IS NOT NULL;
CREATE INDEX aigc_tasks_deleted
    ON aigc.tasks (deleted_at)
    WHERE deleted_at IS NOT NULL;

-- ─── 输出 ────────────────────────────────────────────────

-- 1 task : N outputs (不学 zhiying tb_creation.image_url_1~9 宽表).
-- sha256 是 CAS 业务主键 (storage_key 是物理位置).
-- moderation_status: 后置审核结果 (pass / block / review).
CREATE TABLE aigc.task_outputs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id           uuid NOT NULL REFERENCES aigc.tasks(id) ON DELETE CASCADE,
    idx               smallint NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('image','video','audio','cover')),

    sha256            text NOT NULL,
    storage_url       text NOT NULL,
    storage_key       text NOT NULL,

    blurhash          text,
    cover_sha         text,

    mime_type         text,
    file_size         bigint,
    width             integer,
    height            integer,
    duration_ms       integer,

    moderation_status text NOT NULL DEFAULT 'pending'
                      CHECK (moderation_status IN ('pending','pass','block','review')),

    metadata          jsonb DEFAULT '{}'::jsonb,
    created_at        timestamptz DEFAULT now()
);
CREATE INDEX aigc_task_outputs_task ON aigc.task_outputs (task_id);
CREATE INDEX aigc_task_outputs_sha  ON aigc.task_outputs (sha256);

-- ─── 用户上传源 (首帧 / 参考图 / 爆款视频源 / 数字人参考) ──────

CREATE TABLE aigc.uploaded_files (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL,
    sha256      text NOT NULL,
    purpose     text NOT NULL CHECK (purpose IN
                ('first_frame','last_frame','reference','character_avatar','voice_sample','hotparse_source')),
    storage_key text NOT NULL,
    mime_type   text,
    file_size   bigint,
    width       integer,
    height      integer,
    duration_ms integer,
    ref_count   integer DEFAULT 0,
    created_at  timestamptz DEFAULT now(),
    expires_at  timestamptz
);
CREATE UNIQUE INDEX aigc_uploaded_files_user_sha
    ON aigc.uploaded_files (user_id, sha256);
CREATE INDEX aigc_uploaded_files_gc
    ON aigc.uploaded_files (expires_at)
    WHERE ref_count = 0;

-- ─── 血缘 DAG (★ MVP 必做差异化能力) ─────────────────────────

-- 一条边: child_sha 由 parent_sha 经 op 派生而来.
-- op: remix | edit | inpaint | upscale | i2v | extract_frame |
--     style_transfer | first_frame | reference | cache_hit
CREATE TABLE aigc.asset_lineage (
    child_sha     text NOT NULL,
    parent_sha    text NOT NULL,
    op            text NOT NULL,
    op_params     jsonb DEFAULT '{}'::jsonb,
    child_task_id uuid,
    created_at    timestamptz DEFAULT now(),
    PRIMARY KEY (child_sha, parent_sha, op)
);
CREATE INDEX aigc_asset_lineage_parent ON aigc.asset_lineage (parent_sha);
CREATE INDEX aigc_asset_lineage_child  ON aigc.asset_lineage (child_sha);

-- ─── 数字人角色 / 爆款视频 ──────────────────────────────────

CREATE TABLE aigc.characters (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid,
    name            text NOT NULL,
    avatar_url      text,
    voice_default   text,
    config          jsonb DEFAULT '{}'::jsonb,
    is_public       boolean DEFAULT false,
    created_at      timestamptz DEFAULT now()
);
CREATE INDEX aigc_characters_user ON aigc.characters (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX aigc_characters_public ON aigc.characters (is_public) WHERE is_public;

CREATE TABLE aigc.hotparse_videos (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL,
    source      text NOT NULL CHECK (source IN
                ('upload','douyin','xhs','bilibili','kuaishou','wechat_channel')),
    source_url  text,
    cover_url   text,
    storage_url text,
    duration_ms integer,
    metadata    jsonb DEFAULT '{}'::jsonb,
    created_at  timestamptz DEFAULT now()
);
CREATE INDEX aigc_hotparse_videos_user ON aigc.hotparse_videos (user_id, created_at DESC);

-- ─── 推理缓存 (v2 启用, P1 建表占位) ──────────────────────────

CREATE TABLE aigc.inference_cache (
    cache_key   text PRIMARY KEY,
    output_sha  text NOT NULL,
    task_id     uuid NOT NULL REFERENCES aigc.tasks(id) ON DELETE CASCADE,
    hit_count   integer DEFAULT 1,
    last_hit_at timestamptz DEFAULT now(),
    created_at  timestamptz DEFAULT now()
);
CREATE INDEX aigc_inference_cache_output ON aigc.inference_cache (output_sha);

-- ─── 公开作品 CDN 镜像 ────────────────────────────────────

CREATE TABLE aigc.public_mirrors (
    output_sha     text PRIMARY KEY,
    cdn_url        text NOT NULL,
    bytes_served   bigint DEFAULT 0,
    last_served_at timestamptz,
    mirrored_at    timestamptz DEFAULT now()
);

-- ─── 内容审核结果 ─────────────────────────────────────────

CREATE TABLE aigc.moderation_results (
    sha256       text PRIMARY KEY,
    status       text NOT NULL CHECK (status IN ('pending','pass','block','review')),
    provider     text NOT NULL,
    raw_response jsonb,
    reviewed_at  timestamptz,
    reviewer     text
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS aigc.moderation_results;
DROP TABLE IF EXISTS aigc.public_mirrors;
DROP TABLE IF EXISTS aigc.inference_cache;
DROP TABLE IF EXISTS aigc.hotparse_videos;
DROP TABLE IF EXISTS aigc.characters;
DROP TABLE IF EXISTS aigc.asset_lineage;
DROP TABLE IF EXISTS aigc.uploaded_files;
DROP TABLE IF EXISTS aigc.task_outputs;
DROP TABLE IF EXISTS aigc.tasks;
DROP TABLE IF EXISTS aigc.models;
DROP TABLE IF EXISTS aigc.providers;

DROP SCHEMA IF EXISTS aigc;

-- +goose StatementEnd
