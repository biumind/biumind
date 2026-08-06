// M6 — Discover / Saved / OPML actions.
//
// starter_packs_list / starter_packs_install — bulk-add curated feeds
// marks_list — query rss.entry_marks for the saved tab
// opml_export / opml_import — OPML 1.0 round-trip

package rss

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ─── starter packs ────────────────────────────────────────────────

func (a *App) invokeStarterPacksList(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	rows, err := a.pg.pool.Query(ctx, `
		SELECT id, name, description, COALESCE(icon_emoji, ''), feeds, sort_order
		  FROM rss.starter_packs
		 ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("rss: list packs: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id, name, desc, icon string
			sortOrder            int
			feedsJSON            []byte
		)
		if err := rows.Scan(&id, &name, &desc, &icon, &feedsJSON, &sortOrder); err != nil {
			return nil, err
		}
		var feeds []map[string]any
		_ = json.Unmarshal(feedsJSON, &feeds)
		items = append(items, map[string]any{
			"id":          id,
			"name":        name,
			"description": desc,
			"icon_emoji":  icon,
			"feeds":       feeds,
			"sort_order":  sortOrder,
		})
	}
	return map[string]any{"items": items}, rows.Err()
}

func (a *App) invokeStarterPacksInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		PackID string `json:"pack_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.PackID == "" {
		return nil, errors.New("rss: pack_id required")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}

	var feedsJSON []byte
	if err := a.pg.pool.QueryRow(ctx,
		`SELECT feeds FROM rss.starter_packs WHERE id = $1`, in.PackID).Scan(&feedsJSON); err != nil {
		return nil, fmt.Errorf("rss: pack not found: %w", err)
	}
	var feeds []map[string]any
	_ = json.Unmarshal(feedsJSON, &feeds)

	added, skipped, failed := 0, 0, []map[string]any{}
	for _, f := range feeds {
		url, _ := f["url"].(string)
		title, _ := f["title"].(string)
		if url == "" {
			continue
		}
		_, err := a.pg.AddFeed(ctx, AddFeedInput{
			Scope: scope, ScopeID: scopeID,
			FeedURL: url, Title: title,
		})
		switch {
		case err == nil:
			added++
		case errors.Is(err, ErrFeedExists):
			skipped++
		default:
			failed = append(failed, map[string]any{"url": url, "error": err.Error()})
		}
	}
	return map[string]any{
		"added":   added,
		"skipped": skipped,
		"failed":  failed,
	}, nil
}

// ─── saved tab ────────────────────────────────────────────────────

func (a *App) invokeMarksList(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		Mark  string `json:"mark"`
		Limit int    `json:"limit,omitempty"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("rss: bad input: %w", err)
		}
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if in.Mark == "" {
		in.Mark = "star"
	}
	switch in.Mark {
	case "star", "pin", "wiki", "shared":
	default:
		return nil, fmt.Errorf("rss: bad mark %q", in.Mark)
	}
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 100
	}

	rows, err := a.pg.pool.Query(ctx, `
		SELECT e.id, e.feed_id, COALESCE(f.title,'') AS feed_title,
		       e.title, COALESCE(e.url,''), COALESCE(e.author,''),
		       COALESCE(e.ai_takeaway,''), COALESCE(e.ai_topics, '{}'::text[]),
		       COALESCE(e.published_at, e.fetched_at),
		       m.created_at AS marked_at,
		       COALESCE(m.wiki_block_id::text, '')
		  FROM rss.entry_marks m
		  JOIN rss.entries     e ON e.id = m.entry_id
		  JOIN rss.feeds       f ON f.id = e.feed_id
		 WHERE m.user_id = $1 AND m.mark = $2
		 ORDER BY m.created_at DESC
		 LIMIT $3`, scopeID, in.Mark, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("rss: list marks: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id, feedID                              uuid.UUID
			feedTitle, title, url, author, takeaway string
			topics                                  []string
			markedAt                                interface{}
			pubAt                                   interface{}
			wikiBlockID                             string
		)
		if err := rows.Scan(&id, &feedID, &feedTitle, &title, &url, &author,
			&takeaway, &topics, &pubAt, &markedAt, &wikiBlockID); err != nil {
			return nil, err
		}
		row := map[string]any{
			"id":          id.String(),
			"feed_id":     feedID.String(),
			"feed_title":  feedTitle,
			"title":       title,
			"url":         url,
			"author":      author,
			"ai_takeaway": takeaway,
			"ai_topics":   topics,
			"published_at": pubAt,
			"marked_at":   markedAt,
		}
		if wikiBlockID != "" {
			row["wiki_block_id"] = wikiBlockID
		}
		items = append(items, row)
	}
	_ = scope // unused — scope_id is the discriminator
	return map[string]any{"items": items}, rows.Err()
}

// ─── OPML import / export ─────────────────────────────────────────

type opmlOutline struct {
	XMLName xml.Name      `xml:"outline"`
	Type    string        `xml:"type,attr,omitempty"`
	Text    string        `xml:"text,attr,omitempty"`
	Title   string        `xml:"title,attr,omitempty"`
	XMLURL  string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL string        `xml:"htmlUrl,attr,omitempty"`
	Inner   []opmlOutline `xml:"outline"`
}

type opmlBody struct {
	XMLName xml.Name      `xml:"body"`
	Outline []opmlOutline `xml:"outline"`
}

type opmlHead struct {
	Title string `xml:"title"`
}

type opmlDoc struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}

func (a *App) invokeOpmlExport(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
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
	xmlBytes, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"xml":   xml.Header + string(xmlBytes),
		"count": len(feeds),
	}, nil
}

func (a *App) invokeOpmlImport(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		XML string `json:"xml"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.XML == "" {
		return nil, errors.New("rss: xml required")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}

	var doc opmlDoc
	if err := xml.NewDecoder(strings.NewReader(in.XML)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("rss: opml parse: %w", err)
	}

	added, skipped, failed := 0, 0, []map[string]any{}
	var walk func([]opmlOutline)
	walk = func(outs []opmlOutline) {
		for _, o := range outs {
			if o.XMLURL != "" {
				title := o.Title
				if title == "" {
					title = o.Text
				}
				_, err := a.pg.AddFeed(ctx, AddFeedInput{
					Scope: scope, ScopeID: scopeID,
					FeedURL: o.XMLURL, Title: title, SiteURL: o.HTMLURL,
				})
				switch {
				case err == nil:
					added++
				case errors.Is(err, ErrFeedExists):
					skipped++
				default:
					failed = append(failed, map[string]any{"url": o.XMLURL, "error": err.Error()})
				}
			}
			if len(o.Inner) > 0 {
				walk(o.Inner)
			}
		}
	}
	walk(doc.Body.Outline)
	return map[string]any{"added": added, "skipped": skipped, "failed": failed}, nil
}
