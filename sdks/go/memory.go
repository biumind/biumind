package biumind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Memory is one stored memory record. ``Score`` is only populated by
// recall responses.
type Memory struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Kind           string    `json:"kind"`
	Content        string    `json:"content"`
	Salience       float64   `json:"salience"`
	CreatedAt      time.Time `json:"created_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
	Score          *float64  `json:"score,omitempty"`
}

// RecallResult is the recall endpoint response shape.
type RecallResult struct {
	Memories []Memory `json:"memories"`
	Mode     string   `json:"mode"`
	Query    string   `json:"query"`
}

// MemoryClient calls Brain's memory endpoints.
type MemoryClient struct {
	cfg Config
}

func NewMemoryClient(cfg Config) *MemoryClient {
	cfg.normalize()
	return &MemoryClient{cfg: cfg}
}

var validKinds = map[string]struct{}{
	"recall": {}, "preference": {}, "skill": {},
}

// StoreOptions are optional knobs on Store.
type StoreOptions struct {
	Kind     string
	Salience *float64
}

// Store inserts a new memory. Default kind is "recall". When salience
// is nil the server picks (currently 0.5).
func (m *MemoryClient) Store(ctx context.Context, projectID, content string, opts StoreOptions) (*Memory, error) {
	kind := opts.Kind
	if kind == "" {
		kind = "recall"
	}
	if _, ok := validKinds[kind]; !ok {
		return nil, errors.New("biumind: invalid kind " + kind)
	}
	body := map[string]interface{}{
		"project_id": projectID,
		"kind":       kind,
		"content":    content,
	}
	if opts.Salience != nil {
		body["salience"] = *opts.Salience
	}
	out := &Memory{}
	if err := m.do(ctx, http.MethodPost, "/v1/memory", nil, body, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOptions are optional filters on List.
type ListOptions struct {
	Kind  string
	Limit int
}

// List returns the most recent memories for a project, optionally
// filtered by kind.
func (m *MemoryClient) List(ctx context.Context, projectID string, opts ListOptions) ([]Memory, error) {
	q := url.Values{}
	q.Set("project_id", projectID)
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	q.Set("limit", strconv.Itoa(opts.Limit))
	var out struct {
		Memories []Memory `json:"memories"`
	}
	if err := m.do(ctx, http.MethodGet, "/v1/memory", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Memories, nil
}

// RecallOptions are optional knobs on Recall.
type RecallOptions struct {
	Kind  string
	Limit int
}

// Recall does a hybrid (lexical + semantic) search across the
// project's memories.
func (m *MemoryClient) Recall(ctx context.Context, projectID, query string, opts RecallOptions) (*RecallResult, error) {
	if query == "" {
		return nil, errors.New("biumind: query is required")
	}
	q := url.Values{}
	q.Set("project_id", projectID)
	q.Set("q", query)
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	q.Set("limit", strconv.Itoa(opts.Limit))
	out := &RecallResult{}
	if err := m.do(ctx, http.MethodGet, "/v1/memory/recall", q, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes a single memory by id.
func (m *MemoryClient) Delete(ctx context.Context, id string) error {
	return m.do(ctx, http.MethodDelete, "/v1/memory/"+url.PathEscape(id), nil, nil, nil)
}

// ─── Plumbing ──────────────────────────────────────────

func (m *MemoryClient) do(ctx context.Context, method, path string,
	query url.Values, body interface{}, out interface{},
) error {
	full := m.cfg.BrainURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var bodyReader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, full, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, full, nil)
	}
	if err != nil {
		return err
	}
	setJSONHeaders(req, m.cfg.Token)

	httpClient := m.cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: m.cfg.Timeout}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := readBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return httpError(resp, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
