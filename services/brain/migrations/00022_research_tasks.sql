-- +goose Up
-- +goose StatementBegin
--
-- Deep Research tasks (LLM_WIKI port).
--
-- A research task is "topic in → wiki page out". The pipeline:
--   1. web-search the topic (and optional refined queries)
--   2. LLM synthesises results into a markdown wiki page
--   3. page is created in brain.pages + brain.blocks
--
-- The task row tracks the journey so the UI can show "searching… /
-- synthesizing… / saving… / done | error" without holding the HTTP
-- connection open for the full 30-90 s the LLM call takes.
--
-- status enum (text, not pg enum because we evolve it):
--   queued       — accepted, not started
--   searching    — pulling web results
--   synthesizing — LLM is composing the page
--   saving       — writing pages/blocks rows
--   done         — page_id is populated
--   error        — error_message is populated

CREATE TABLE IF NOT EXISTS brain.research_tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id        uuid NOT NULL,
    topic           text NOT NULL,
    queries         text[] NOT NULL DEFAULT ARRAY[]::text[],
    status          text NOT NULL DEFAULT 'queued',
    page_id         uuid REFERENCES brain.pages(id) ON DELETE SET NULL,
    web_results     jsonb NOT NULL DEFAULT '[]'::jsonb,
    synthesis       text NOT NULL DEFAULT '',
    error_message   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS research_tasks_project_idx
    ON brain.research_tasks (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS research_tasks_owner_idx
    ON brain.research_tasks (owner_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.research_tasks;
-- +goose StatementEnd
