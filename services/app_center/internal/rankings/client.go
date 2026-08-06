// newsnow client. Calls /api/s?id=<board> and returns the parsed
// snapshot. Upstream id field is sometimes a number and sometimes a
// string depending on the source — we coerce to string here so the
// rest of the pipeline only ever sees strings.
//
// Snapshots are NOT validated against expected_domain here; that's
// the caller's job (per-board policy lives on the boards row). This
// keeps the client a pure transport.

package rankings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultTimeout = 8 * time.Second
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://newsnow.example.com"
	}
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: defaultTimeout},
	}
}

type Snapshot struct {
	BoardID     string
	UpdatedTime int64
	Items       []Item
}

type Item struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	URL       string         `json:"url"`
	MobileURL string         `json:"mobileUrl,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// flexibleID is needed because upstream sources mix string and number.
// json.RawMessage preserves the literal so we can unwrap quotes for
// strings and pass through digits as-is.
type rawItem struct {
	ID        json.RawMessage `json:"id"`
	Title     string          `json:"title"`
	URL       string          `json:"url"`
	MobileURL string          `json:"mobileUrl,omitempty"`
	Extra     map[string]any  `json:"extra,omitempty"`
}

func flexibleID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	if raw[0] == 'n' { // null
		return ""
	}
	return string(raw)
}

type rawResponse struct {
	Status      string    `json:"status"`
	ID          string    `json:"id"`
	UpdatedTime int64     `json:"updatedTime"`
	Items       []rawItem `json:"items"`
	Message     string    `json:"message,omitempty"`
}

var (
	ErrUpstreamFailed = errors.New("rankings: upstream failed")
	ErrUpstreamShape  = errors.New("rankings: upstream shape unexpected")
)

func (c *Client) Fetch(ctx context.Context, boardID string) (*Snapshot, error) {
	u := c.BaseURL + "/api/s?id=" + boardID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("rankings: build req: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BiuMind/1.0 (+rankings)")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrUpstreamFailed, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrUpstreamFailed, err)
	}

	var raw rawResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	if raw.ID != "" && raw.ID != boardID {
		return nil, fmt.Errorf("%w: response id=%q want %q", ErrUpstreamShape, raw.ID, boardID)
	}

	out := &Snapshot{
		BoardID:     boardID,
		UpdatedTime: raw.UpdatedTime,
		Items:       make([]Item, 0, len(raw.Items)),
	}
	for _, r := range raw.Items {
		idStr := flexibleID(r.ID)
		if idStr == "" {
			// Some upstream sources omit id entirely (RSS-derived ones).
			// Fall back to the URL — radar matcher dedupes by title hash
			// anyway, so the id is just a stable handle.
			idStr = r.URL
		}
		// Defensive: reject items where everything is empty.
		if r.Title == "" {
			continue
		}
		out.Items = append(out.Items, Item{
			ID:        idStr,
			Title:     r.Title,
			URL:       r.URL,
			MobileURL: r.MobileURL,
			Extra:     r.Extra,
		})
	}
	return out, nil
}

// numberStr is exposed for tests that build mock items quickly.
func numberStr(n int64) string { return strconv.FormatInt(n, 10) }
