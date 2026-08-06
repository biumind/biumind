-- +goose Up
-- +goose StatementBegin

-- ─── Brain.Graph schema ───────────────────────────────────────────
-- Knowledge-graph nodes extracted from wiki content.
-- A node represents a distinct concept / entity / tag inside a project.
-- Identity is `(project_id, kind, lower(name))`.
--
-- pgvector + ltree are loaded by the test-env extensions migration; on
-- environments without ltree the GIST index here will fail loudly so we
-- catch the misconfiguration early instead of degrading silently.

CREATE TABLE IF NOT EXISTS brain.graph_nodes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    name        text NOT NULL,
    aliases     text[] NOT NULL DEFAULT '{}',
    summary     text NOT NULL DEFAULT '',
    -- Embedding is opt-in: written by P4.1.5+ LLM extractor. NULL until then.
    embedding   vector(1536),
    -- ltree breadcrumb (e.g. `topic.programming.rust`) so subgraphs can be
    -- filtered with `path <@ 'topic.programming'`. Optional.
    path        ltree,
    weight      real NOT NULL DEFAULT 1.0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, kind, name)
);

CREATE INDEX IF NOT EXISTS graph_nodes_project_kind_idx
    ON brain.graph_nodes(project_id, kind);

CREATE INDEX IF NOT EXISTS graph_nodes_path_gist
    ON brain.graph_nodes USING GIST (path)
    WHERE path IS NOT NULL;

-- ivfflat needs >= ~1k rows to be effective; create lazily.
-- For dev / small projects a brute-force scan is fine.
CREATE INDEX IF NOT EXISTS graph_nodes_embedding_ivf
    ON brain.graph_nodes USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- Typed directed edges between nodes (or between a page and a node).
-- `src_id` and `dst_id` reference graph_nodes.id OR brain.pages.id — we
-- intentionally don't FK them because the graph layer treats pages as
-- nodes, and page deletion already cascades to graph_block_nodes.
CREATE TABLE IF NOT EXISTS brain.graph_edges (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    src_id            uuid NOT NULL,
    dst_id            uuid NOT NULL,
    relation          text NOT NULL,
    weight            real NOT NULL DEFAULT 1.0,
    evidence_block_id uuid REFERENCES brain.blocks(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, src_id, dst_id, relation)
);

CREATE INDEX IF NOT EXISTS graph_edges_src_idx
    ON brain.graph_edges(src_id);
CREATE INDEX IF NOT EXISTS graph_edges_dst_idx
    ON brain.graph_edges(dst_id);
CREATE INDEX IF NOT EXISTS graph_edges_project_relation_idx
    ON brain.graph_edges(project_id, relation);

-- Junction: which blocks contain which nodes (drives backlinks).
CREATE TABLE IF NOT EXISTS brain.graph_block_nodes (
    block_id   uuid NOT NULL REFERENCES brain.blocks(id) ON DELETE CASCADE,
    node_id    uuid NOT NULL REFERENCES brain.graph_nodes(id) ON DELETE CASCADE,
    confidence real NOT NULL DEFAULT 1.0,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (block_id, node_id)
);

CREATE INDEX IF NOT EXISTS graph_block_nodes_node_idx
    ON brain.graph_block_nodes(node_id);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.graph_block_nodes;
DROP TABLE IF EXISTS brain.graph_edges;
DROP TABLE IF EXISTS brain.graph_nodes;
-- +goose StatementEnd
