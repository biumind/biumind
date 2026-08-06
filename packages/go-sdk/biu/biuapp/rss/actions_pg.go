// PG-backed action handlers. Wired in when App is constructed via
// NewWithPool. Each handler reads caller identity from context (claims
// injected by api.Server.requireAuth) so the multi-tenant boundary
// stays in the data layer.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

var ErrNoCaller = errors.New("rss: no caller identity in context")

func callerScope(ctx context.Context) (string, string, error) {
	claims, ok := bauth.ClaimsFrom(ctx)
	if !ok || claims == nil || claims.UserID == "" {
		return "", "", ErrNoCaller
	}
	return "user", claims.UserID, nil
}

// ─── feeds.* ──────────────────────────────────────────────────────

type addFeedInput struct {
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	Category   string `json:"category,omitempty"`
	RefreshSec int    `json:"refresh_sec,omitempty"`
	Scope      string `json:"scope,omitempty"`
	// Kind — M13.3/13.4: when the client adds via the 公众号 / X tab it
	// has already constructed the final relay feed URL, so it passes an
	// explicit kind ('wechat'/'x'/'podcast') and we skip auto-discovery
	// (the URL is final, not a website to probe).
	Kind string `json:"kind,omitempty"`
}

func (a *App) invokeFeedsAdd(ctx context.Context, raw json.RawMessage) (any, error) {
	var in addFeedInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.URL == "" {
		return nil, errors.New("rss: feeds.add requires url")
	}
	scope, scopeID, err := a.resolveScope(ctx, in.Scope, true)
	if err != nil {
		return nil, err
	}

	// M5 — detect source kind + discover feed URL when user pasted a
	// website / channel URL instead of a feed URL.
	//
	// M13.3/13.4 — when the client supplies an explicit kind (公众号 / X /
	// podcast), the relay URL is already final; skip discovery so we don't
	// try to probe the relay host for a <link rel=alternate>.
	originalURL := in.URL
	detectedKind := KindRSS
	feedKind := strings.ToLower(in.Kind)
	switch feedKind {
	case "", "rss":
		feedKind = ""
		if a.discover != nil {
			dr, dErr := a.discover.Discover(ctx, in.URL)
			if dErr == nil && dr.FeedURL != "" {
				in.URL = dr.FeedURL
				detectedKind = dr.Kind
			}
		}
	}

	// Mainstream RSS reader behaviour: fetch the upstream once
	// synchronously so we can (a) validate the URL really IS a
	// parseable feed before persisting (Inoreader / Feedly pattern),
	// (b) populate title / site_url / icon_url / first batch of
	// entries so the user sees content immediately rather than
	// waiting on the next cron tick. On fetch failure we still
	// persist the row but flag it as error so the user can see
	// what went wrong + retry.
	title := in.Title
	siteURL := ""
	desc := ""
	iconURL := ""
	var firstFetch *FetchResult
	if a.fetcher != nil {
		res, fErr := a.fetcher.Fetch(ctx, FetchRequest{FeedURL: in.URL})
		if fErr == nil {
			firstFetch = res
			if title == "" {
				title = res.Title
			}
			siteURL = res.SiteURL
			desc = res.Description
			iconURL = res.IconURL
		}
	}
	if title == "" {
		title = in.URL
	}

	f, err := a.pg.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: scopeID,
		FeedURL:     in.URL,
		Title:       title,
		SiteURL:     siteURL,
		Description: desc,
		IconURL:     iconURL,
		Category:    in.Category,
		RefreshSec:  in.RefreshSec,
		Kind:        feedKind,
	})
	if err != nil {
		return nil, err
	}

	// Ingest first batch + stamp fetch state so the feed isn't
	// flagged as "等待首次抓取" in the UI on the very next render.
	if firstFetch != nil && !firstFetch.NotModified {
		_, _ = a.pg.InsertEntries(ctx, f.ID, firstFetch.Entries)
		_ = a.pg.UpdateFetchState(ctx, f.ID, FetchOutcome{
			Status:       "ok",
			Etag:         firstFetch.Etag,
			LastModified: firstFetch.LastModified,
			Title:        firstFetch.Title,
			SiteURL:      firstFetch.SiteURL,
			IconURL:      firstFetch.IconURL,
		})
		// Re-read so the response carries the updated last_fetched_at /
		// last_status / icon_url fields too.
		if updated, gErr := a.pg.GetFeed(ctx, f.ID); gErr == nil {
			f = updated
		}
	}
	out := feedJSON(f)
	if detectedKind != KindRSS {
		out["detected_kind"] = string(detectedKind)
		out["original_url"] = originalURL
	}
	return out, nil
}

func (a *App) invokeFeedsList(ctx context.Context, raw json.RawMessage) (any, error) {
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	feeds, err := a.pg.ListFeeds(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	// M11.4: in the user view, union in the org's forced subscriptions so
	// they're always visible. (In the org/team view they're already part
	// of the scope=org list, so we only union for user scope.)
	if scope == "user" {
		if claims, _ := bauth.ClaimsFrom(ctx); claims != nil && claims.OrgID != "" {
			forced, fErr := a.pg.ListForcedOrgFeeds(ctx, claims.OrgID)
			if fErr != nil {
				return nil, fErr
			}
			feeds = append(feeds, forced...)
		}
	}
	unreadByFeed, err := a.pg.UnreadByFeed(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(feeds))
	for i, f := range feeds {
		fj := feedJSON(f)
		fj["unread"] = unreadByFeed[f.ID]
		items[i] = fj
	}
	return map[string]any{"items": items}, nil
}

func (a *App) invokeFeedsRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad feed id: %w", err)
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), true)
	if err != nil {
		return nil, err
	}
	if err := a.pg.RemoveFeed(ctx, scope, scopeID, id); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeFeedsRefresh(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.sched == nil {
		return nil, errors.New("rss: scheduler not wired")
	}
	stats, err := a.sched.RefreshAll(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"considered":   stats.Considered,
		"ok":           stats.OK,
		"not_modified": stats.NotMod,
		"errors":       stats.Errors,
		"new_entries":  stats.NewEntries,
	}, nil
}

// ─── entries.* ────────────────────────────────────────────────────

type listEntriesInput struct {
	FeedID     string `json:"feed_id,omitempty"`
	UnreadOnly bool   `json:"unread_only,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

func (a *App) invokeEntriesList(ctx context.Context, raw json.RawMessage) (any, error) {
	var in listEntriesInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("rss: bad input: %w", err)
		}
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}

	feeds, err := a.pg.ListFeeds(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	feedIDs := map[uuid.UUID]bool{}
	for _, f := range feeds {
		feedIDs[f.ID] = true
	}

	var targets []uuid.UUID
	if in.FeedID != "" {
		fid, err := uuid.Parse(in.FeedID)
		if err != nil {
			return nil, fmt.Errorf("rss: bad feed id: %w", err)
		}
		if !feedIDs[fid] {
			return nil, ErrNotFound
		}
		targets = []uuid.UUID{fid}
	} else {
		for id := range feedIDs {
			targets = append(targets, id)
		}
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	all := make([]map[string]any, 0)
	for _, fid := range targets {
		entries, err := a.pg.ListEntries(ctx, fid, ListEntriesOpts{
			UnreadOnly: in.UnreadOnly,
			Limit:      limit,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			all = append(all, entryJSON(e))
		}
	}
	return map[string]any{"items": all}, nil
}

func (a *App) invokeEntriesMarkRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID   string `json:"id"`
		Read *bool  `json:"read,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	read := true
	if in.Read != nil {
		read = *in.Read
	}
	if err := a.pg.MarkRead(ctx, id, read); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeEntriesStar(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID      string `json:"id"`
		Starred bool   `json:"starred"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	if err := a.pg.Star(ctx, id, in.Starred); err != nil {
		return nil, err
	}
	if in.Starred {
		RecordMark("star")
	}
	return map[string]any{"ok": true}, nil
}

// ─── unread_count (PG path) ───────────────────────────────────────

func (a *App) invokeUnreadCountPG(ctx context.Context) (any, error) {
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	inboxN, err := a.pg.UnreadCount(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	severity := "info"
	if inboxN >= 50 {
		severity = "warn"
	}

	// Merge radar (P2) — escalates severity when any unread hit is
	// tagged warn/error, and adds radar count to total.
	radarN := 0
	if a.radar != nil {
		radarN, err = a.radar.UnreadCount(ctx, scope, scopeID)
		if err != nil {
			return nil, err
		}
		if radarN > 0 {
			rs, err := a.radar.UnreadMaxSeverity(ctx, scope, scopeID)
			if err != nil {
				return nil, err
			}
			if severityRank(rs) > severityRank(severity) {
				severity = rs
			}
		}
	}

	return map[string]any{
		"count":    inboxN + radarN,
		"severity": severity,
		"breakdown": map[string]any{
			"inbox": inboxN,
			"radar": radarN,
		},
	}, nil
}

func severityRank(s string) int {
	switch s {
	case "error":
		return 3
	case "warn":
		return 2
	case "info":
		return 1
	}
	return 0
}

// ─── JSON projections ─────────────────────────────────────────────

// feedKindOr defaults a possibly-empty kind to 'rss' (rows created before
// migration 00020 have no kind; COALESCE handles NULL but "" can still come
// through the in-memory store path).
func feedKindOr(k string) string {
	if k == "" {
		return "rss"
	}
	return k
}

func feedJSON(f *Feed) map[string]any {
	out := map[string]any{
		"id":          f.ID.String(),
		"feed_url":    f.FeedURL,
		"site_url":    f.SiteURL,
		"title":       f.Title,
		"description": f.Description,
		"icon_url":    f.IconURL,
		"category":    f.Category,
		"refresh_sec": f.RefreshSec,
		"last_status": f.LastStatus,
		"enabled":     f.Enabled,
		"forced":      f.Forced,
		"kind":        feedKindOr(f.Kind),
		"created_at":  f.CreatedAt,
		// Legacy aliases for the existing AppViewHost templates that
		// were authored against the in-memory store. Drop in a future
		// version once the view manifest is updated.
		"url":      f.FeedURL,
		"added_at": f.CreatedAt,
		"unread":   0,
	}
	if !f.LastFetchedAt.IsZero() {
		out["last_fetched_at"] = f.LastFetchedAt
		out["last_fetch"] = f.LastFetchedAt
	}
	if f.LastError != "" {
		out["last_error"] = f.LastError
	}
	return out
}

func entryJSON(e *Entry) map[string]any {
	out := map[string]any{
		"id":      e.ID.String(),
		"feed_id": e.FeedID.String(),
		"guid":    e.GUID,
		"url":     e.URL,
		"title":   e.Title,
		"author":  e.Author,
		"unread":  e.Unread(),
		"starred": e.Starred,
	}
	if !e.PublishedAt.IsZero() {
		out["published_at"] = e.PublishedAt
	}
	if !e.FetchedAt.IsZero() {
		out["fetched_at"] = e.FetchedAt
	}
	if e.ContentHTML != "" {
		out["content_html"] = e.ContentHTML
		out["snippet"] = stripTags(e.ContentHTML, 280)
	}
	// AI digest projection — clients render the AI Card as soon as
	// these are populated. ai_processed = true also when ai_error,
	// so the UI can show the error state instead of perma-spinning.
	if !e.AIProcessedAt.IsZero() {
		out["ai_processed"] = true
		if e.AITakeaway != "" {
			out["ai_takeaway"] = e.AITakeaway
		}
		if len(e.AIBullets) > 0 {
			out["ai_bullets"] = e.AIBullets
		}
		if len(e.AITopics) > 0 {
			out["ai_topics"] = e.AITopics
		}
		if e.AIImportance > 0 {
			out["ai_importance"] = e.AIImportance
		}
		if e.AILang != "" {
			out["ai_lang"] = e.AILang
		}
		if e.AIError != "" {
			out["ai_error"] = e.AIError
		}
	} else {
		out["ai_processed"] = false
	}
	if e.WordCount > 0 {
		out["word_count"] = e.WordCount
		out["reading_seconds"] = e.ReadingSeconds
	}
	// M13.5 — podcast audio enclosure + transcription state, so the reader
	// can surface the audio source and a "已转写" marker.
	if e.EnclosureURL != "" {
		out["enclosure_url"] = e.EnclosureURL
		if e.EnclosureType != "" {
			out["enclosure_type"] = e.EnclosureType
		}
		out["transcribed"] = !e.TranscribedAt.IsZero()
		// Tier2 — synced-playback segments. Pass the jsonb array straight
		// through (parsed) so the client can render tappable sentences.
		if len(e.TranscriptSegments) > 0 {
			var segs []any
			if json.Unmarshal(e.TranscriptSegments, &segs) == nil && len(segs) > 0 {
				out["transcript_segments"] = segs
			}
		}
	}
	return out
}

func stripTags(s string, max int) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
			if b.Len() >= max {
				b.WriteString("…")
				return b.String()
			}
		}
	}
	return b.String()
}
