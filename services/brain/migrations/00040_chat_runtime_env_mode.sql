-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R2 / 轴 B：runtime_env_mode —— 工具在哪执行（none/local/cloud）。
-- 与 mode（轴 A：loop 在哪跑）正交。落在 agent_sessions（per-execution
-- 元数据，resume 时重建 WorkPayload 用）：
--   * 'none'  — 无外设工具（chat 模式恒此）
--   * 'local' — 用户机器 daemon 本机执行（agent 模式默认）
--   * 'cloud' — services/sandbox 容器执行（task 恒此；agent 可选，R5 落地）
-- 设计文档: docs/BiuMind-Runtime-v3-Design.md §5。
ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS runtime_env_mode text NOT NULL DEFAULT 'none'
        CHECK (runtime_env_mode IN ('none', 'local', 'cloud'));

-- D9：废弃 chat.threads.execution_mode。它语义是 loop 位置（轴 A），与
-- agent_sessions.mode(chat/agent/task) 重叠；execution_mode=client 是从未
-- 实现的半成品（客户端无本地 loop）。loop 位置统一由 mode 表达。
-- 注：chat.providers.internal / fetch_mode / providers_internal_requires_client
-- 约束引用的是 fetch_mode，与本列无关，保留不动。
ALTER TABLE chat.threads DROP COLUMN IF EXISTS execution_mode;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 反向：恢复结构（原 client 值不可逆——client 从未实现，可接受）。
ALTER TABLE chat.threads
    ADD COLUMN IF NOT EXISTS execution_mode text NOT NULL DEFAULT 'cloud'
        CHECK (execution_mode IN ('cloud', 'client'));

ALTER TABLE agent_sessions DROP COLUMN IF EXISTS runtime_env_mode;

-- +goose StatementEnd
