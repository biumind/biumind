-- ─── Repo Apps (M1)：GitHub 开源项目作为应用安装 ────────────────────
--
-- 设计文档：docs/BiuMind-AppCenter-GitHub-Repo-Apps-TechPlan.md §2.8。
--
--   * app_center.apps 扩 6 列（tier / repo_meta / adapter /
--     adapter_source / index_entry / verification，全部可空）。
--   * source 的 CHECK 约束扩三个 GitHub 来源值（baseline 里该约束是
--     内联 CHECK、未显式命名，PG 自动命名为 apps_source_check）。
--   * 新表 app_center.repo_builds：每次构建/重部署一行，
--     install 删除时级联清。
--   * repo_apps_due_poll_idx：release 轮询器到期扫描的 partial index，
--     只覆盖 gh_* 行（照 rankings.boards_due_idx 的 partial 惯例）。

-- +goose Up
-- +goose StatementBegin

ALTER TABLE app_center.apps
    ADD COLUMN IF NOT EXISTS tier           text,      -- official|community|private（M2.5 起用）
    ADD COLUMN IF NOT EXISTS repo_meta      jsonb,     -- url/ref/sha/etag/poll 状态/latest_ref/update_available
    ADD COLUMN IF NOT EXISTS adapter        jsonb,
    ADD COLUMN IF NOT EXISTS adapter_source text,      -- upstream|index|auto
    ADD COLUMN IF NOT EXISTS index_entry    text,
    ADD COLUMN IF NOT EXISTS verification   jsonb;

ALTER TABLE app_center.apps DROP CONSTRAINT apps_source_check;
ALTER TABLE app_center.apps ADD CONSTRAINT apps_source_check
    CHECK (source IN ('bundled', 'org', 'marketplace', 'user_webview',
                      'gh_official', 'gh_community', 'gh_private'));

CREATE TABLE IF NOT EXISTS app_center.repo_builds (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    install_id  uuid NOT NULL REFERENCES app_center.installations(id) ON DELETE CASCADE,

    ref         text NOT NULL,                          -- release tag / branch
    sha         text NOT NULL,                          -- resolved commit sha
    status      text NOT NULL
                     CHECK (status IN ('queued', 'building', 'deploying', 'live', 'failed')),
    log_ref     text,                                   -- runner 日志位置（实例目录路径 / CAS hash）
    duration_ms int,

    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS repo_builds_install_recent_idx
    ON app_center.repo_builds (install_id, created_at DESC);

-- 轮询到期查询（§2.5）：
--   WHERE source LIKE 'gh\_%' AND (repo_meta->>'next_poll_at')::timestamptz < now()
-- LIKE 里 \_ 是字面下划线（反斜杠是 LIKE 默认转义符）。
CREATE INDEX IF NOT EXISTS repo_apps_due_poll_idx
    ON app_center.apps ((repo_meta->>'next_poll_at'))
    WHERE source LIKE 'gh\_%';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS app_center.repo_apps_due_poll_idx;
DROP TABLE IF EXISTS app_center.repo_builds;

ALTER TABLE app_center.apps DROP CONSTRAINT apps_source_check;
ALTER TABLE app_center.apps ADD CONSTRAINT apps_source_check
    CHECK (source IN ('bundled', 'org', 'marketplace', 'user_webview'));

ALTER TABLE app_center.apps
    DROP COLUMN IF EXISTS tier,
    DROP COLUMN IF EXISTS repo_meta,
    DROP COLUMN IF EXISTS adapter,
    DROP COLUMN IF EXISTS adapter_source,
    DROP COLUMN IF EXISTS index_entry,
    DROP COLUMN IF EXISTS verification;

-- +goose StatementEnd
