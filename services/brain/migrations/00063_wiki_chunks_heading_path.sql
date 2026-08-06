-- +goose Up
-- +goose StatementBegin
ALTER TABLE brain.wiki_chunks ADD COLUMN heading_path text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE brain.wiki_chunks DROP COLUMN IF EXISTS heading_path;
-- +goose StatementEnd
