-- +goose Up
-- +goose StatementBegin
-- 项目 lint 报告。结构规则（不依赖 LLM）由 brain 启动时定义并按需运行；
-- 语义规则（依赖 LLM）由 worker 跑写入此表。任何 issue resolve 后从这里删；
-- 重复 lint 跑会覆盖（DELETE + bulk INSERT）整组同 kind issue。

CREATE TABLE IF NOT EXISTS brain.wiki_lint_issues (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_id       uuid REFERENCES brain.pages(id) ON DELETE CASCADE,
    -- structural | semantic
    rule_family   text NOT NULL,
    -- title-empty | orphan-page | missing-frontmatter | broken-wikilink | ...
    kind          text NOT NULL,
    severity      text NOT NULL DEFAULT 'warning',
    detail        text NOT NULL DEFAULT '',
    metadata      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wiki_lint_issues_project_idx
    ON brain.wiki_lint_issues(project_id, severity, created_at DESC);
CREATE INDEX wiki_lint_issues_family_idx
    ON brain.wiki_lint_issues(project_id, rule_family);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.wiki_lint_issues;
-- +goose StatementEnd
