-- +goose Up
-- +goose StatementBegin

-- update_of_id — pointer from a staged skill to the previously-active
-- row it intends to replace. Set on propose when client carries
-- `update_of`; null on fresh skills. Lets the approver UI render a
-- diff against the predecessor without round-tripping through the
-- propose response cache.
--
-- Self-referential FK is intentional. Loop detection isn't needed:
--   - propose flow only sets it once at creation;
--   - approve flow doesn't touch it (the staged row stays linked to
--     its predecessor for audit even after activation).
-- ON DELETE SET NULL because the predecessor might be hard-deleted
-- (rare, but admin tooling exists). Losing the pointer is fine —
-- the staged row stands on its own.

ALTER TABLE runtime.skills
    ADD COLUMN IF NOT EXISTS update_of_id text
        REFERENCES runtime.skills(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS skills_update_of_idx
    ON runtime.skills (update_of_id)
    WHERE update_of_id IS NOT NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS runtime.skills_update_of_idx;
ALTER TABLE runtime.skills DROP COLUMN IF EXISTS update_of_id;
-- +goose StatementEnd
