package rss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultFetcher_RSS2(t *testing.T) {
	body := `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>Test Blog</title>
  <link>https://example.com</link>
  <description>desc</description>
  <item>
    <title>Hello</title>
    <link>https://example.com/p1</link>
    <guid>p1</guid>
    <pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
    <description>body 1</description>
  </item>
  <item>
    <title>World</title>
    <link>https://example.com/p2</link>
    <guid>p2</guid>
    <pubDate>Tue, 03 Jan 2006 15:04:05 GMT</pubDate>
  </item>
</channel></rss>`
	srv := newSrv(t, body, "application/rss+xml", nil)
	defer srv.Close()
	res, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Title != "Test Blog" {
		t.Errorf("title = %q", res.Title)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(res.Entries))
	}
	if res.Entries[0].Title != "Hello" || res.Entries[1].Title != "World" {
		t.Errorf("titles = %q, %q", res.Entries[0].Title, res.Entries[1].Title)
	}
	if len(res.Entries[0].TitleHash) != 32 {
		t.Errorf("title hash len = %d", len(res.Entries[0].TitleHash))
	}
}

func TestDefaultFetcher_Atom(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Site</title>
  <link href="https://atom.example.com"/>
  <id>tag:example.com,2003:1</id>
  <updated>2003-12-13T18:30:02Z</updated>
  <entry>
    <id>tag:example.com,2003:e1</id>
    <title>Atom Entry</title>
    <link href="https://atom.example.com/e1"/>
    <updated>2003-12-13T18:30:02Z</updated>
    <summary>x</summary>
  </entry>
</feed>`
	srv := newSrv(t, body, "application/atom+xml", nil)
	defer srv.Close()
	res, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Title != "Atom Site" {
		t.Errorf("title = %q", res.Title)
	}
	if len(res.Entries) != 1 || res.Entries[0].Title != "Atom Entry" {
		t.Errorf("entries: %+v", res.Entries)
	}
}

func TestDefaultFetcher_JSONFeed(t *testing.T) {
	body := `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "JSON Site",
  "home_page_url": "https://jf.example.com",
  "feed_url": "https://jf.example.com/feed.json",
  "items": [
    {"id":"1","title":"JF1","url":"https://jf.example.com/1","content_html":"<p>x</p>","date_published":"2025-01-01T00:00:00Z"},
    {"id":"2","title":"JF2","url":"https://jf.example.com/2","content_text":"y","date_published":"2025-01-02T00:00:00Z"}
  ]
}`
	srv := newSrv(t, body, "application/feed+json", nil)
	defer srv.Close()
	res, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Title != "JSON Site" {
		t.Errorf("title = %q", res.Title)
	}
	if len(res.Entries) != 2 {
		t.Errorf("entries = %d", len(res.Entries))
	}
}

func TestDefaultFetcher_NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>x</title><item><title>a</title></item></channel></rss>`))
	}))
	defer srv.Close()

	// First call no etag — full body
	r1, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if r1.NotModified {
		t.Error("first call should not be NotModified")
	}
	if r1.Etag != `"abc"` {
		t.Errorf("etag = %q", r1.Etag)
	}

	// Second call with etag — 304
	r2, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL, Etag: `"abc"`})
	if err != nil {
		t.Fatal(err)
	}
	if !r2.NotModified {
		t.Error("second call should be NotModified")
	}
}

func TestDefaultFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := NewDefaultFetcher().Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if !errors.Is(err, ErrFetchFailed) {
		t.Errorf("expected ErrFetchFailed, got %v", err)
	}
}

func TestDefaultFetcher_BodyTooLarge(t *testing.T) {
	huge := strings.Repeat("a", 6*1024*1024)
	body := `<rss version="2.0"><channel><title>x</title><description>` + huge + `</description></channel></rss>`
	srv := newSrv(t, body, "application/rss+xml", nil)
	defer srv.Close()
	f := NewDefaultFetcher()
	f.MaxBodyMiB = 1
	// Truncated body causes parse failure — that's expected behaviour
	_, err := f.Fetch(context.Background(), FetchRequest{FeedURL: srv.URL})
	if err == nil {
		t.Error("expected error from truncated body")
	}
}

func TestTitleHash_Stable(t *testing.T) {
	a := titleHash("Hello World", "https://x.com/1")
	b := titleHash("hello world", "https://x.com/1")
	c := titleHash("Hello World", "https://x.com/2")
	if string(a) != string(b) {
		t.Error("title hash should be case-insensitive")
	}
	if string(a) == string(c) {
		t.Error("URL should affect hash")
	}
	if len(a) != 32 {
		t.Errorf("hash len = %d, want 32", len(a))
	}
}

func newSrv(t *testing.T, body, ct string, extraHeaders map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		for k, v := range extraHeaders {
			w.Header().Set(k, v)
		}
		_, _ = w.Write([]byte(body))
	}))
}
