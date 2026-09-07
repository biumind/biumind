-- ============================================================================
-- 00010_agent_run_audit.sql — wiki maintain agent run 关联 + 持久化
-- （BiuMind-Agent-Experience-Design §1.2 P2）
--
-- 1. page_revisions.run_id：agent 写路径（wiki_update_page / wiki_merge_pages，
--    run 内经 tools.WithRunID 注入）快照落 run_id；人工编辑 / MCP 路径无 run
--    上下文 → NULL，天然区分 agent/人工。5 分钟窗口合并不变（合并 = 不新增行，
--    首条快照的 run_id 不被覆盖）。
-- 2. brain.agent_runs：maintain agent run 持久化（此前只有内存 AgentRuns
--    sync.Map），run_id 客户端生成（取消 / 审计对齐用），status 终态
--    done/failed/cancelled 由 handleWikiAgentRun / cancel 端点写。
--
-- 两表均无 files.objects 引用，不涉及 FGC 孤儿扫描清单。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.page_revisions
    ADD COLUMN IF NOT EXISTS run_id text;

-- 审计查询 = 按 run_id 反查快照（run 详情改动清单）；partial 索引不为人工作业
-- （run_id IS NULL 占绝大多数）付索引维护成本。
CREATE INDEX IF NOT EXISTS page_revisions_run_idx
    ON brain.page_revisions (run_id) WHERE run_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS brain.agent_runs (
    run_id       text PRIMARY KEY,           -- 客户端生成（uuid），服务端兜底生成
    project_id   uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id     uuid NOT NULL,
    mode         text NOT NULL DEFAULT '',   -- fast|standard|deep
    model        text NOT NULL DEFAULT '',
    instruction  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'done', 'failed', 'cancelled')),
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    error        text
);

CREATE INDEX IF NOT EXISTS agent_runs_project_idx
    ON brain.agent_runs (project_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS brain.agent_runs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS brain.page_revisions_run_idx;
ALTER TABLE brain.page_revisions DROP COLUMN IF EXISTS run_id;
-- +goose StatementEnd
