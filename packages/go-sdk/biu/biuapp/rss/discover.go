// Source kind detection + feed URL discovery.
//
// Two phases:
//
//  1. DetectSourceKind classifies a URL into one of: youtube channel,
//     github repo, generic website, or unknown. Pure regex; no HTTP.
//
//  2. DiscoverFeedURL takes a URL of any kind and returns the
//     parseable feed URL — for youtube/github we construct a known
//     pattern; for generic sites we HTTP GET the page, look for
//     <link rel="alternate" type="application/(rss|atom|feed+json)">,
//     and probe a few common paths if absent.

package rss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// SourceKind tells the caller what type of feed-bearing URL the user
// pasted. The UI uses this for "已为你将 youtube.com/@xxx 解析成 RSS"
// affordance + per-kind icon.
type SourceKind string

const (
	KindRSS     SourceKind = "rss"     // already a feed URL
	KindYouTube SourceKind = "youtube"
	KindGitHub  SourceKind = "github"
	KindWeChat  SourceKind = "wechat"  // M13.3 公众号 via werss / wewe-rss relay
	KindTwitter SourceKind = "x"       // M13.4 Twitter/X user via Nitter instance
	KindPodcast SourceKind = "podcast" // M13.5 RSS feed carrying audio enclosures
	KindGeneric SourceKind = "generic" // plain website, needs discovery
	KindUnknown SourceKind = "unknown"
)

var (
	youtubeChannelRe = regexp.MustCompile(`^https?://(?:www\.)?youtube\.com/(?:channel/(UC[A-Za-z0-9_-]+)|@([A-Za-z0-9._-]+))/?`)
	githubRepoRe     = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/?$`)
	// twitterHandleRe matches an x.com / twitter.com profile URL and
	// captures the @handle. Unambiguous (host-anchored), unlike fuzzy
	// "looks like a 公众号 name" detection which we deliberately don't do —
	// the client picks the 公众号 tab explicitly instead.
	twitterHandleRe = regexp.MustCompile(`^https?://(?:www\.|mobile\.)?(?:twitter\.com|x\.com)/([A-Za-z0-9_]{1,15})/?$`)
)

// reservedTwitterPaths are x.com top-level paths that are NOT user handles,
// so we don't turn x.com/home into a "user timeline" feed.
var reservedTwitterPaths = map[string]bool{
	"home": true, "explore": true, "notifications": true, "messages": true,
	"search": true, "settings": true, "i": true, "compose": true, "intent": true,
}

// WeChatFeedURL builds a 公众号 relay feed URL from a configurable relay
// base + the public account name. The relay (werss.app public instance or a
// self-hosted wewe-rss) is a THIRD-PARTY dependency — if it goes down the
// feed simply errors through the normal fetch path (v3 risk R7). The base is
// user-configurable in Settings; we only do the string join + name encoding.
func WeChatFeedURL(relayBase, accountName string) (string, error) {
	relayBase = strings.TrimRight(strings.TrimSpace(relayBase), "/")
	accountName = strings.TrimSpace(accountName)
	if relayBase == "" {
		return "", errors.New("rss: 公众号中继地址未配置")
	}
	if accountName == "" {
		return "", errors.New("rss: 公众号名为空")
	}
	if u, err := url.Parse(relayBase); err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("rss: 公众号中继地址非法: %q", relayBase)
	}
	return relayBase + "/" + url.PathEscape(accountName), nil
}

// NitterFeedURL builds <instance>/<handle>/rss. The Nitter instance is
// user-configurable in Settings and defaults to empty (= X source disabled),
// since public Nitter instances are unstable (v3 risk R7).
func NitterFeedURL(instance, handle string) (string, error) {
	instance = strings.TrimRight(strings.TrimSpace(instance), "/")
	handle = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if instance == "" {
		return "", errors.New("rss: Nitter 实例未配置")
	}
	if handle == "" {
		return "", errors.New("rss: X 用户名为空")
	}
	if u, err := url.Parse(instance); err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("rss: Nitter 实例地址非法: %q", instance)
	}
	return instance + "/" + url.PathEscape(handle) + "/rss", nil
}

// TwitterHandleFromURL extracts a bare @handle from an x.com/twitter.com
// profile URL, or "" if the URL isn't a user-profile URL. Lets the client
// turn a pasted x.com/elonmusk into the Nitter feed (combined with the
// configured instance via NitterFeedURL).
func TwitterHandleFromURL(rawURL string) string {
	m := twitterHandleRe.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return ""
	}
	if reservedTwitterPaths[strings.ToLower(m[1])] {
		return ""
	}
	return m[1]
}

// DetectSourceKind classifies the URL purely by pattern. Doesn't
// dereference; safe to call from frontend / live in the action layer.
func DetectSourceKind(rawURL string) SourceKind {
	if youtubeChannelRe.MatchString(rawURL) {
		return KindYouTube
	}
	if githubRepoRe.MatchString(rawURL) {
		return KindGitHub
	}
	if looksLikeFeedURL(rawURL) {
		return KindRSS
	}
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" && u.Host != "" {
		return KindGeneric
	}
	return KindUnknown
}

func looksLikeFeedURL(u string) bool {
	low := strings.ToLower(u)
	for _, suffix := range []string{".xml", ".rss", ".atom", "/feed", "/rss", "/atom", "feed.json", "/atom.xml", "/rss.xml", "/feed.xml", "/index.xml"} {
		if strings.Contains(low, suffix) {
			return true
		}
	}
	return false
}

// DiscoverResult is what feeds_add returns to the UI, and what
// feeds_discover returns standalone.
type DiscoverResult struct {
	OriginalURL string
	FeedURL     string
	Kind        SourceKind
}

// Discoverer wraps an HTTP client + cache. Callers create one at
// boot and share. Cache keys on the original URL — same URL probed
// twice in a row hits memo.
type Discoverer struct {
	HTTP    *http.Client
	mu      sync.Mutex
	memo    map[string]*DiscoverResult
}

func NewDiscoverer() *Discoverer {
	return &Discoverer{
		HTTP: &http.Client{Timeout: 10 * time.Second},
		memo: map[string]*DiscoverResult{},
	}
}

// commonFeedPaths is the standard list of paths to probe when no
// <link rel="alternate"> tag is present in the HTML head.
var commonFeedPaths = []string{
	"/rss.xml", "/feed.xml", "/atom.xml", "/index.xml",
	"/rss", "/feed", "/atom", "/feed.json",
}

func (d *Discoverer) Discover(ctx context.Context, rawURL string) (*DiscoverResult, error) {
	d.mu.Lock()
	if r, ok := d.memo[rawURL]; ok {
		d.mu.Unlock()
		return r, nil
	}
	d.mu.Unlock()

	kind := DetectSourceKind(rawURL)
	res := &DiscoverResult{OriginalURL: rawURL, Kind: kind}

	switch kind {
	case KindRSS:
		res.FeedURL = rawURL
	case KindYouTube:
		feed, err := d.youtubeFeed(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		res.FeedURL = feed
	case KindGitHub:
		res.FeedURL = githubReleasesAtom(rawURL)
	case KindGeneric:
		feed, err := d.genericDiscover(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		res.FeedURL = feed
	default:
		return nil, fmt.Errorf("rss: cannot detect feed kind for %q", rawURL)
	}

	d.mu.Lock()
	d.memo[rawURL] = res
	d.mu.Unlock()
	return res, nil
}

// youtubeFeed turns a channel URL into the standard videos.xml feed.
// Two flavours:
//   /channel/UCxxx              — already canonical, just substitute
//   /@handle                    — need to resolve handle → channel id
func (d *Discoverer) youtubeFeed(ctx context.Context, rawURL string) (string, error) {
	m := youtubeChannelRe.FindStringSubmatch(rawURL)
	if len(m) < 3 {
		return "", errors.New("rss: bad youtube channel url")
	}
	if m[1] != "" { // /channel/UCxxx
		return "https://www.youtube.com/feeds/videos.xml?channel_id=" + m[1], nil
	}
	// /@handle — resolve via HTML page (channel id is embedded in
	// canonical link or "channelId" in JS bootstrap).
	body, err := d.fetchHTML(ctx, rawURL)
	if err != nil {
		return "", err
	}
	if id := extractYTChannelID(body); id != "" {
		return "https://www.youtube.com/feeds/videos.xml?channel_id=" + id, nil
	}
	return "", fmt.Errorf("rss: youtube channel id not found in %s", rawURL)
}

func extractYTChannelID(body string) string {
	// Canonical: <meta property="og:url" content=".../channel/UCxxx">
	// or "channelId":"UCxxx" in the bootstrap JSON.
	for _, prefix := range []string{`"channelId":"`, `/channel/`} {
		i := strings.Index(body, prefix)
		if i < 0 {
			continue
		}
		s := body[i+len(prefix):]
		end := 0
		for end < len(s) && (s[end] == '_' || s[end] == '-' ||
			(s[end] >= '0' && s[end] <= '9') ||
			(s[end] >= 'a' && s[end] <= 'z') ||
			(s[end] >= 'A' && s[end] <= 'Z')) {
			end++
		}
		if strings.HasPrefix(s, "UC") && end >= 24 {
			return s[:end]
		}
	}
	return ""
}

func githubReleasesAtom(rawURL string) string {
	m := githubRepoRe.FindStringSubmatch(rawURL)
	if len(m) < 3 {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases.atom", m[1], m[2])
}

// genericDiscover fetches the page and looks for a feed link. Falls
// back to common-path probing.
func (d *Discoverer) genericDiscover(ctx context.Context, rawURL string) (string, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("rss: bad url: %w", err)
	}
	body, err := d.fetchHTML(ctx, rawURL)
	if err != nil {
		// Even if HTML fetch fails, fall through to path probing
		// — the page might be 403 but /rss.xml might be public.
	} else {
		if href := findFeedLink(body); href != "" {
			abs, err := base.Parse(href)
			if err == nil {
				return abs.String(), nil
			}
		}
	}
	// Probe common paths in parallel.
	type result struct {
		url string
		ok  bool
	}
	resCh := make(chan result, len(commonFeedPaths))
	for _, p := range commonFeedPaths {
		probe := *base
		probe.Path = p
		probe.RawQuery = ""
		probeURL := probe.String()
		go func() {
			resCh <- result{url: probeURL, ok: d.headOK(ctx, probeURL)}
		}()
	}
	for i := 0; i < len(commonFeedPaths); i++ {
		r := <-resCh
		if r.ok {
			return r.url, nil
		}
	}
	return "", fmt.Errorf("rss: no feed found at %s", rawURL)
}

func (d *Discoverer) fetchHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return string(body), err
}

func (d *Discoverer) headOK(ctx context.Context, rawURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "xml") || strings.Contains(ct, "atom") ||
		strings.Contains(ct, "rss") || strings.Contains(ct, "feed+json") {
		return true
	}
	// Sniff first ~256 bytes for a feed root element.
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	low := strings.ToLower(string(buf[:n]))
	return strings.Contains(low, "<rss") || strings.Contains(low, "<feed") ||
		strings.Contains(low, "<rdf:rdf") || strings.Contains(low, `"version":"https://jsonfeed.org`)
}

// findFeedLink walks an HTML doc for a <link rel="alternate"
// type="application/(rss|atom|feed+json)" href="...">.
func findFeedLink(body string) string {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "link") {
			var rel, typ, href string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "type":
					typ = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				}
			}
			if rel == "alternate" && href != "" &&
				(strings.Contains(typ, "rss") || strings.Contains(typ, "atom") ||
					strings.Contains(typ, "feed+json")) {
				found = href
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if found != "" {
				return
			}
		}
	}
	walk(doc)
	return found
}
