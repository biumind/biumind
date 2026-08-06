-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R3 / Q3：agent_sessions.backend —— agent loop 用哪个 backend。
--   * 'biumindkit'（默认）— 内建 loop（chat/agent/task 都默认走这个）
--   * 'claude-cli'        — 外部 Claude Code CLI（D8 复活 agent/ 包；D1 CLI 自执行工具）
--   * 'codex-cli'         — 外部 Codex CLI（R8 接线）
-- per-execution 元数据（resume / 审计用）。设计文档 BiuMind-Runtime-v3-Design §6。
ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS backend text NOT NULL DEFAULT 'biumindkit'
        CHECK (backend IN ('biumindkit', 'claude-cli', 'codex-cli'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_sessions DROP COLUMN IF EXISTS backend;
-- +goose StatementEnd
