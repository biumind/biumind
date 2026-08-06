-- +goose Up
-- +goose StatementBegin

-- ─── Code tasks schema ────────────────────────────────────────
--
-- 编码工作台任务跨端同步, 设计文档:
--   docs/BiuMind-Code-Sync-Design.md
--   docs/BiuMind-Code-Artifacts-Sync-Design.md
--
-- 三张表:
--   * code.tasks         — 任务元数据 (title / status / cost / branch / etc)
--   * code.task_events   — 流式 agent 事件 (任务内 seq 自增)
--   * code.task_artifacts — 产物元数据 (代码 diff / 图片 / 文档...)
--
-- 注意: workspace_json 不含 device-private 的 localPath 字段 (CSY1 不变量),
-- 仅含 kind / branchName / baseCommit / displayName。
--
-- 完整二进制文件 (L3 Full Asset) 走 Brain Files 服务 (v1.5 模块, MinIO),
-- 这里仅记 cloud_file_id 引用。

CREATE SCHEMA IF NOT EXISTS code;

-- ─── tasks ────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS code.tasks (
    id                  uuid PRIMARY KEY,
    user_id             uuid NOT NULL,

    -- 任务跑在哪台 device 上 (执行绑定到 origin, 其他设备只能发指令)
    origin_device_id    text NOT NULL,
    origin_device_label text NOT NULL DEFAULT '',  -- 用户可见标签 'MacBook Pro'

    -- 用户输入
    title               text NOT NULL,
    prompt              text NOT NULL,

    -- agent 选择 + 执行参数
    agent               text NOT NULL,             -- biu | claudeCode | codex
    mode                text NOT NULL,             -- ask | autoEdit | fullAccess
    status              text NOT NULL,             -- queued | running | done | ...

    -- 累计 cost
    cost_usd            double precision NOT NULL DEFAULT 0,
    input_tokens        bigint NOT NULL DEFAULT 0,
    output_tokens       bigint NOT NULL DEFAULT 0,

    -- 结果 / 错误
    error_message       text,

    -- workspace 引用 (不含 localPath, 见 CSY1)
    workspace_json      jsonb,

    -- 对比组关联 (同 prompt 派给多 agent 时这些 task 共享 id)
    compare_group_id    uuid,

    created_at          timestamptz NOT NULL,
    completed_at        timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz                -- 软删
);

-- delta sync 主索引 (按 user_id + updated_at 拉增量)
CREATE INDEX IF NOT EXISTS idx_code_tasks_user_updated
    ON code.tasks (user_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- 对比组查询
CREATE INDEX IF NOT EXISTS idx_code_tasks_compare_group
    ON code.tasks (compare_group_id)
    WHERE compare_group_id IS NOT NULL;

-- ─── task_events ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS code.task_events (
    task_id     uuid NOT NULL REFERENCES code.tasks(id) ON DELETE CASCADE,
    seq         bigint NOT NULL,                   -- 单 task 内严格自增
    ts          timestamptz NOT NULL,
    payload     jsonb NOT NULL,                    -- AgentEvent.toJson()
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, seq)
);

-- 增量拉 events: WHERE task_id = ? AND seq > <cursor> ORDER BY seq
CREATE INDEX IF NOT EXISTS idx_task_events_seq
    ON code.task_events (task_id, seq);

-- ─── task_artifacts ───────────────────────────────────────────

CREATE TABLE IF NOT EXISTS code.task_artifacts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id             uuid NOT NULL REFERENCES code.tasks(id) ON DELETE CASCADE,
    user_id             uuid NOT NULL,             -- 反范式: 让按 user 列出更快

    -- 产物分类
    kind                text NOT NULL,             -- codeFile | image | document | audio | video | dataset | binary
    rel_path            text NOT NULL,             -- 相对 worktree 的 POSIX 路径
    mime_type           text,
    size_bytes          bigint NOT NULL,
    sha256              text NOT NULL,
    op                  text NOT NULL,             -- created | modified | deleted

    -- L2 Preview (≤ 200KB)
    preview_summary     text,                      -- "+12 -3" / "256x256 jpeg" / "1200 字"
    preview_data        bytea,                     -- 缩略图原始字节 / diff text utf-8 编码
    preview_mime_type   text,                      -- image/jpeg / text/diff / text/plain

    -- L3 Full Asset (Brain Files 服务里的 file_id, 用户主动上传才有)
    cloud_file_id       uuid,
    cloud_uploaded_at   timestamptz,

    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_artifacts_task
    ON code.task_artifacts (task_id);

CREATE INDEX IF NOT EXISTS idx_artifacts_user_recent
    ON code.task_artifacts (user_id, updated_at DESC);

-- sha256 去重: 用户上传同 sha256 的 artifact 时, 查 cloud_file_id 复用
CREATE INDEX IF NOT EXISTS idx_artifacts_user_sha256
    ON code.task_artifacts (user_id, sha256);

-- ─── task_commands (反向通道) ─────────────────────────────────
--
-- 非 origin device 发出的指令 (cancel / permission_response / 等), Brain
-- 落库 + 通过 Realtime 转发给 origin device。落库目的: 移动端断网时也能
-- 排队, origin 上线后会再拉。

CREATE TABLE IF NOT EXISTS code.task_commands (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id             uuid NOT NULL REFERENCES code.tasks(id) ON DELETE CASCADE,
    user_id             uuid NOT NULL,

    -- 命令类型: cancel | permission_response | pause | resume | retry
    cmd                 text NOT NULL,
    payload             jsonb,                     -- {decision: allow|deny, ...}

    -- 来源 device (审计 + 防止 origin 自己发自己接收)
    issued_by_device_id text NOT NULL,

    -- 执行状态: pending → consumed | expired | failed
    status              text NOT NULL DEFAULT 'pending',
    consumed_at         timestamptz,
    consumed_error      text,

    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL DEFAULT (now() + interval '5 minutes')
);

-- origin device 拉 pending 指令
CREATE INDEX IF NOT EXISTS idx_task_commands_pending
    ON code.task_commands (task_id, status, created_at)
    WHERE status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS code.task_commands;
DROP TABLE IF EXISTS code.task_artifacts;
DROP TABLE IF EXISTS code.task_events;
DROP TABLE IF EXISTS code.tasks;
DROP SCHEMA IF EXISTS code;

-- +goose StatementEnd
