// PostgreSQL-backed feed / entry store. Schema lives in
// services/app_center/migrations/00006_rss_schema.sql.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrFeedExists = errors.New("rss: feed already subscribed")
	ErrNotFound   = errors.New("rss: not found")
	// ErrForcedFeed — a member tried to remove an org admin–forced feed.
	ErrForcedFeed = errors.New("rss: forced org subscription cannot be removed")
)

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

type AddFeedInput struct {
	Scope       string
	ScopeID     string
	FeedURL     string
	Title       string
	SiteURL     string
	Description string
	IconURL     string
	Category    string
	RefreshSec  int
	Forced      bool   // M11.4 — org admin 强制订阅
	Kind        string // M13.1 — source kind (rss/wechat/x/podcast); "" → 'rss'
}

func (s *PGStore) AddFeed(ctx context.Context, in AddFeedInput) (*Feed, error) {
	if in.RefreshSec <= 0 {
		in.RefreshSec = 1800
	}
	if in.Title == "" {
		in.Title = in.FeedURL
	}
	if in.Kind == "" {
		in.Kind = "rss"
	}
	row, err := s.pool.Query(ctx, `
		INSERT INTO rss.feeds
			(scope, scope_id, feed_url, title, site_url, description, icon_url, category, refresh_sec, forced, kind)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+feedColumns,
		in.Scope, in.ScopeID, in.FeedURL, in.Title, in.SiteURL, in.Description, in.IconURL, in.Category, in.RefreshSec, in.Forced, in.Kind)
	if err != nil {
		return nil, classifyInsertErr(err)
	}
	defer row.Close()
	if !row.Next() {
		if err := row.Err(); err != nil {
			return nil, classifyInsertErr(err)
		}
		return nil, fmt.Errorf("rss: insert feed: no row")
	}
	return scanFeed(row)
}

func (s *PGStore) GetFeed(ctx context.Context, id uuid.UUID) (*Feed, error) {
	row, err := s.pool.Query(ctx, `SELECT `+feedColumns+` FROM rss.feeds WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("rss: get feed: %w", err)
	}
	defer row.Close()
	if !row.Next() {
		return nil, ErrNotFound
	}
	return scanFeed(row)
}

func (s *PGStore) ListFeeds(ctx context.Context, scope, scopeID string) ([]*Feed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+feedColumns+`
		  FROM rss.feeds
		 WHERE scope=$1 AND scope_id=$2
		 ORDER BY created_at DESC`, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("rss: list feeds: %w", err)
	}
	defer rows.Close()
	out := make([]*Feed, 0)
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListForcedOrgFeeds returns the forced (org admin–pushed) feeds for an
// org. M11.4 — these are unioned into every member's feeds_list so a
// forced subscription shows up regardless of which tab they're on.
func (s *PGStore) ListForcedOrgFeeds(ctx context.Context, orgID string) ([]*Feed, error) {
	if orgID == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+feedColumns+`
		  FROM rss.feeds
		 WHERE scope='org' AND scope_id=$1 AND forced=true
		 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("rss: list forced org feeds: %w", err)
	}
	defer rows.Close()
	out := make([]*Feed, 0)
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RemoveFeed deletes a feed within (scope, scope_id). Forced feeds are
// protected: a non-org-admin delete (scope=user) can never match a
// forced org row, and even an org-scope caller is blocked here unless
// allowForced is set — the handler passes that only after an org_write
// authz check. We surface ErrForcedFeed so the UI can explain.
func (s *PGStore) RemoveFeed(ctx context.Context, scope, scopeID string, id uuid.UUID) error {
	// Guard: refuse to delete a forced row through the ordinary path.
	var forced bool
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(forced,false) FROM rss.feeds WHERE id=$1 AND scope=$2 AND scope_id=$3`,
		id, scope, scopeID).Scan(&forced)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("rss: remove feed lookup: %w", err)
	}
	if forced {
		return ErrForcedFeed
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rss.feeds WHERE id=$1 AND scope=$2 AND scope_id=$3`,
		id, scope, scopeID)
	if err != nil {
		return fmt.Errorf("rss: remove feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveForcedFeed deletes a forced org feed — only the org admin path
// reaches here (after rss:org_write). It's the escape hatch the guard in
// RemoveFeed deliberately blocks for everyone else.
func (s *PGStore) RemoveForcedFeed(ctx context.Context, orgID string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rss.feeds WHERE id=$1 AND scope='org' AND scope_id=$2 AND forced=true`,
		id, orgID)
	if err != nil {
		return fmt.Errorf("rss: remove forced feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DueFeeds returns feeds whose last_fetched_at + refresh_sec < now()
// (or never fetched), enabled only. Limit caps the batch the scheduler
// claims per tick.
func (s *PGStore) DueFeeds(ctx context.Context, limit int) ([]*Feed, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+feedColumns+`
		  FROM rss.feeds
		 WHERE enabled = true
		   AND (last_fetched_at IS NULL
		        OR last_fetched_at + (refresh_sec * interval '1 second') < now())
		 ORDER BY last_fetched_at NULLS FIRST
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("rss: due feeds: %w", err)
	}
	defer rows.Close()
	out := make([]*Feed, 0)
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type FetchOutcome struct {
	Etag         string
	LastModified string
	Status       string
	ErrMsg       string
	Title        string
	SiteURL      string
	IconURL      string
}

func (s *PGStore) UpdateFetchState(ctx context.Context, id uuid.UUID, o FetchOutcome) error {
	failureExpr := "consecutive_failures = 0"
	if o.Status == "error" {
		failureExpr = "consecutive_failures = consecutive_failures + 1"
	}
	q := `
		UPDATE rss.feeds
		   SET last_fetched_at = now(),
		       last_status     = $2,
		       last_error      = $3,
		       etag            = COALESCE(NULLIF($4, ''), etag),
		       last_modified   = COALESCE(NULLIF($5, ''), last_modified),
		       title           = COALESCE(NULLIF($6, ''), title),
		       site_url        = COALESCE(NULLIF($7, ''), site_url),
		       icon_url        = COALESCE(NULLIF($8, ''), icon_url),
		       updated_at      = now(),
		       ` + failureExpr + `
		 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, o.Status, o.ErrMsg, o.Etag, o.LastModified, o.Title, o.SiteURL, o.IconURL)
	if err != nil {
		return fmt.Errorf("rss: update fetch state: %w", err)
	}
	return nil
}

// InsertEntries upserts a batch into rss.entries. Returns the
// ParsedEntries that were actually inserted (ON CONFLICT DO NOTHING
// — duplicates are dropped). Inserted slice preserves input order.
func (s *PGStore) InsertEntries(ctx context.Context, feedID uuid.UUID, entries []ParsedEntry) ([]ParsedEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("rss: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	inserted := make([]ParsedEntry, 0, len(entries))
	for _, e := range entries {
		var pubAt any = nil
		if !e.PublishedAt.IsZero() {
			pubAt = e.PublishedAt
		}
		text := stripHTMLForCount(e.ContentHTML)
		wc := wordCount(text)
		readingSec := wc * 60 / 200 // 200 wpm baseline
		var encURL, encType any
		if e.EnclosureURL != "" {
			encURL = e.EnclosureURL
			encType = e.EnclosureType
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO rss.entries
				(feed_id, guid, url, title, author, content_html, content_text, published_at, hash, word_count, reading_seconds, enclosure_url, enclosure_type)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (feed_id, guid) DO NOTHING`,
			feedID, e.GUID, e.URL, e.Title, e.Author, e.ContentHTML, text, pubAt, e.TitleHash, wc, readingSec, encURL, encType)
		if err != nil {
			return nil, fmt.Errorf("rss: insert entry: %w", err)
		}
		if tag.RowsAffected() > 0 {
			inserted = append(inserted, e)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("rss: commit: %w", err)
	}
	return inserted, nil
}

type ListEntriesOpts struct {
	UnreadOnly bool
	Limit      int
	Before     time.Time
}

func (s *PGStore) ListEntries(ctx context.Context, feedID uuid.UUID, opts ListEntriesOpts) ([]*Entry, error) {
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 100
	}
	q := `SELECT ` + entryColumns + ` FROM rss.entries WHERE feed_id=$1`
	args := []any{feedID}
	if opts.UnreadOnly {
		q += ` AND read_at IS NULL`
	}
	if !opts.Before.IsZero() {
		args = append(args, opts.Before)
		q += fmt.Sprintf(` AND published_at < $%d`, len(args))
	}
	args = append(args, opts.Limit)
	q += fmt.Sprintf(` ORDER BY published_at DESC NULLS LAST, fetched_at DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("rss: list entries: %w", err)
	}
	defer rows.Close()
	out := make([]*Entry, 0)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) MarkRead(ctx context.Context, entryID uuid.UUID, read bool) error {
	q := `UPDATE rss.entries SET read_at = now() WHERE id=$1 AND read_at IS NULL`
	if !read {
		q = `UPDATE rss.entries SET read_at = NULL WHERE id=$1`
	}
	tag, err := s.pool.Exec(ctx, q, entryID)
	if err != nil {
		return fmt.Errorf("rss: mark read: %w", err)
	}
	if tag.RowsAffected() == 0 && read {
		// already read — no-op success
		return nil
	}
	return nil
}

func (s *PGStore) Star(ctx context.Context, entryID uuid.UUID, starred bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE rss.entries SET starred = $2 WHERE id=$1`, entryID, starred)
	if err != nil {
		return fmt.Errorf("rss: star: %w", err)
	}
	return nil
}

// SetReadingProgress upserts the per-user scroll position (0..1) for an
// entry. T10.4.3 — drives cross-device resume. High-frequency write, so
// it's a single keyed UPSERT into the dedicated reading_progress table
// (not an append to the reading_log ledger).
func (s *PGStore) SetReadingProgress(ctx context.Context, userID string, entryID uuid.UUID, pct float64) error {
	if pct < 0 {
		pct = 0
	} else if pct > 1 {
		pct = 1
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO rss.reading_progress (user_id, entry_id, pct, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, entry_id)
		DO UPDATE SET pct = EXCLUDED.pct, updated_at = now()`,
		userID, entryID, pct)
	if err != nil {
		return fmt.Errorf("rss: set reading progress: %w", err)
	}
	return nil
}

// GetReadingProgress returns the saved scroll position (0..1) for a
// (user, entry). Returns (0, false, nil) when no row exists yet.
func (s *PGStore) GetReadingProgress(ctx context.Context, userID string, entryID uuid.UUID) (float64, bool, error) {
	var pct float64
	err := s.pool.QueryRow(ctx,
		`SELECT pct FROM rss.reading_progress WHERE user_id=$1 AND entry_id=$2`,
		userID, entryID).Scan(&pct)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("rss: get reading progress: %w", err)
	}
	return pct, true, nil
}

// UnreadByFeed returns map[feed_id]unread_count for all enabled feeds
// in scope. Feeds with zero unread are present in the map with 0 so
// callers can assume keys cover the full feed set.
func (s *PGStore) UnreadByFeed(ctx context.Context, scope, scopeID string) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id,
		       COUNT(e.id) FILTER (WHERE e.read_at IS NULL) AS unread
		  FROM rss.feeds f
		  LEFT JOIN rss.entries e ON e.feed_id = f.id
		 WHERE f.scope = $1 AND f.scope_id = $2 AND f.enabled = true
		 GROUP BY f.id`, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("rss: unread by feed: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// UnreadCount sums unread entries across all enabled feeds in scope.
func (s *PGStore) UnreadCount(ctx context.Context, scope, scopeID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM rss.entries e
		  JOIN rss.feeds  f ON f.id = e.feed_id
		 WHERE f.scope = $1 AND f.scope_id = $2
		   AND f.enabled = true
		   AND e.read_at IS NULL`, scope, scopeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rss: unread count: %w", err)
	}
	return n, nil
}

// GC removes read non-starred entries older than 90d. Run via cron.
func (s *PGStore) GC(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rss.entries
		 WHERE read_at IS NOT NULL
		   AND starred = false
		   AND fetched_at < now() - interval '90 days'`)
	if err != nil {
		return 0, fmt.Errorf("rss: gc: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// stripHTMLForCount is a cheap (non-validating) tag remover for word
// counting — fine for length estimates, NOT safe for rendering. The
// canonical HTML→text path is the Miniflux sanitizer used elsewhere.
func stripHTMLForCount(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			out = append(out, r)
		}
	}
	return string(out)
}

// wordCount estimates word count for both Latin and CJK text. CJK
// chars are counted 1 char = 1 word (rough but fine for reading-time).
func wordCount(s string) int {
	if s == "" {
		return 0
	}
	cjk := 0
	latin := 0
	inWord := false
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
			(r >= 0x3040 && r <= 0x30FF) { // hiragana / katakana
			cjk++
			inWord = false
			continue
		}
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			inWord = false
		} else if !inWord {
			latin++
			inWord = true
		}
	}
	return cjk + latin
}

func classifyInsertErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrFeedExists
	}
	return fmt.Errorf("rss: insert feed: %w", err)
}

// ─── scanners ─────────────────────────────────────────────────────

const feedColumns = `id, scope, scope_id, feed_url, site_url, title, description,
	COALESCE(icon_url,''), COALESCE(category,''), refresh_sec,
	COALESCE(etag,''), COALESCE(last_modified,''),
	COALESCE(last_fetched_at, '0001-01-01'::timestamptz), last_status, last_error,
	consecutive_failures, enabled, COALESCE(forced,false), created_at, updated_at,
	COALESCE(kind,'rss')`

const entryColumns = `id, feed_id, guid, COALESCE(url,''), title,
	COALESCE(author,''), COALESCE(content_html,''), COALESCE(content_text,''),
	COALESCE(published_at, '0001-01-01'::timestamptz),
	fetched_at, COALESCE(read_at, '0001-01-01'::timestamptz),
	starred, hash,
	COALESCE(ai_takeaway, ''),
	COALESCE(ai_bullets, '[]'::jsonb),
	COALESCE(ai_topics, '{}'::text[]),
	ai_importance,
	COALESCE(ai_lang, ''),
	COALESCE(ai_processed_at, '0001-01-01'::timestamptz),
	COALESCE(ai_error, ''),
	COALESCE(word_count, 0),
	COALESCE(reading_seconds, 0),
	COALESCE(enclosure_url, ''), COALESCE(enclosure_type, ''),
	COALESCE(transcribed_at, '0001-01-01'::timestamptz),
	COALESCE(transcript_segments, '[]'::jsonb)`

func scanFeed(r pgx.Rows) (*Feed, error) {
	var f Feed
	err := r.Scan(
		&f.ID, &f.Scope, &f.ScopeID, &f.FeedURL, &f.SiteURL, &f.Title, &f.Description,
		&f.IconURL, &f.Category, &f.RefreshSec,
		&f.Etag, &f.LastModified,
		&f.LastFetchedAt, &f.LastStatus, &f.LastError,
		&f.ConsecutiveFailures, &f.Enabled, &f.Forced, &f.CreatedAt, &f.UpdatedAt,
		&f.Kind,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: scan feed: %w", err)
	}
	if f.LastFetchedAt.Year() < 2 {
		f.LastFetchedAt = time.Time{}
	}
	return &f, nil
}

func scanEntry(r pgx.Rows) (*Entry, error) {
	var e Entry
	var bulletsJSON []byte
	err := r.Scan(
		&e.ID, &e.FeedID, &e.GUID, &e.URL, &e.Title,
		&e.Author, &e.ContentHTML, &e.ContentText,
		&e.PublishedAt,
		&e.FetchedAt, &e.ReadAt,
		&e.Starred, &e.Hash,
		&e.AITakeaway, &bulletsJSON, &e.AITopics,
		&e.AIImportance, &e.AILang, &e.AIProcessedAt, &e.AIError,
		&e.WordCount, &e.ReadingSeconds,
		&e.EnclosureURL, &e.EnclosureType, &e.TranscribedAt,
		&e.TranscriptSegments,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: scan entry: %w", err)
	}
	if len(bulletsJSON) > 0 {
		_ = json.Unmarshal(bulletsJSON, &e.AIBullets)
	}
	if e.PublishedAt.Year() < 2 {
		e.PublishedAt = time.Time{}
	}
	if e.ReadAt.Year() < 2 {
		e.ReadAt = time.Time{}
	}
	if e.AIProcessedAt.Year() < 2 {
		e.AIProcessedAt = time.Time{}
	}
	if e.TranscribedAt.Year() < 2 {
		e.TranscribedAt = time.Time{}
	}
	return &e, nil
}
