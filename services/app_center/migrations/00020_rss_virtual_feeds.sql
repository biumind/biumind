-- M13.1 (v3): source kind label on rss.feeds.
--
-- 'rss' (default, also covers youtube/github/generic — those are still
-- normal HTTP-fetched feeds, the kind here is a *display* label) plus the
-- multi-source kinds introduced in M13:
--   'wechat'  — 公众号, fetched via a third-party RSS relay (werss / wewe-rss)
--   'x'       — Twitter/X user timeline, fetched via a Nitter instance
--   'podcast' — RSS feed whose entries carry audio enclosures
--
-- All of these still hold a real HTTP feed_url and go through the normal
-- scheduler fetch path; the column only drives the inbox source badge and
-- (forward-looking) lets the scheduler skip feeds whose feed_url isn't
-- HTTP-fetchable (a future newsletter/virtual alias like mailto:<alias>).

-- +goose Up
ALTER TABLE rss.feeds ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'rss';

-- +goose Down
ALTER TABLE rss.feeds DROP COLUMN IF EXISTS kind;
