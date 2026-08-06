-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS runtime.tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    project_id      uuid,
    agent           text NOT NULL DEFAULT 'biu',
    prompt          text NOT NULL,
    system_prompt   text NOT NULL DEFAULT '',
    model           text NOT NULL,
    permission_mode text NOT NULL DEFAULT 'ask',
    status          text NOT NULL DEFAULT 'pending',
    error_message   text NOT NULL DEFAULT '',
    thread_id       text NOT NULL,
    run_id          text NOT NULL,
    cost_tokens_in  bigint NOT NULL DEFAULT 0,
    cost_tokens_out bigint NOT NULL DEFAULT 0,
    cost_usd_micros bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX tasks_user_status_idx ON runtime.tasks (user_id, status);
CREATE INDEX tasks_run_idx ON runtime.tasks (run_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS runtime.tasks;
