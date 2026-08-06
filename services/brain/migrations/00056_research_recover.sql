-- +goose Up
-- +goose StatementBegin
--
-- Deep Research recover support.
--
-- The orchestrator runs in-process (no NATS, no queue service); before
-- this change a crash between phases left a task stuck in
-- searching/synthesizing/saving forever — nothing on boot re-adopted it.
--
-- started_at  — stamped once when the task first leaves 'queued'
--               (queued → searching). Survives re-runs so the wall-clock
--               a task has been in-flight stays meaningful.
-- finished_at — stamped by Complete / Fail. Null ⇒ still active.
--
-- The partial index backs the boot-time "find stuck in-flight tasks"
-- scan: it only indexes the four active statuses, so the (growing)
-- done/error history never bloats the lookup.
ALTER TABLE brain.research_tasks
    ADD COLUMN IF NOT EXISTS started_at  timestamptz,
    ADD COLUMN IF NOT EXISTS finished_at timestamptz;

CREATE INDEX IF NOT EXISTS research_tasks_active_idx
    ON brain.research_tasks (updated_at)
    WHERE status IN ('queued', 'searching', 'synthesizing', 'saving');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS brain.research_tasks_active_idx;

ALTER TABLE brain.research_tasks
    DROP COLUMN IF EXISTS finished_at,
    DROP COLUMN IF EXISTS started_at;
-- +goose StatementEnd
