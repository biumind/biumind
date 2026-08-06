-- +goose Up
-- +goose StatementBegin

-- ─── Chat: thread provider_id ────────────────────────────────
--
-- 让 thread 显式记录走哪个 provider 路由,而不是只存 model_id 让 brain
-- 自己猜。同 model id 在多个 provider 下都可能存在(BiuMind Cloud 的
-- claude-sonnet-4-6 vs 用户自己加的 Anthropic provider 的 claude-sonnet-4-6),
-- 之前 brain router 拿 thread.model 自动选一条 active provider, 用户感知
-- 不到具体走哪条 — 这次让 picker 选定的 provider 一直跟随 thread。
--
-- 字段语义:
--   * 软关联 chat.providers.provider_id slug ('biumind_cloud' / 'anthropic' /
--     'openai' / 用户自定义 slug);**故意不加 FK** —— provider 删除后老
--     thread 的 provider_id 字符串仍然保留,brain router 找不到时降级 fallback
--     到首条 enabled provider(跟"BiuMind 默认"语义一致)。
--   * NULL = 走老路径(brain 自己挑 provider) —— 兼容存量 thread。
--   * 客户端通过 POST /v1/agent/sessions 的 provider_id 字段透传;brain
--     WorkPayload 加 ProviderID 让 daemon worker / chat runner 知道路由。

ALTER TABLE chat.threads
    ADD COLUMN IF NOT EXISTS provider_id TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE chat.threads
    DROP COLUMN IF EXISTS provider_id;

-- +goose StatementEnd
