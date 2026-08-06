-- M13.5 (v3): audio enclosure capture for podcast transcription.
--
-- Podcast RSS feeds carry an <enclosure url=... type="audio/mpeg"> per item.
-- We previously dropped enclosures in the miniflux→ParsedEntry projection.
-- Store the first audio enclosure so the transcribe worker can pick it up
-- and so the reader can surface the audio source.
--
-- transcribed_at marks entries whose content_text was filled by the
-- transcribe worker (vs. the feed's own body) — used both to skip already
-- done episodes and to avoid the AI digest treating an empty body as final.

-- +goose Up
ALTER TABLE rss.entries ADD COLUMN IF NOT EXISTS enclosure_url  text;
ALTER TABLE rss.entries ADD COLUMN IF NOT EXISTS enclosure_type text;
ALTER TABLE rss.entries ADD COLUMN IF NOT EXISTS transcribed_at timestamptz;

-- Backfill worker scan: audio entries not yet transcribed. Partial index
-- keeps it tiny (only podcast entries qualify).
CREATE INDEX IF NOT EXISTS entries_untranscribed_idx
    ON rss.entries (fetched_at)
    WHERE enclosure_url IS NOT NULL AND transcribed_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS rss.entries_untranscribed_idx;
ALTER TABLE rss.entries DROP COLUMN IF EXISTS transcribed_at;
ALTER TABLE rss.entries DROP COLUMN IF EXISTS enclosure_type;
ALTER TABLE rss.entries DROP COLUMN IF EXISTS enclosure_url;
