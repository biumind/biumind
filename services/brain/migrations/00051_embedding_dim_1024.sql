-- +goose Up
-- B-1: switch the three embedding columns vector(1536) → vector(1024)
-- to match bge-m3, the chosen platform embedding model (self-hosted via
-- model-relay /v1/internal/embeddings). bge-m3 emits a fixed 1024-dim
-- vector and ignores the OpenAI `dimensions` request field, so the old
-- 1536 column (sized for text-embedding-3-small) no longer fits.
--
-- Existing vectors are discarded (SET NULL): 1536→1024 is not a lossless
-- cast, and the embed worker re-indexes every dirty row on its next pass
-- anyway. Each ivfflat index depends on the column type, so it is dropped
-- before the ALTER and recreated after, matching the original DDL
-- (cosine ops, lists=100, partial WHERE embedding IS NOT NULL).

UPDATE brain.wiki_chunks SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.wiki_chunks_embedding_ivf;
ALTER TABLE brain.wiki_chunks ALTER COLUMN embedding SET DATA TYPE vector(1024);
CREATE INDEX IF NOT EXISTS wiki_chunks_embedding_ivf
    ON brain.wiki_chunks USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

UPDATE brain.graph_nodes SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.graph_nodes_embedding_ivf;
ALTER TABLE brain.graph_nodes ALTER COLUMN embedding SET DATA TYPE vector(1024);
CREATE INDEX IF NOT EXISTS graph_nodes_embedding_ivf
    ON brain.graph_nodes USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

UPDATE brain.memories SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.memories_embedding_ivf;
ALTER TABLE brain.memories ALTER COLUMN embedding SET DATA TYPE vector(1024);
CREATE INDEX IF NOT EXISTS memories_embedding_ivf
    ON brain.memories USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- +goose Down
-- Revert the type to 1536. Same data-loss caveat: existing 1024 vectors
-- are nulled, embed worker repopulates. Requires a 1536-dim model
-- (e.g. text-embedding-3-small) configured via EMBED_MODEL/EMBED_DIMS.

UPDATE brain.wiki_chunks SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.wiki_chunks_embedding_ivf;
ALTER TABLE brain.wiki_chunks ALTER COLUMN embedding SET DATA TYPE vector(1536);
CREATE INDEX IF NOT EXISTS wiki_chunks_embedding_ivf
    ON brain.wiki_chunks USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

UPDATE brain.graph_nodes SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.graph_nodes_embedding_ivf;
ALTER TABLE brain.graph_nodes ALTER COLUMN embedding SET DATA TYPE vector(1536);
CREATE INDEX IF NOT EXISTS graph_nodes_embedding_ivf
    ON brain.graph_nodes USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

UPDATE brain.memories SET embedding = NULL WHERE embedding IS NOT NULL;
DROP INDEX IF EXISTS brain.memories_embedding_ivf;
ALTER TABLE brain.memories ALTER COLUMN embedding SET DATA TYPE vector(1536);
CREATE INDEX IF NOT EXISTS memories_embedding_ivf
    ON brain.memories USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;
