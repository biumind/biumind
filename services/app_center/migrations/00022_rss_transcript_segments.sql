-- M13.5 Tier2 (v3): sentence-level transcript segments for synced playback.
--
-- The transcribe worker already writes the flat transcript into
-- entries.content_text (feeds search + AI digest). Here we additionally
-- persist the timestamped segments [{id,start,end,text}] returned by the ASR
-- provider (paraformer sentences / whisper segments), so the reader can:
--   • render the transcript as tappable sentences (tap → seek audio)
--   • highlight the sentence currently being spoken as audio plays
-- jsonb (not a child table) — segments are read/written as one blob per
-- entry, never queried individually.

-- +goose Up
ALTER TABLE rss.entries ADD COLUMN IF NOT EXISTS transcript_segments jsonb;

-- +goose Down
ALTER TABLE rss.entries DROP COLUMN IF EXISTS transcript_segments;
