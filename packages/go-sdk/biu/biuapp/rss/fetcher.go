// Fetcher abstracts the act of "given a feed URL, retrieve and parse
// the upstream payload". Default impl wraps the vendored Miniflux
// parser chain (RSS / Atom / RDF / JSON Feed). Conditional GET via
// If-None-Match / If-Modified-Since is implemented here so callers
// only have to persist the latest etag / last_modified.

package rss

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mfparser "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/parser"
)

const (
	defaultUserAgent  = "BiuMind/1.0 (+https://biumind.com)"
	defaultMaxBodyMiB = 5
	defaultTimeout    = 20 * time.Second
)

type FetchRequest struct {
	FeedURL      string
	Etag         string
	LastModified string
	UserAgent    string
}

type FetchResult struct {
	NotModified  bool
	Etag         string
	LastModified string

	Title       string
	SiteURL     string
	Description string
	IconURL     string
	Entries     []ParsedEntry
}

type ParsedEntry struct {
	GUID        string
	URL         string
	Title       string
	Author      string
	ContentHTML string
	PublishedAt time.Time
	TitleHash   []byte
	// M13.5 — first audio enclosure (podcast episode), if any. Empty for
	// ordinary articles. Drives the transcribe worker + reader audio link.
	EnclosureURL  string
	EnclosureType string
}

type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error)
}

type DefaultFetcher struct {
	HTTP       *http.Client
	UserAgent  string
	MaxBodyMiB int
}

func NewDefaultFetcher() *DefaultFetcher {
	return &DefaultFetcher{
		HTTP:       &http.Client{Timeout: defaultTimeout},
		UserAgent:  defaultUserAgent,
		MaxBodyMiB: defaultMaxBodyMiB,
	}
}

var (
	ErrFetchFailed = errors.New("rss: fetch failed")
	ErrParseFailed = errors.New("rss: parse failed")
)

func (f *DefaultFetcher) Fetch(ctx context.Context, in FetchRequest) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.FeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrFetchFailed, err)
	}
	ua := in.UserAgent
	if ua == "" {
		ua = f.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/feed+json, application/json;q=0.9, application/xml;q=0.8, text/xml;q=0.7, */*;q=0.5")
	// NB: do NOT set Accept-Encoding manually. Go's http.Transport
	// auto-negotiates gzip and transparently decompresses ONLY when
	// the caller leaves this header alone. Setting it here makes
	// resp.Body return raw compressed bytes, which the feed parser
	// then fails to detect as RSS / Atom (the visible bug was
	// "parser: unable to detect feed format" on every feed served
	// with content-encoding: gzip — i.e. ~all production sites).
	if in.Etag != "" {
		req.Header.Set("If-None-Match", in.Etag)
	}
	if in.LastModified != "" {
		req.Header.Set("If-Modified-Since", in.LastModified)
	}

	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %v", ErrFetchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &FetchResult{
			NotModified:  true,
			Etag:         in.Etag,
			LastModified: in.LastModified,
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrFetchFailed, resp.StatusCode)
	}

	limit := f.MaxBodyMiB
	if limit <= 0 {
		limit = defaultMaxBodyMiB
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrFetchFailed, err)
	}

	mfFeed, err := mfparser.ParseFeed(in.FeedURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}

	out := &FetchResult{
		Etag:         resp.Header.Get("Etag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Title:        strings.TrimSpace(mfFeed.Title),
		SiteURL:      mfFeed.SiteURL,
		Description:  mfFeed.Description,
		IconURL:      mfFeed.IconURL,
	}
	out.Entries = make([]ParsedEntry, 0, len(mfFeed.Entries))
	for _, e := range mfFeed.Entries {
		guid := e.Hash
		if guid == "" {
			guid = e.URL
		}
		// M13.5 — capture the first audio enclosure (podcast episode).
		var encURL, encType string
		for _, enc := range e.Enclosures {
			if enc != nil && enc.IsAudio() && enc.URL != "" {
				encURL, encType = enc.URL, enc.MimeType
				break
			}
		}
		out.Entries = append(out.Entries, ParsedEntry{
			GUID:          guid,
			URL:           e.URL,
			Title:         strings.TrimSpace(e.Title),
			Author:        e.Author,
			ContentHTML:   e.Content,
			PublishedAt:   e.Date,
			TitleHash:     titleHash(e.Title, e.URL),
			EnclosureURL:  encURL,
			EnclosureType: encType,
		})
	}
	return out, nil
}

func titleHash(title, url string) []byte {
	h := sha256.New()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(title))))
	h.Write([]byte("|"))
	h.Write([]byte(url))
	return h.Sum(nil)
}
