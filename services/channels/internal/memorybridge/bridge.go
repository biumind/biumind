// Package memorybridge fetches relevant memories from Brain.Memory
// for an inbound message so the agent can answer with full context.
//
// The bridge is opt-in: Channels main wires it only when all of
// CHANNELS_BRAIN_URL, CHANNELS_BRAIN_BEARER, CHANNELS_MEMORY_PROJECT_ID
// are set. When any is missing the Router treats Bridge as nil and
// forwards envelopes without a memory_context field — preserving the
// pre-C1 behaviour exactly.
//
// We deliberately call Brain's HTTP `/v1/memory/recall` instead of the
// MCP server: HTTP is simpler from a Go client and the JSON contract
// already returns ranked hits with score + mode discriminator. MCP is
// the right choice for *external* agents speaking the protocol; for
// in-VPC service-to-service calls, plain HTTP wins.
package memorybridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Recalled is a single memory hit returned by Brain.
type Recalled struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Content  string  `json:"content"`
	Salience float64 `json:"salience"`
	Score    float64 `json:"score"`
}

// Bridge calls Brain.Memory /v1/memory/recall on every inbound text.
type Bridge struct {
	BrainURL  string // e.g. "http://brain:7003"
	Bearer    string // long-lived virtual key Brain accepts
	ProjectID string // single project for now; per-sender mapping is future work
	Client    *http.Client
	Timeout   time.Duration // per-call; default 3s
	Limit     int           // recall limit; default 5
}

func New(brainURL, bearer, projectID string) *Bridge {
	return &Bridge{
		BrainURL:  strings.TrimRight(brainURL, "/"),
		Bearer:    bearer,
		ProjectID: projectID,
		Client:    &http.Client{Timeout: 3 * time.Second},
		Timeout:   3 * time.Second,
		Limit:     5,
	}
}

// Recall returns up to Limit memories ranked against the query. On
// any error it returns nil + the error — callers MUST treat this as
// best-effort and continue without context. mode is the brain-side
// discriminator ("hybrid" / "lexical") used by metrics + tests.
func (b *Bridge) Recall(ctx context.Context, query string) ([]Recalled, string, error) {
	if b == nil {
		return nil, "", nil
	}
	if strings.TrimSpace(query) == "" {
		return nil, "", nil
	}
	if b.BrainURL == "" || b.Bearer == "" || b.ProjectID == "" {
		return nil, "", fmt.Errorf("memorybridge: missing config")
	}

	timeout := b.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	limit := b.Limit
	if limit == 0 {
		limit = 5
	}

	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	q := url.Values{}
	q.Set("project_id", b.ProjectID)
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(c, http.MethodGet,
		b.BrainURL+"/v1/memory/recall?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+b.Bearer)
	req.Header.Set("Accept", "application/json")

	client := b.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("memorybridge: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("memorybridge: %s: %s",
			resp.Status, truncate(body, 200))
	}
	var out struct {
		Memories []Recalled `json:"memories"`
		Mode     string     `json:"mode"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("memorybridge: parse: %w", err)
	}
	return out.Memories, out.Mode, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
