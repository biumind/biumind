package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectSourceKind(t *testing.T) {
	cases := []struct {
		url  string
		want SourceKind
	}{
		{"https://www.youtube.com/channel/UCBR8-60-B28hp2BmDPdntcQ", KindYouTube},
		{"https://youtube.com/@anthropic-ai", KindYouTube},
		{"https://github.com/anthropics/anthropic-sdk-go", KindGitHub},
		{"https://blog.laoda.de/rss.xml", KindRSS},
		{"https://example.com/feed", KindRSS},
		{"https://example.com/atom.xml", KindRSS},
		{"https://feeds.example.com/json", KindRSS},
		{"https://blog.example.com/", KindGeneric},
		{"https://news.ycombinator.com/", KindGeneric},
		{"not-a-url", KindUnknown},
	}
	for _, c := range cases {
		got := DetectSourceKind(c.url)
		if got != c.want {
			t.Errorf("%q → %s, want %s", c.url, got, c.want)
		}
	}
}

func TestGitHubReleasesAtom(t *testing.T) {
	got := githubReleasesAtom("https://github.com/anthropics/anthropic-sdk-go")
	want := "https://github.com/anthropics/anthropic-sdk-go/releases.atom"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := githubReleasesAtom("https://example.com/foo/bar"); got != "" {
		t.Errorf("non-github → %q, want empty", got)
	}
}

func TestExtractYTChannelID(t *testing.T) {
	body := `<html><head><meta property="og:url" content="https://youtube.com/channel/UCBR8-60-B28hp2BmDPdntcQ"></head>...`
	if got := extractYTChannelID(body); got != "UCBR8-60-B28hp2BmDPdntcQ" {
		t.Errorf("got %q", got)
	}
	body2 := `{"channelId":"UCt4t-jeY85JegMlZ-E5UWtA","other":"data"}`
	if got := extractYTChannelID(body2); got != "UCt4t-jeY85JegMlZ-E5UWtA" {
		t.Errorf("got %q", got)
	}
	if got := extractYTChannelID("no channel here"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFindFeedLink(t *testing.T) {
	body := `<html><head>
<link rel="alternate" type="application/rss+xml" href="/rss.xml" title="RSS">
<link rel="canonical" href="/main">
</head><body></body></html>`
	if got := findFeedLink(body); got != "/rss.xml" {
		t.Errorf("got %q", got)
	}
	if got := findFeedLink("<html><head></head></html>"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDiscover_RSSFeedURLPassthrough(t *testing.T) {
	d := NewDiscoverer()
	// looksLikeFeedURL=true → KindRSS, no HTTP needed.
	r, err := d.Discover(context.Background(), "https://example.com/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	if r.FeedURL != "https://example.com/rss.xml" || r.Kind != KindRSS {
		t.Errorf("got %+v", r)
	}
}

func TestDiscover_GenericWithFeedLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
<link rel="alternate" type="application/rss+xml" href="/feed.xml">
</head></html>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := NewDiscoverer()
	r, err := d.Discover(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if r.FeedURL != srv.URL+"/feed.xml" || r.Kind != KindGeneric {
		t.Errorf("got %+v", r)
	}
}

func TestDiscover_GenericProbeCommonPath(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head></head><body>hi</body></html>`))
		case "/rss.xml":
			hits++
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><title>x</title></channel></rss>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDiscoverer()
	r, err := d.Discover(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if r.FeedURL == "" {
		t.Errorf("feed URL empty")
	}
	if hits == 0 {
		t.Errorf("/rss.xml not probed")
	}
}
