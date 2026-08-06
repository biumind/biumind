-- +goose Up
-- +goose StatementBegin

-- ─── Brain.Wiki review queue ────────────────────────────────────
-- 通用「写入侧 AI 闭环」结果表：dedup / lint / sweep / merge / suggestion
-- 五类自动审阅都落到这一张，UI 走单一审阅中心。
--
-- 选择单表多 kind 而非每种一张：
--   * 5 类的字段重叠度 > 90%（都需要 page_ids + status + payload）
--   * UI 一律是「列表 + 标记已读 + 跳到关联页」流程
--   * dedupe_key 让重复扫描幂等，不论 kind 来源
--
-- dedupe_key 设计（每个 kind 自己拼）：
--   dedup:    "dedup:<min_page_uuid>:<max_page_uuid>"  pair-level
--   lint:     "lint:<page_uuid>:<rule_id>"             rule-level
--   sweep:    "sweep:<page_uuid>:<rule_id>"            rule-level
--   merge:    "merge:<min_page_uuid>:<max_page_uuid>"  pair-level
--   sugg:     "sugg:<page_uuid>:<topic_hash>"          topic-level
--
-- 存在的 dedupe_key 再次写入 → 无操作 (ON CONFLICT DO NOTHING)，让
-- 周期 worker 反复扫不会膨胀。状态机（status）：
--
--   open → resolved   用户接受了建议（执行了合并 / 修了 lint）
--        → dismissed  用户拒绝（标"不是重复"等）
--
-- 一旦 resolved/dismissed，相同 dedupe_key 后续不再触发（worker 跳过
-- 已闭环的）。重新 reopen 通过 DELETE row 重新触发即可，业务上罕见。

CREATE TABLE IF NOT EXISTS brain.review_items (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id      uuid NOT NULL,
    kind          text NOT NULL
                       CHECK (kind IN ('dedup','lint','sweep','merge','suggestion')),
    status        text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open','resolved','dismissed')),
    title         text NOT NULL,
    description   text NOT NULL DEFAULT '',
    -- 关联的 page id，dedup/merge 通常 2 个；lint/sweep 1 个；suggestion 0..N。
    page_ids      uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    -- 类型相关数据（相似度、lint 规则名、sweep 阈值天数 …）
    payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 幂等键，每 kind 自定义。UNIQUE 让 ON CONFLICT 直接生效。
    dedupe_key    text NOT NULL UNIQUE,
    resolved_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS review_items_project_status_idx
    ON brain.review_items(project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS review_items_owner_idx
    ON brain.review_items(owner_id, status);

-- 用于 UI「最近 N 条 open 审阅」查询
CREATE INDEX IF NOT EXISTS review_items_open_idx
    ON brain.review_items(created_at DESC)
    WHERE status = 'open';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.review_items;
-- +goose StatementEnd
