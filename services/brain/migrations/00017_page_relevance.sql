-- +goose Up
-- +goose StatementBegin

-- ─── Page-to-page relevance ─────────────────────────────────────
-- Pre-computed relatedness score per ordered page pair, derived from
-- the wikilink graph + page type affinity. Backs:
--
--   * GET /v1/wiki/pages/{id}/related        sidebar / "see also"
--   * MCP  wiki.related_pages                AI client deep dives
--   * search/api  RRF 4th retrieval path     (P2-C-2 follow-up)
--
-- Derived from knowcode/llm_wiki's graph-relevance scheme but trimmed
-- to the 3 signals biumind has the source data for:
--
--   1. Direct wikilink     ×3.0   blocks of A reference [[B]] (or B→A)
--   2. Adamic-Adar         ×1.5   shared neighbors weighted 1/log(deg)
--   3. Type affinity       ×1.0   frontmatter.type pair affinity matrix
--
-- (knowcode also ships "source overlap" ×4.0 — biumind's sources table
-- is one-to-one with pages, so the signal degenerates to either-equal
-- or independent. Deferred until we add source_pages many-to-many.)
--
-- Worker writes are batch: DELETE WHERE page_a = X then bulk-insert
-- the new rows for that page. We don't UPDATE because partial updates
-- leave stale neighbours behind when the wikilink graph shrinks.
--
-- Storage shape: directed (page_a, page_b) with page_a < page_b string-
-- comparison so each pair appears once. Lookup against a given page
-- needs UNION over both columns — that costs one extra index scan but
-- halves the row count on big projects.

CREATE TABLE IF NOT EXISTS brain.page_relevance (
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_a      uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    page_b      uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    score       real NOT NULL,
    -- Per-signal contribution for debug + future re-ranking. Schema is
    -- free-form jsonb so we can add signals without migrations.
    signals     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, page_a, page_b),
    -- Enforce undirected canonicalisation in the table itself so a
    -- buggy worker can't insert (B,A) when (A,B) already exists.
    CHECK (page_a::text < page_b::text)
);

-- One-direction lookup (page_a = X). Score DESC for ranked queries.
CREATE INDEX IF NOT EXISTS page_relevance_a_score_idx
    ON brain.page_relevance(page_a, score DESC);

-- Other-direction lookup (page_b = X). The query unions both indexes.
CREATE INDEX IF NOT EXISTS page_relevance_b_score_idx
    ON brain.page_relevance(page_b, score DESC);

-- Project-wide pruning (e.g. drop everything for a project we're
-- about to recompute from scratch).
CREATE INDEX IF NOT EXISTS page_relevance_project_idx
    ON brain.page_relevance(project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.page_relevance;
-- +goose StatementEnd
