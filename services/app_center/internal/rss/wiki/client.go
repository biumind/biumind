// Brain Wiki client — minimal subset the RSS sink path needs.
//
// Endpoints used:
//   POST /v1/wiki/projects                       create-or-get "信息流" project
//   GET  /v1/wiki/projects                       list mine (find by name)
//   POST /v1/wiki/projects/{pid}/pages           create one page per entry
//   POST /v1/wiki/projects/{pid}/pages/{id}/blocks    add takeaway/body/source
//
// Auth: pass-through user bearer (so per-user project ownership +
// quota stay tidy). The caller is the RSS app which has the user's
// raw token in context (bauth.RawTokenFrom).

package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultProjectName = "信息流" // M3 default landing project
	defaultTimeout     = 15 * time.Second
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: defaultTimeout},
	}
}

var ErrNotConfigured = errors.New("wiki: brain url empty")

// Project / Page / Block — all minimal projections.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Block struct {
	ID       string         `json:"id"`
	Position float64        `json:"position"`
	Type     string         `json:"type"`
	Content  map[string]any `json:"content"`
}

// EnsureProject finds or creates a project named "信息流" for the
// caller. Idempotent: list → match by name → reuse; otherwise create.
func (c *Client) EnsureProject(ctx context.Context, token string) (*Project, error) {
	if c == nil || c.BaseURL == "" {
		return nil, ErrNotConfigured
	}
	// 1. List my projects, look for "信息流".
	var list []Project
	if err := c.do(ctx, http.MethodGet, "/v1/wiki/projects", token, nil, &list); err != nil {
		return nil, fmt.Errorf("wiki: list projects: %w", err)
	}
	for _, p := range list {
		if p.Name == defaultProjectName {
			return &p, nil
		}
	}
	// 2. Create.
	var created Project
	if err := c.do(ctx, http.MethodPost, "/v1/wiki/projects", token,
		map[string]any{"name": defaultProjectName}, &created); err != nil {
		return nil, fmt.Errorf("wiki: create project: %w", err)
	}
	return &created, nil
}

func (c *Client) CreatePage(ctx context.Context, token, projectID, title string) (*Page, error) {
	var p Page
	if err := c.do(ctx, http.MethodPost,
		"/v1/wiki/projects/"+projectID+"/pages", token,
		map[string]any{"title": title}, &p); err != nil {
		return nil, fmt.Errorf("wiki: create page: %w", err)
	}
	return &p, nil
}

func (c *Client) CreateBlock(ctx context.Context, token, projectID, pageID string, position float64,
	blockType string, content map[string]any) (*Block, error) {
	var b Block
	body := map[string]any{
		"position": position,
		"type":     blockType,
		"content":  content,
	}
	if err := c.do(ctx, http.MethodPost,
		"/v1/wiki/projects/"+projectID+"/pages/"+pageID+"/blocks", token,
		body, &b); err != nil {
		return nil, fmt.Errorf("wiki: create block: %w", err)
	}
	return &b, nil
}

func (c *Client) do(ctx context.Context, method, path, token string,
	body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode,
			truncate(string(respBytes), 200))
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode: %w (body=%q)", err, truncate(string(respBytes), 200))
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
