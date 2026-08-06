-- +goose Up
-- +goose StatementBegin

-- Rename memory kind 'skill' → 'habit'.
--
-- The original CHECK enum included {recall, preference, skill}, where
-- "skill" meant "an inferred recurring user pattern" (e.g. user always
-- writes Conventional Commits). When the runtime.skills subsystem
-- landed (Skills-Design §11), the same word came to mean two different
-- things across services, telemetry, and Cedar policies.
--
-- Renaming to "habit" keeps the original semantics (an inferred
-- behaviour pattern) while freeing the "skill" identifier for the new
-- Agent Skill subsystem. The Go store layer keeps "skill" as a
-- deprecated alias on input for 90 days (until 2026-08-25); writes are
-- silently rewritten to "habit" before reaching this CHECK.

ALTER TABLE brain.memories DROP CONSTRAINT IF EXISTS memories_kind_check;

UPDATE brain.memories SET kind = 'habit' WHERE kind = 'skill';

ALTER TABLE brain.memories
    ADD CONSTRAINT memories_kind_check
    CHECK (kind IN ('recall', 'preference', 'habit'));

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin

ALTER TABLE brain.memories DROP CONSTRAINT IF EXISTS memories_kind_check;

UPDATE brain.memories SET kind = 'skill' WHERE kind = 'habit';

ALTER TABLE brain.memories
    ADD CONSTRAINT memories_kind_check
    CHECK (kind IN ('recall', 'preference', 'skill'));

-- +goose StatementEnd
