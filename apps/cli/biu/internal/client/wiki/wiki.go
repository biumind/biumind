// Package wiki is biu CLI's thin client to the Brain.Wiki HTTP API.
//
// Endpoints used:
//
//	GET  /v1/wiki/projects                                list mine
//	POST /v1/wiki/projects                                create project
//	POST /v1/wiki/projects/{pid}/pages                    create page
//	POST /v1/wiki/projects/{pid}/pages/{id}/blocks        create block
//
// Used by `biu ingest --commit` to push the generated PageDraft into Wiki.
package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string // typically same as model-relay gateway URL (api.biu.app or self-hosted)
	BearerToken string // user JWT or virtual key
	HTTP        *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		BearerToken: token,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Page struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	Version   int    `json:"version"`
}

type Block struct {
	ID       string         `json:"id"`
	PageID   string         `json:"page_id"`
	Position float64        `json:"position"`
	Type     string         `json:"type"`
	Content  map[string]any `json:"content"`
	Version  int            `json:"version"`
}

// ListProjects returns the caller's projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/wiki/projects", nil, &out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// CreateProject creates a new project owned by caller.
func (c *Client) CreateProject(ctx context.Context, name string) (*Project, error) {
	var p Project
	if err := c.do(ctx, http.MethodPost, "/v1/wiki/projects", map[string]any{"name": name}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePage creates a page under a project.
type CreatePageInput struct {
	Title    string
	ParentID string // optional
}

func (c *Client) CreatePage(ctx context.Context, projectID string, in CreatePageInput) (*Page, error) {
	body := map[string]any{"title": in.Title}
	if in.ParentID != "" {
		body["parent_id"] = in.ParentID
	}
	var p Page
	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/v1/wiki/projects/%s/pages", projectID), body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateBlock creates a single block.
type CreateBlockInput struct {
	Type     string
	Position float64
	Content  map[string]any
}

func (c *Client) CreateBlock(ctx context.Context, projectID, pageID string, in CreateBlockInput) (*Block, error) {
	body := map[string]any{
		"type":     in.Type,
		"position": in.Position,
		"content":  in.Content,
	}
	var b Block
	if err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/v1/wiki/projects/%s/pages/%s/blocks", projectID, pageID), body, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ResolveProject finds a project by id (uuid) or by name (case-insensitive exact match).
// Convenience for CLI: --project <id|name>.
func (c *Client) ResolveProject(ctx context.Context, idOrName string) (*Project, error) {
	if idOrName == "" {
		return nil, fmt.Errorf("project id or name required")
	}
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	// Try exact id match first
	for _, p := range projects {
		if p.ID == idOrName {
			return &p, nil
		}
	}
	// Then name match (case-insensitive)
	for _, p := range projects {
		if strings.EqualFold(p.Name, idOrName) {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("project %q not found (have %d projects)", idOrName, len(projects))
}

// ─── HTTP helper ────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, in any, out any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("wiki: missing base URL")
	}
	if c.BearerToken == "" {
		return fmt.Errorf("wiki: missing bearer token")
	}
	var bodyReader io.Reader
	if in != nil {
		body, err := json.Marshal(in)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("wiki: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("wiki %s %s: %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("wiki: decode %s: %w", path, err)
		}
	}
	return nil
}
