// Package share renders public read-only HTML for a shared RSS view
// (M11.3). The token is looked up in rss.shared_views; if it's missing,
// revoked, or expired the caller renders 404. Otherwise we pull a small
// live slice of the underlying view (entries or radar hits, scoped to
// the share's tenant) and emit a minimal, dependency-free HTML page —
// no JS, no auth, no action buttons.
package share

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotShareable means the token doesn't resolve to an active share.
var ErrNotShareable = errors.New("share: token not found / expired / revoked")

const maxItems = 30

type view struct {
	ViewKind string
	Filter   map[string]any
	Scope    string
	ScopeID  string
}

type item struct {
	Title     string
	URL       string
	Published *time.Time
	Source    string
}

// Render resolves the token and returns a complete HTML document. Returns
// ErrNotShareable when the token is not an active share.
func Render(ctx context.Context, pool *pgxpool.Pool, token string) (string, error) {
	v, err := loadView(ctx, pool, token)
	if err != nil {
		return "", err
	}
	var items []item
	switch v.ViewKind {
	case "radar":
		items, err = radarItems(ctx, pool, v)
	default: // today / inbox / saved → entries
		items, err = entryItems(ctx, pool, v)
	}
	if err != nil {
		return "", err
	}
	return renderHTML(v, items), nil
}

func loadView(ctx context.Context, pool *pgxpool.Pool, token string) (*view, error) {
	var (
		v          view
		filterRaw  []byte
		expiresAt  time.Time
		revokedAt  *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT view_kind, filter_json, scope, scope_id, expires_at, revoked_at
		  FROM rss.shared_views
		 WHERE token = $1`, token).
		Scan(&v.ViewKind, &filterRaw, &v.Scope, &v.ScopeID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotShareable
	}
	if err != nil {
		return nil, err
	}
	if revokedAt != nil || time.Now().After(expiresAt) {
		return nil, ErrNotShareable
	}
	_ = json.Unmarshal(filterRaw, &v.Filter)
	return &v, nil
}

// entryItems pulls recent entries for the share's tenant. "saved" filters
// to starred. An optional filter.feed_id narrows to one feed.
func entryItems(ctx context.Context, pool *pgxpool.Pool, v *view) ([]item, error) {
	q := strings.Builder{}
	q.WriteString(`
		SELECT e.title, COALESCE(e.url,''), e.published_at, f.title
		  FROM rss.entries e
		  JOIN rss.feeds f ON f.id = e.feed_id
		 WHERE f.scope = $1 AND f.scope_id = $2`)
	args := []any{v.Scope, v.ScopeID}
	if v.ViewKind == "saved" {
		q.WriteString(" AND e.starred = true")
	}
	if fid, ok := v.Filter["feed_id"].(string); ok && fid != "" {
		args = append(args, fid)
		q.WriteString(" AND e.feed_id = $3")
	}
	q.WriteString(" ORDER BY e.published_at DESC NULLS LAST, e.fetched_at DESC LIMIT 30")
	rows, err := pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.Title, &it.URL, &it.Published, &it.Source); err != nil {
			return nil, err
		}
		out = append(out, it)
		if len(out) >= maxItems {
			break
		}
	}
	return out, rows.Err()
}

// radarItems pulls recent hits for the share's tenant.
func radarItems(ctx context.Context, pool *pgxpool.Pool, v *view) ([]item, error) {
	rows, err := pool.Query(ctx, `
		SELECT h.title, COALESCE(h.url,''), h.hit_at, h.source
		  FROM rss.watch_hits h
		  JOIN rss.watch_rules r ON r.id = h.rule_id
		 WHERE r.scope = $1 AND r.scope_id = $2
		 ORDER BY h.hit_at DESC
		 LIMIT 30`, v.Scope, v.ScopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []item
	for rows.Next() {
		var (
			it    item
			hitAt time.Time
		)
		if err := rows.Scan(&it.Title, &it.URL, &hitAt, &it.Source); err != nil {
			return nil, err
		}
		h := hitAt
		it.Published = &h
		out = append(out, it)
		if len(out) >= maxItems {
			break
		}
	}
	return out, rows.Err()
}

var kindTitles = map[string]string{
	"today": "今日要闻", "radar": "雷达命中", "saved": "收藏", "inbox": "订阅",
}

func renderHTML(v *view, items []item) string {
	title := kindTitles[v.ViewKind]
	if title == "" {
		title = "RSS 分享"
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<meta name="robots" content="noindex">`)
	b.WriteString("<title>" + html.EscapeString(title) + " · BiuMind RSS</title>")
	b.WriteString(`<style>
:root{color-scheme:light dark}
body{font-family:-apple-system,system-ui,"Segoe UI",Roboto,sans-serif;max-width:720px;margin:0 auto;padding:32px 20px;line-height:1.6}
h1{font-size:22px;margin:0 0 4px}
.sub{color:#888;font-size:13px;margin-bottom:24px}
ul{list-style:none;padding:0;margin:0}
li{padding:14px 0;border-bottom:1px solid rgba(128,128,128,.18)}
a{color:inherit;text-decoration:none;font-size:16px;font-weight:600}
a:hover{text-decoration:underline}
.meta{display:block;color:#999;font-size:12px;margin-top:4px}
.empty{color:#999}
footer{margin-top:32px;color:#aaa;font-size:12px;text-align:center}
</style></head><body>`)
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(`<div class="sub">只读分享 · 由 BiuMind RSS 生成</div>`)
	if len(items) == 0 {
		b.WriteString(`<p class="empty">暂无内容。</p>`)
	} else {
		b.WriteString("<ul>")
		for _, it := range items {
			b.WriteString("<li>")
			t := html.EscapeString(it.Title)
			if it.URL != "" {
				b.WriteString(`<a href="` + html.EscapeString(it.URL) + `" target="_blank" rel="noopener noreferrer">` + t + "</a>")
			} else {
				b.WriteString("<span>" + t + "</span>")
			}
			meta := html.EscapeString(it.Source)
			if it.Published != nil {
				if meta != "" {
					meta += " · "
				}
				meta += it.Published.Format("2006-01-02 15:04")
			}
			if meta != "" {
				b.WriteString(`<span class="meta">` + meta + "</span>")
			}
			b.WriteString("</li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`<footer>BiuMind · 一体化 AI 工作平台</footer>`)
	b.WriteString("</body></html>")
	return b.String()
}
