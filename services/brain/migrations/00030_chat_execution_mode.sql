-- +goose Up
-- +goose StatementBegin

-- ─── Chat: thread execution_mode + provider internal flag ────
--
-- 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6
--
-- execution_mode 决定一条 thread 的 Agent 循环（LLM 调用 + 工具执行）
-- 在哪里跑:
--   * 'cloud'  — Brain → Hub。默认。当 Provider 公网可达，且工具
--                也在 Brain 侧实现时使用。
--   * 'client' — Flutter / Web 客户端直连 LLM 并在本地执行工具。
--                适用于:
--                  - LLM proxy 部署在公司内网（Brain 访问不到）
--                  - 工具需要访问本机文件 / 内网服务
--                  - 数据不出本机的隐私需求
--
-- providers.internal 标记一个 Provider 仅内网可达。设为 true 时:
--   * Provider 自动锁 fetch_mode='client'
--   * 用此 Provider 的 thread 默认创建为 execution_mode='client'
--   * UI 警示无法走 cloud 路径
--
-- 注: 真正的 agent 循环代码尚未集成到 Chat (W6/W7 任务)，本 migration
-- 只准备元数据字段，让 thread/provider CRUD 提前透传，避免后续再
-- 改 schema。

ALTER TABLE chat.threads
    ADD COLUMN IF NOT EXISTS execution_mode text NOT NULL DEFAULT 'cloud'
        CHECK (execution_mode IN ('cloud', 'client'));

ALTER TABLE chat.providers
    ADD COLUMN IF NOT EXISTS internal boolean NOT NULL DEFAULT false;

-- 内网 Provider 必须 client fetch
-- (CHECK 约束: internal=true ⇒ fetch_mode='client')
-- Postgres 16 没有 ADD CONSTRAINT IF NOT EXISTS,用 DO block 守门
-- 让本 migration 在 version-id 冲突 / rename 后可重跑.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'providers_internal_requires_client'
    ) THEN
        ALTER TABLE chat.providers
            ADD CONSTRAINT providers_internal_requires_client
                CHECK (NOT internal OR fetch_mode = 'client');
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE chat.providers
    DROP CONSTRAINT IF EXISTS providers_internal_requires_client;

ALTER TABLE chat.providers
    DROP COLUMN IF EXISTS internal;

ALTER TABLE chat.threads
    DROP COLUMN IF EXISTS execution_mode;

-- +goose StatementEnd
