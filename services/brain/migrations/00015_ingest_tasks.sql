-- +goose Up
-- +goose StatementBegin

-- ─── Brain.Wiki ingest tasks ────────────────────────────────────
-- 跟踪「把 raw source 送给 LLM 转写成 wiki page」的异步任务。
--
-- workers/wiki-llm 是消费方：订阅 NATS 主题 brain.wiki.ingest.requested
-- 后从此表取行，把任务推进到 done/failed/cancelled，期间通过 progress
-- jsonb 流式回写「已落地多少 page」「当前阶段」让 UI 能看进度条。
--
-- 状态机（见 services/brain/internal/wiki/ingest/store.go 中 ValidStatus）：
--
--   pending  → running → partial → done
--                 │         │
--                 ↓         ↓
--               failed   cancelled
--
-- partial 是 streaming partial-save 专属：worker 已经落了 N 页但流还
-- 没结束；此时进度可见、cancel 仍可中断。
--
-- 设计要点：
--   * source_id 可空：未来允许 free-form 文本 ingest 而非必须先建 source
--   * cancel_requested_at 是「软取消」信号，worker 在每个 chunk 边界检查
--   * result_pages 数组里是这次 ingest 落地的所有 page id（partial 阶段
--     就开始有，done 时定型）；UI 可点跳转
--   * progress jsonb 让 worker 自由扩字段（stage/cot_phase/eta 等），不
--     需要每次新增需求都改 schema

CREATE TABLE IF NOT EXISTS brain.ingest_tasks (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id           uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id             uuid NOT NULL,
    source_id            uuid REFERENCES brain.sources(id) ON DELETE SET NULL,
    -- 直接 ingest 的入参（用户粘贴文本时用）。source_id NULL 时必填。
    raw_text             text NOT NULL DEFAULT '',
    -- 任务标题，便于 UI 列表展示，可空时 worker 会基于 source.title 兜底
    title                text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'pending'
                              CHECK (status IN
                                ('pending','running','partial','done','failed','cancelled')),
    error                text NOT NULL DEFAULT '',
    progress             jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 累积落地的 page ID 列表，同步推进。array 比 jsonb 这里更合用：
    -- 类型固定 + 索引便宜 + 可直接 ANY/ALL 查找。
    result_pages         uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    cancel_requested_at  timestamptz,
    started_at           timestamptz,
    finished_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- 列表/详情常用查询
CREATE INDEX IF NOT EXISTS ingest_tasks_project_status_idx
    ON brain.ingest_tasks(project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS ingest_tasks_owner_idx
    ON brain.ingest_tasks(owner_id, created_at DESC);

-- worker 启动时找 stuck running 任务做超时回收用
CREATE INDEX IF NOT EXISTS ingest_tasks_running_idx
    ON brain.ingest_tasks(status, started_at)
    WHERE status IN ('running','partial');

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.ingest_tasks;
-- +goose StatementEnd
