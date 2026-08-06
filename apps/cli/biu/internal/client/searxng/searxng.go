// Package searxng is a thin client used by biu CLI's `websearch` tool when
// search.mode = "direct". For mode=model-relay the CLI calls Brain.Search instead.
package searxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Result struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet"`
	Engine  string  `json:"engine"`
	Score   float64 `json:"score"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

type apiResp struct {
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
		Engine  string  `json:"engine"`
	} `json:"results"`
}

// Search hits <BaseURL>/search?q=... directly.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if c.BaseURL == "" {
		return nil, errors.New("searxng: not configured (set [search].searxng_url)")
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	v := url.Values{}
	v.Set("q", q)
	v.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/search?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("searxng: %d: %s", resp.StatusCode, string(raw))
	}
	var api apiResp
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return nil, err
	}
	out := make([]Result, 0, min(limit, len(api.Results)))
	for _, r := range api.Results {
		if len(out) >= limit {
			break
		}
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Engine: r.Engine, Score: r.Score})
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
