-- +goose Up
-- +goose StatementBegin
-- Agent Plane 数据层（S3-1）—— 三张表，**故意删掉了**两张早期设计稿里
-- 提到的：
--
--   ❌ agent_session_events  —— frame 流走 NATS JetStream (MaxMsgs=1000 +
--      MaxAge=1h)，Postgres 不存逐 frame 回放数据。理由：用户回看的是"任
--      务结果"而非逐 frame 重放；per-frame INSERT 给 Postgres 写入压力
--      不必要。
--   ❌ agent_work_items       —— work queue 用 NATS JetStream durable
--      consumer + AckWait，死 worker 自动 redeliver；SQL queue 多余。
--
-- 详见 docs/BiuMind-Agent-Plane-Design.md §5.4。
--
-- 三张表都建在 public schema —— Agent Plane 跨 chat/wiki/memory 多个领
-- 域，没有合适的现存 schema；新建独立 schema (agent.*) 也行但当前没必
-- 要，等表数 ≥5 再分。

-- ── 1. agent_environments：worker 注册表 ─────────────────────
-- 每条记录 = 一个 worker 实例（biu daemon / biu CLI / runtime pod）
-- 注册时写入，heartbeat 续 last_seen_at，超过 90s 没心跳由 janitor 标 offline。
CREATE TABLE IF NOT EXISTS agent_environments (
    environment_id   UUID PRIMARY KEY,
    user_id          UUID,                                  -- NULL = 系统级（runtime 共享池），否则归属用户
    worker_kind      TEXT NOT NULL,                         -- 'biu_daemon' | 'biu_cli' | 'runtime'
    machine_name     TEXT NOT NULL,                         -- biu_daemon: hostname; runtime: pod name (见 Design §5.4)
    os_arch          TEXT,                                  -- "darwin/arm64" / "linux/amd64"
    git_info         JSONB,                                 -- {repo, branch, dir} for biu_daemon (Agent 模式)
    capabilities     TEXT[],                                -- ["sandbox", "mcp:supabase", "skills:5"]
    public_key       BYTEA,                                 -- X25519 public key (S3-4 加密 work_secret 用)
    pool_tag         TEXT,                                  -- runtime 副本池负载均衡标签 (e.g. "runtime-prod" / "runtime-gpu")
    state            TEXT NOT NULL DEFAULT 'online',        -- 'online' | 'offline' | 'draining'
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_env_kind_chk CHECK (worker_kind IN ('biu_daemon', 'biu_cli', 'runtime')),
    CONSTRAINT agent_env_state_chk CHECK (state IN ('online', 'offline', 'draining'))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- 列表查询：按用户找自己的 environments（Agent 模式选机器用）
CREATE INDEX IF NOT EXISTS agent_environments_user_state_idx
    ON agent_environments (user_id, state)
    WHERE state IN ('online', 'draining');
-- +goose StatementEnd

-- +goose StatementBegin
-- 池子查询：Task 模式按 worker_kind 找空闲 runtime 副本
CREATE INDEX IF NOT EXISTS agent_environments_kind_state_idx
    ON agent_environments (worker_kind, state, last_seen_at DESC)
    WHERE state = 'online';
-- +goose StatementEnd

-- +goose StatementBegin
-- Janitor 扫描：找 last_seen_at 过老的 online 行
CREATE INDEX IF NOT EXISTS agent_environments_last_seen_idx
    ON agent_environments (last_seen_at)
    WHERE state = 'online';
-- +goose StatementEnd

-- ── 2. agent_sessions：session 元数据 ────────────────────────
-- 每条记录 = 一次跨 worker 的对话 session。**不**存 frame —— 那走 NATS。
-- thread_id 关联 chat.threads（Chat / Agent 模式有；Task 模式可能 NULL）。
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_sessions (
    session_id      UUID PRIMARY KEY,
    user_id         UUID NOT NULL,
    environment_id  UUID,                                   -- NULL = chat mode (无 worker)
    thread_id       UUID,                                   -- 关联 chat.threads（可空，Task 模式不一定有 thread）
    mode            TEXT NOT NULL,                          -- 'chat' | 'agent' | 'task'
    state           TEXT NOT NULL DEFAULT 'active',         -- 'active' | 'paused' | 'completed' | 'failed'
    model           TEXT,                                   -- 创建时的 model id (e.g. "claude-3-7")
    system_prompt   TEXT,                                   -- 创建时的 system prompt（不变；可空）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_sessions_mode_chk CHECK (mode IN ('chat', 'agent', 'task')),
    CONSTRAINT agent_sessions_state_chk CHECK (state IN ('active', 'paused', 'completed', 'failed')),
    CONSTRAINT agent_sessions_env_fk FOREIGN KEY (environment_id) REFERENCES agent_environments(environment_id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
-- 用户 list session：按 mode 筛 + 时间倒序
CREATE INDEX IF NOT EXISTS agent_sessions_user_mode_created_idx
    ON agent_sessions (user_id, mode, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- environment 反查 session（debug / 死 worker 接管前看它在跑啥）
CREATE INDEX IF NOT EXISTS agent_sessions_env_state_idx
    ON agent_sessions (environment_id, state)
    WHERE state IN ('active', 'paused');
-- +goose StatementEnd

-- ── 3. agent_session_results：Task 模式最终态摘要 ──────────────
-- 仅 Task 模式 finalize 时**写一行**——chat / agent 模式走 chat.messages
-- 已有路径，不写这张表。前端展示"任务结果"页时读这张。
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_session_results (
    session_id          UUID PRIMARY KEY REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    status              TEXT NOT NULL,                      -- 'completed' | 'failed' | 'cancelled'
    final_text          TEXT,                               -- assistant 最后一条文本
    final_parts         JSONB,                              -- assistant 最后一条 parts (跟 chat.messages.parts 同结构)
    tool_calls_summary  JSONB,                              -- [{name, count, total_ms}]，UI 展示工具用量
    cost_usd            NUMERIC(12, 6),
    prompt_tokens       INTEGER,
    completion_tokens   INTEGER,
    duration_ms         BIGINT,
    error_message       TEXT,                               -- failed/cancelled 才填
    finished_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_session_results_status_chk CHECK (status IN ('completed', 'failed', 'cancelled'))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- 用户列任务结果（最近完成的）
CREATE INDEX IF NOT EXISTS agent_session_results_finished_idx
    ON agent_session_results (finished_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_session_results;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_sessions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_environments;
-- +goose StatementEnd
