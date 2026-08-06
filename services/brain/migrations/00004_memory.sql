-- +goose Up
-- +goose StatementBegin

-- ─── Brain.Memory schema ──────────────────────────────────────────
-- Multi-layer memory store. Three layers, all sharing one table:
--   * recall      — durable facts the agent should remember
--   * preference  — user-stated likes / formatting / tone
--   * skill       — captured how-to / playbook / shortcut
--
-- Embeddings are opt-in (NULL until B2 ingest worker fills them).
-- Recall today is BM25-via-ILIKE; once embeddings land we'll switch to
-- pgvector cosine with BM25 as a fallback.

CREATE TABLE IF NOT EXISTS brain.memories (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id       uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id         uuid NOT NULL,
    kind             text NOT NULL DEFAULT 'recall'
                          CHECK (kind IN ('recall', 'preference', 'skill')),
    content          text NOT NULL,
    -- Optional pgvector embedding; populated by ingest worker.
    embedding        vector(1536),
    -- 0..1 score; updated by writes and decayed by reads (B2 territory).
    salience         real NOT NULL DEFAULT 0.5,
    last_accessed_at timestamptz NOT NULL DEFAULT now(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memories_project_owner_kind_idx
    ON brain.memories(project_id, owner_id, kind);

CREATE INDEX IF NOT EXISTS memories_recent_idx
    ON brain.memories(project_id, last_accessed_at DESC);

-- ivfflat for embedding lookup. Becomes effective once we have ~1k rows.
-- Brute force scan is fine for dev / small projects.
CREATE INDEX IF NOT EXISTS memories_embedding_ivf
    ON brain.memories USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.memories;
-- +goose StatementEnd
