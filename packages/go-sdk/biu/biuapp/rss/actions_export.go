// export_archive (M14.3) — data ownership / takeout. Bundles the caller's
// entire RSS footprint into one zip the user can download and walk away
// with: feeds (OPML, importable into any reader), every article (JSONL),
// read/star marks (CSV), radar rules (JSON), preferences (JSON), plus a
// README explaining the layout. No lock-in.
//
// Returns the zip base64-encoded (same shape as briefing audio) so it rides
// the normal JSON invoke channel; the client decodes + saves to disk.

package rss

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"time"
)

// exportEntryCap bounds the JSONL so a pathological account can't produce a
// multi-hundred-MB base64 blob on the JSON channel. Logged when hit.
const exportEntryCap = 100000

func (a *App) invokeExportArchive(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	userID := scopeID // user scope: scope_id == user id

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	counts := map[string]int{}

	// 1. feeds → OPML (importable into Inoreader/Feedly/NetNewsWire/…)
	feeds, err := a.pg.ListFeeds(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	doc := opmlDoc{Version: "1.0", Head: opmlHead{Title: "BiuMind RSS"}}
	for _, f := range feeds {
		doc.Body.Outline = append(doc.Body.Outline, opmlOutline{
			Type: "rss", Text: f.Title, Title: f.Title,
			XMLURL: f.FeedURL, HTMLURL: f.SiteURL,
		})
	}
	opmlBytes, _ := xml.MarshalIndent(doc, "", "  ")
	if err := writeZipFile(zw, "opml.xml", []byte(xml.Header+string(opmlBytes))); err != nil {
		return nil, err
	}
	counts["feeds"] = len(feeds)

	// 2. entries → JSONL (one self-describing JSON object per line).
	nEntries, err := a.exportEntries(ctx, zw, scope, scopeID)
	if err != nil {
		return nil, err
	}
	counts["entries"] = nEntries

	// 3. marks → CSV (read/star/etc events, keyed by user).
	if userID != "" {
		nMarks, err := a.exportMarks(ctx, zw, userID)
		if err != nil {
			return nil, err
		}
		counts["marks"] = nMarks
	}

	// 4. radar rules → JSON.
	nRules, err := a.exportRules(ctx, zw, scope, scopeID)
	if err != nil {
		return nil, err
	}
	counts["rules"] = nRules

	// 5. preferences → JSON.
	if userID != "" {
		if err := a.exportPrefs(ctx, zw, userID); err != nil {
			return nil, err
		}
	}

	// 6. README.
	_ = writeZipFile(zw, "README.txt", []byte(exportReadme))

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("rss: zip close: %w", err)
	}

	return map[string]any{
		"archive_b64": base64.StdEncoding.EncodeToString(buf.Bytes()),
		"filename":    "biumind-rss-export.zip",
		"size":        buf.Len(),
		"counts":      counts,
	}, nil
}

func (a *App) exportEntries(ctx context.Context, zw *zip.Writer, scope, scopeID string) (int, error) {
	rows, err := a.pg.pool.Query(ctx, `
		SELECT e.id, e.feed_id, COALESCE(e.guid,''), COALESCE(e.url,''),
		       e.title, COALESCE(e.author,''), COALESCE(e.content_text,''),
		       e.published_at, e.starred, e.read_at IS NOT NULL AS read,
		       COALESCE(e.enclosure_url,'')
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope=$1 AND f.scope_id=$2
		 ORDER BY e.published_at DESC NULLS LAST
		 LIMIT $3`, scope, scopeID, exportEntryCap)
	if err != nil {
		return 0, fmt.Errorf("rss: export entries: %w", err)
	}
	defer rows.Close()

	w, err := zw.Create("entries.jsonl")
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(w)
	n := 0
	for rows.Next() {
		var (
			idRaw, feedRaw                         any
			guid, url, title, author, text, encURL string
			pub                                    *time.Time
			starred, read                          bool
		)
		if err := rows.Scan(&idRaw, &feedRaw, &guid, &url, &title, &author,
			&text, &pub, &starred, &read, &encURL); err != nil {
			return n, err
		}
		rec := map[string]any{
			"id":      fmt.Sprint(idRaw),
			"feed_id": fmt.Sprint(feedRaw),
			"guid":    guid,
			"url":     url,
			"title":   title,
			"author":  author,
			"content": text,
			"starred": starred,
			"read":    read,
		}
		if encURL != "" {
			rec["enclosure_url"] = encURL
		}
		if pub != nil && !pub.IsZero() {
			rec["published_at"] = pub.UTC().Format(time.RFC3339)
		}
		if err := enc.Encode(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func (a *App) exportMarks(ctx context.Context, zw *zip.Writer, userID string) (int, error) {
	rows, err := a.pg.pool.Query(ctx, `
		SELECT entry_id, mark, created_at
		  FROM rss.entry_marks WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return 0, fmt.Errorf("rss: export marks: %w", err)
	}
	defer rows.Close()

	w, err := zw.Create("marks.csv")
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"entry_id", "mark", "created_at"})
	n := 0
	for rows.Next() {
		var entryID any
		var mark string
		var created time.Time
		if err := rows.Scan(&entryID, &mark, &created); err != nil {
			return n, err
		}
		_ = cw.Write([]string{fmt.Sprint(entryID), mark, created.UTC().Format(time.RFC3339)})
		n++
	}
	cw.Flush()
	return n, rows.Err()
}

func (a *App) exportRules(ctx context.Context, zw *zip.Writer, scope, scopeID string) (int, error) {
	rows, err := a.pg.pool.Query(ctx, `
		SELECT name, match_any, match_all, exclude, sources,
		       on_hit_badge, on_hit_notify, cooldown_sec, enabled
		  FROM rss.watch_rules WHERE scope=$1 AND scope_id=$2 ORDER BY created_at`,
		scope, scopeID)
	if err != nil {
		return 0, fmt.Errorf("rss: export rules: %w", err)
	}
	defer rows.Close()

	var rules []map[string]any
	for rows.Next() {
		var (
			name, badge                          string
			matchAny, matchAll, exclude, sources []string
			notify                               []string
			cooldown                             int
			enabled                              bool
		)
		if err := rows.Scan(&name, &matchAny, &matchAll, &exclude, &sources,
			&badge, &notify, &cooldown, &enabled); err != nil {
			return 0, err
		}
		rules = append(rules, map[string]any{
			"name": name, "match_any": matchAny, "match_all": matchAll,
			"exclude": exclude, "sources": sources, "on_hit_badge": badge,
			"on_hit_notify": notify, "cooldown_sec": cooldown, "enabled": enabled,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	b, _ := json.MarshalIndent(rules, "", "  ")
	return len(rules), writeZipFile(zw, "rules.json", b)
}

func (a *App) exportPrefs(ctx context.Context, zw *zip.Writer, userID string) error {
	var cfg []byte
	err := a.pg.pool.QueryRow(ctx,
		`SELECT config FROM rss.user_preferences WHERE user_id=$1`, userID).Scan(&cfg)
	if err != nil {
		// No row → empty prefs; not an error.
		cfg = []byte("{}")
	}
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	return writeZipFile(zw, "preferences.json", cfg)
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

const exportReadme = `BiuMind RSS — Data Export
=========================

This archive is a complete, portable copy of your RSS data. Nothing here is
locked to BiuMind.

  opml.xml          Your subscriptions in OPML 1.0. Import into any reader
                    (Inoreader, Feedly, NetNewsWire, Miniflux, …).
  entries.jsonl     Every article, one JSON object per line:
                    {id, feed_id, guid, url, title, author, content,
                     starred, read, enclosure_url?, published_at?}.
  marks.csv         Your read/star/etc marks: entry_id, mark, created_at.
  rules.json        Your radar rules (keyword/source watch rules).
  preferences.json  Your RSS settings.

Re-importing subscriptions: most readers accept opml.xml directly via their
"Import OPML" feature.
`
