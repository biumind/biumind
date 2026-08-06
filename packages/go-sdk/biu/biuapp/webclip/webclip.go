// Package webclip — turn an HTML document (or a fetched URL) into a
// structured ParsedDoc that can land in Brain.Wiki, exposed as a BiuApp.
//
// Reuses biu/ingest's parseHTML pipeline: title extraction, script
// stripping, paragraph & heading splitting. We don't re-implement here;
// the app is a thin shell so server-side and client-side ingest both go
// through the same code path (invariant from P2.4).
//
// Actions:
//
//	parse_html  in: {"html": "..."}                              out: ParsedDoc
//	fetch       in: {"url": "https://..."}                       out: ParsedDoc
package webclip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/ingest"
)

const Name = "webclip"

type App struct {
	httpClient *http.Client
}

func New() *App { return &App{httpClient: &http.Client{Timeout: 30 * time.Second}} }

func (a *App) Manifest() biuapp.Manifest {
	return biuapp.Manifest{
		Name:        Name,
		Version:     "0.2.0",
		Description: "Clip a web page (or HTML blob) into a structured document",
		Author:      "BiuMind",
		Permissions: []string{"net.outbound"},
		Actions: []biuapp.ActionSpec{
			{
				Name:        "parse_html",
				Description: "Parse an HTML string in-place",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"html"},
					"properties": map[string]any{
						"html": map[string]any{"type": "string"},
						"url":  map[string]any{"type": "string"},
					},
				},
			},
			{
				Name:        "fetch",
				Description: "Fetch a URL and parse the response body",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url": map[string]any{"type": "string", "format": "uri", "title": "网址"},
					},
				},
			},
		},
		ManifestExt: biuapp.ManifestExt{
			Identifier: Name,
			Title:      "网页剪藏",
			Category:   "content",
			Kind:       "hybrid",
			Views: []biuapp.ViewSpec{
				{
					ID:        "home",
					Route:     "/apps/webclip",
					Title:     "网页剪藏",
					Layout:    biuapp.LayoutForm,
					SchemaRef: "actions.fetch.input_schema",
					Submit: &biuapp.FormSubmit{
						Action: "fetch",
						OnSuccess: &biuapp.ViewActionEffect{
							Toast: "已剪藏",
						},
					},
				},
			},
			Sidebar: &biuapp.SidebarHints{
				PreferredPosition: "middle",
			},
		},
	}
}

func (a *App) Init(ctx context.Context, deps biuapp.Deps) error { return nil }

type parseHTMLIn struct {
	HTML string `json:"html"`
	URL  string `json:"url"`
}

type fetchIn struct {
	URL string `json:"url"`
}

func (a *App) Invoke(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	switch action {
	case "parse_html":
		var in parseHTMLIn
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("webclip: bad input: %w", err)
		}
		if in.HTML == "" {
			return nil, errors.New("webclip: missing html")
		}
		return ingest.Parse(ingest.Source{
			Kind:    ingest.KindHTML,
			Content: in.HTML,
			URL:     in.URL,
		})

	case "fetch":
		var in fetchIn
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("webclip: bad input: %w", err)
		}
		if in.URL == "" {
			return nil, errors.New("webclip: missing url")
		}
		// Parse the URL up-front so we fail fast on garbage input
		// instead of letting the HTTP client try to dial nonsense.
		if _, err := url.Parse(in.URL); err != nil {
			return nil, fmt.Errorf("webclip: bad url: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "biumind-webclip/0.1")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("webclip: %s -> %d", in.URL, resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
		if err != nil {
			return nil, err
		}
		return ingest.Parse(ingest.Source{
			Kind:    ingest.KindHTML,
			Content: string(body),
			URL:     in.URL,
		})

	default:
		return nil, fmt.Errorf("webclip: unknown action %q", action)
	}
}
