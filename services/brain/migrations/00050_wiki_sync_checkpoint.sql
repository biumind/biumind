-- +goose Up
-- wiki_sync_checkpoints — per-(user, project) last-seen change_id for
-- client sync catch-up (knowcode-style resume after reconnect).
-- Previously held in an in-memory map (B-3 bug: lost on brain restart);
-- this table makes it durable.
CREATE TABLE IF NOT EXISTS brain.wiki_sync_checkpoints (
    user_id    uuid        NOT NULL,
    project_id uuid        NOT NULL,
    change_id  text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, project_id)
);

-- +goose Down
DROP TABLE IF EXISTS brain.wiki_sync_checkpoints;
