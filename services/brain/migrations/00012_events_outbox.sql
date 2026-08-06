-- +goose Up
-- Transactional outbox poller — durable bridge from brain.events to NATS.
--
-- The events row is already written in the same tx as the underlying data
-- change (atomicity), but until now the only consumer was a LISTEN/NOTIFY
-- listener inside this process. That has zero durability:
--   * NOTIFY is delivered only to currently-LISTENing connections; events
--     emitted during a Brain restart window are silently lost.
--   * Process crash between tx COMMIT and notification dispatch loses the
--     event entirely.
--   * Multiple Brain replicas all LISTEN → each event is published N times
--     to NATS (duplicates).
--
-- The poller fixes all three by treating the events table as an outbox:
-- every row carries a published_at marker; a poller scans WHERE
-- published_at IS NULL with FOR UPDATE SKIP LOCKED so multiple replicas
-- safely share work, publishes via the same publisher.Tee, then marks
-- the row published. Independent of LISTEN/NOTIFY entirely (the listener
-- stays as a low-latency fast-path; the poller is the durability floor).
--
-- Backfill choice: existing rows are stamped with current time so the
-- poller doesn't replay every Wiki write since the dawn of the schema
-- on first boot. If you need to replay history, NULL out manually:
--   UPDATE brain.events SET published_at = NULL WHERE id > <cutoff>;

ALTER TABLE brain.events
    ADD COLUMN IF NOT EXISTS published_at timestamptz;

UPDATE brain.events
   SET published_at = created_at
 WHERE published_at IS NULL;

-- Partial index — scans only the unpublished tail. Keeps the index tiny
-- (almost-empty in steady state) and the poller's SELECT cheap regardless
-- of how large brain.events grows.
CREATE INDEX IF NOT EXISTS events_outbox_pending_idx
    ON brain.events (id)
 WHERE published_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS brain.events_outbox_pending_idx;
ALTER TABLE brain.events DROP COLUMN IF EXISTS published_at;
