-- +goose Up
-- +goose StatementBegin
-- 用户反馈 / 路线图。无项目维度（属于 BiuMind 平台级反馈渠道）。
-- 任何用户可读公开列表；只有 author 能编辑/删自己的；vote 一人一票。

CREATE TABLE IF NOT EXISTS brain.wiki_suggestions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id    uuid NOT NULL,
    title        text NOT NULL,
    body         text NOT NULL DEFAULT '',
    -- feature | bug | idea | other
    category     text NOT NULL DEFAULT 'feature',
    -- open | planned | shipped | rejected
    status       text NOT NULL DEFAULT 'open',
    deleted_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wiki_suggestions_status_idx
    ON brain.wiki_suggestions(status, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX wiki_suggestions_author_idx
    ON brain.wiki_suggestions(author_id)
    WHERE deleted_at IS NULL;

-- 一人一票（多次 toggle 走 INSERT/DELETE 而非 count）
CREATE TABLE IF NOT EXISTS brain.wiki_suggestion_votes (
    suggestion_id  uuid NOT NULL REFERENCES brain.wiki_suggestions(id) ON DELETE CASCADE,
    voter_id       uuid NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (suggestion_id, voter_id)
);
CREATE INDEX wiki_suggestion_votes_voter_idx
    ON brain.wiki_suggestion_votes(voter_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.wiki_suggestion_votes;
DROP TABLE IF EXISTS brain.wiki_suggestions;
-- +goose StatementEnd
