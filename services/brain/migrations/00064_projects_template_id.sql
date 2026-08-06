-- +goose Up
-- +goose StatementBegin
ALTER TABLE brain.projects ADD COLUMN template_id text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE brain.projects DROP COLUMN IF EXISTS template_id;
-- +goose StatementEnd
