// Package searxng calls a SearxNG instance for web results.
//
//	GET <url>/search?q=...&format=json
//
// We expose only the JSON shape we need to keep this thin.
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

type Client struct {
	BaseURL string
	HC      *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HC:      &http.Client{Timeout: 10 * time.Second},
	}
}

type WebResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Engine  string  `json:"engine"`
}

type apiResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
		Engine  string  `json:"engine"`
	} `json:"results"`
}

// Search returns up to limit web results.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]WebResult, error) {
	if c.BaseURL == "" {
		return nil, errors.New("searxng: not configured")
	}
	if query = strings.TrimSpace(query); query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("searxng: status %d body=%s", resp.StatusCode, string(raw))
	}
	var api apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return nil, fmt.Errorf("searxng: decode: %w", err)
	}
	out := make([]WebResult, 0, min(limit, len(api.Results)))
	for _, r := range api.Results {
		if len(out) >= limit {
			break
		}
		out = append(out, WebResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Score:   r.Score,
			Engine:  r.Engine,
		})
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
