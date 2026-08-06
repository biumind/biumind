package rss

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleRSS = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Demo Feed</title>
    <link>https://example.com</link>
    <item>
      <title>First post</title>
      <link>https://example.com/1</link>
      <description>Hello world</description>
      <pubDate>Mon, 02 Jan 2026 15:04:05 +0000</pubDate>
      <guid>g1</guid>
    </item>
    <item>
      <title>Second post</title>
      <link>https://example.com/2</link>
      <description>Another</description>
      <pubDate>Tue, 03 Jan 2026 16:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

const sampleAtom = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title>
  <entry>
    <title>Atom one</title>
    <link href="https://example.com/a1" rel="alternate"/>
    <summary>summary one</summary>
    <updated>2026-01-04T12:00:00Z</updated>
    <id>urn:1</id>
  </entry>
</feed>`

func TestParseFeed_RSS(t *testing.T) {
	out, err := ParseFeed([]byte(sampleRSS), "https://example.com/feed", 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Title != "Demo Feed" {
		t.Errorf("title=%q", out.Title)
	}
	if len(out.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(out.Items))
	}
	if out.Items[0].Title != "First post" || out.Items[0].Link != "https://example.com/1" {
		t.Errorf("item[0]: %+v", out.Items[0])
	}
	if out.Items[0].Published.Year() != 2026 {
		t.Errorf("pubDate not parsed: %v", out.Items[0].Published)
	}
	if out.Items[0].GUID != "g1" {
		t.Errorf("guid: %s", out.Items[0].GUID)
	}
}

func TestParseFeed_Atom(t *testing.T) {
	out, err := ParseFeed([]byte(sampleAtom), "https://example.com/feed", 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Title != "Atom Feed" || len(out.Items) != 1 {
		t.Fatalf("bad: %+v", out)
	}
	if out.Items[0].Link != "https://example.com/a1" {
		t.Errorf("alternate link not picked: %+v", out.Items[0])
	}
}

func TestParseFeed_Limit(t *testing.T) {
	out, _ := ParseFeed([]byte(sampleRSS), "x", 1)
	if len(out.Items) != 1 {
		t.Errorf("want 1, got %d", len(out.Items))
	}
}

func TestParseFeed_BadXML(t *testing.T) {
	if _, err := ParseFeed([]byte("not xml"), "x", 10); err == nil {
		t.Errorf("want parse error")
	}
}

func TestApp_FetchAgainstHTTPServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	a := New()
	out, err := a.Invoke(context.Background(), "fetch",
		json.RawMessage(`{"url":"`+srv.URL+`","limit":5}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	o, ok := out.(*Output)
	if !ok || o.Title != "Demo Feed" || o.ItemCount != 2 {
		t.Errorf("bad output: %#v", out)
	}
}

func TestApp_RejectsEmptyURL(t *testing.T) {
	a := New()
	if _, err := a.Invoke(context.Background(), "fetch", json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "missing url") {
		t.Errorf("want missing-url err, got %v", err)
	}
}

func TestApp_UnreadCountBadge(t *testing.T) {
	a := New()
	// 空 store → count=0 (BadgeData.visible 会过滤掉)。
	out, err := a.Invoke(context.Background(), "unread_count", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unread_count returned %T, want map", out)
	}
	if m["count"].(int) != 0 {
		t.Errorf("empty store count = %v, want 0", m["count"])
	}
	if m["severity"] != "info" {
		t.Errorf("empty severity = %v, want info", m["severity"])
	}

	// 加几条订阅 + 标 fetched, 总未读 = sum(Unread)。
	s1 := a.store.Add("https://a.example/feed", "A", nil)
	s2 := a.store.Add("https://b.example/feed", "B", nil)
	a.store.MarkFetched(s1.ID, 5)
	a.store.MarkFetched(s2.ID, 7)
	out2, _ := a.Invoke(context.Background(), "unread_count", json.RawMessage(`{}`))
	m2 := out2.(map[string]any)
	if m2["count"].(int) != 12 {
		t.Errorf("sum unread = %v, want 12", m2["count"])
	}
	if m2["severity"] != "info" {
		t.Errorf("severity at 12 = %v, want info (warn 阈值 50)", m2["severity"])
	}

	// 50+ → warn。
	a.store.MarkFetched(s1.ID, 60)
	out3, _ := a.Invoke(context.Background(), "unread_count", json.RawMessage(`{}`))
	m3 := out3.(map[string]any)
	if m3["count"].(int) < 50 {
		t.Fatalf("setup error, count=%v", m3["count"])
	}
	if m3["severity"] != "warn" {
		t.Errorf("severity at 60+ = %v, want warn", m3["severity"])
	}
}

func TestApp_ManifestSidebarBadge(t *testing.T) {
	m := New().Manifest()
	if m.Sidebar == nil {
		t.Fatal("manifest.sidebar missing")
	}
	if m.Sidebar.BadgeAction != "unread_count" {
		t.Errorf("BadgeAction = %q, want unread_count", m.Sidebar.BadgeAction)
	}
	if m.Sidebar.BadgeRefreshSec < 60 {
		t.Errorf("BadgeRefreshSec = %d, want ≥ 60", m.Sidebar.BadgeRefreshSec)
	}
	// validator 强制 badge_action 必须在 actions[].name 中。
	found := false
	for _, a := range m.Actions {
		if a.Name == "unread_count" {
			found = true
			break
		}
	}
	if !found {
		t.Error("actions[].name missing 'unread_count'")
	}
}
