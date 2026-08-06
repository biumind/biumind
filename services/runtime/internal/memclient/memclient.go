// Package memclient is a thin Go HTTP client for Brain.Memory.
//
// Used by Runtime Agent's memory tools (memory.recall / memory.store)
// so the Agent can pull relevant facts into context and persist new
// ones. Mirrors services/brain/internal/memory/api/api.go.
//
// The client is intentionally small: no caching, no retry logic. The
// tool invocation already runs inside an Agent loop with backoff at the
// outer layer; layering retries here would only mask transient errors
// the LLM should see.
package memclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/services/runtime/internal/agent"
)

// Client talks to Brain.Memory using a Bearer JWT.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Recall implements agent.MemoryClient.
func (c *Client) Recall(ctx context.Context, projectID, query string, limit int) ([]agent.MemoryHit, error) {
	if limit <= 0 {
		limit = 5
	}
	q := url.Values{}
	q.Set("project_id", projectID)
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/v1/memory/recall?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("memory recall: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Memories []struct {
			ID       string  `json:"id"`
			Kind     string  `json:"kind"`
			Content  string  `json:"content"`
			Salience float32 `json:"salience"`
			Score    float32 `json:"score"`
		} `json:"memories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]agent.MemoryHit, 0, len(raw.Memories))
	for _, m := range raw.Memories {
		out = append(out, agent.MemoryHit{
			ID: m.ID, Kind: m.Kind, Content: m.Content,
			Salience: m.Salience, Score: m.Score,
		})
	}
	return out, nil
}

// Store implements agent.MemoryClient.
func (c *Client) Store(ctx context.Context, projectID, kind, content string, salience float32) error {
	body := map[string]any{
		"project_id": projectID,
		"kind":       kind,
		"content":    content,
	}
	if salience > 0 {
		body["salience"] = salience
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/memory", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("memory store: %d %s", resp.StatusCode, string(errBody))
	}
	return nil
}

func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Accept", "application/json")
}
