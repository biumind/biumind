-- +goose Up
-- +goose StatementBegin

-- Materialize tsvector on pages: title gets weight A, frontmatter B (lower).
ALTER TABLE brain.pages ADD COLUMN IF NOT EXISTS tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('biumind_zhcn', coalesce(title, '')), 'A')
  ) STORED;
CREATE INDEX IF NOT EXISTS pages_tsv_gin ON brain.pages USING GIN (tsv) WHERE deleted_at IS NULL;

-- Materialize tsvector on blocks: extract `text` field from JSON content.
ALTER TABLE brain.blocks ADD COLUMN IF NOT EXISTS tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('biumind_zhcn',
      coalesce(content->>'text', '') ||
      ' ' ||
      coalesce(content->>'caption', '')
    ), 'B')
  ) STORED;
CREATE INDEX IF NOT EXISTS blocks_tsv_gin ON brain.blocks USING GIN (tsv) WHERE deleted_at IS NULL;

-- Future: pgvector embedding columns
-- ALTER TABLE brain.pages ADD COLUMN embedding vector(1024);
-- ALTER TABLE brain.blocks ADD COLUMN embedding vector(1024);
-- CREATE INDEX pages_embed_ivfflat ON brain.pages USING ivfflat (embedding vector_cosine_ops);

-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS brain.blocks_tsv_gin;
DROP INDEX IF EXISTS brain.pages_tsv_gin;
ALTER TABLE brain.blocks DROP COLUMN IF EXISTS tsv;
ALTER TABLE brain.pages DROP COLUMN IF EXISTS tsv;
