-- +goose Up
-- +goose StatementBegin
-- 删 brain.wiki_lint_issues（00037）—— lint 两套收敛 (B-10)。
--
-- 结构 lint 规则统一收敛到 brain.review_items (kind=lint, migration 00016)：
-- review_items 有完整状态机 (open/resolved/dismissed) + 幂等 dedupe_key
-- (lint:<page>:<rule>[:<sub>]) + owner 隔离，重跑 ON CONFLICT DO NOTHING，
-- dismiss/resolve 后同 key 不再触发。
--
-- wiki_lint_issues 反之无状态机无 dedupe_key：services/brain/internal/wiki/lint/api.go
-- 的 runStructuralRules 每次 POST /lint/run 全表 DELETE+INSERT，用户上次清掉的
-- issue 下次跑又复活。同一「无标题页」在两套各写一行 (title-empty + untitled_page)，
-- 双 UI 入口双倍噪音。
--
-- 收敛后：wiki/lint/api.go 整删；4 SQL 结构规则移植成 reviews/lint.go 的 Go RuleID
-- (missing_frontmatter / duplicate_title / orphan_page；title-empty 被 reviews
-- untitled_page 超集覆盖直接丢弃)；semantic.go 搬 reviews 包。前端单一 Reviews
-- 入口 + POST /reviews/scan 触发端点。dedup 收敛 commit d5ae05d9 同手法，唯一
-- 差异：dedup 是无状态桩无表，本项有 00037 表须配套 DROP（仿 00057:71 sources）。
--
-- 无 FK 引用 wiki_lint_issues（全仓唯一写入点 = lint/api.go，已删），plain DROP 安全。
DROP TABLE IF EXISTS brain.wiki_lint_issues;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 不可逆：lint findings 历史数据丢弃。但 review_items (kind=lint) 是收敛后的
-- 单一来源，下次 reviews lint worker tick（12h 默认）或用户点「扫描」即重建，
-- 且状态机幂等不复活已 dismiss 的项。回滚需手动从 00037 重建空表。
CREATE TABLE IF NOT EXISTS brain.wiki_lint_issues (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_id     uuid REFERENCES brain.pages(id) ON DELETE CASCADE,
    rule_family text NOT NULL,
    kind        text NOT NULL,
    severity    text NOT NULL,
    detail      text,
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wiki_lint_issues_project_severity_idx
    ON brain.wiki_lint_issues (project_id, severity, created_at DESC);
CREATE INDEX IF NOT EXISTS wiki_lint_issues_project_family_idx
    ON brain.wiki_lint_issues (project_id, rule_family);
-- +goose StatementEnd
