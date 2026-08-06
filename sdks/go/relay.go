package biumind

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Message is one turn in the request/response history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MessagesRequest models the body model-relay forwards to upstream providers.
// Extra is merged in last so callers can pass provider-specific fields
// (e.g. anthropic_version, top_p) without the SDK needing to track
// every option.
type MessagesRequest struct {
	Model     string                 `json:"model"`
	Messages  []Message              `json:"messages"`
	System    string                 `json:"system,omitempty"`
	MaxTokens int                    `json:"max_tokens,omitempty"`
	Stream    bool                   `json:"stream,omitempty"`
	Extra     map[string]interface{} `json:"-"`
}

// RelayClient calls model-relay's relay endpoint.
type RelayClient struct {
	cfg Config
}

func NewRelayClient(cfg Config) *RelayClient {
	cfg.normalize()
	return &RelayClient{cfg: cfg}
}

// Messages sends a non-streaming request. The returned bytes are the
// raw JSON body — callers decode according to the upstream provider.
func (h *RelayClient) Messages(ctx context.Context, req MessagesRequest) ([]byte, error) {
	req.Stream = false
	body, err := encodeBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.cfg.RelayURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setJSONHeaders(httpReq, h.cfg.Token)
	resp, err := h.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readJSON(resp)
}

// MessagesStream returns a channel that emits Anthropic-style
// content-block text deltas. Errors during the stream surface on the
// returned error channel and the data channel is closed when the
// stream ends.
func (h *RelayClient) MessagesStream(ctx context.Context, req MessagesRequest) (<-chan string, <-chan error) {
	req.Stream = true
	out := make(chan string, 16)
	errCh := make(chan error, 1)

	body, err := encodeBody(req)
	if err != nil {
		errCh <- err
		close(out)
		close(errCh)
		return out, errCh
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.cfg.RelayURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		errCh <- err
		close(out)
		close(errCh)
		return out, errCh
	}
	setJSONHeaders(httpReq, h.cfg.Token)
	httpReq.Header.Set("Accept", "text/event-stream")

	go func() {
		defer close(out)
		defer close(errCh)
		// Streaming uses an unbounded-deadline client.
		c := *h.client()
		c.Timeout = 0
		resp, doErr := c.Do(httpReq)
		if doErr != nil {
			errCh <- doErr
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			b, _ := readBody(resp)
			errCh <- httpError(resp, b)
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
				select {
				case out <- ev.Delta.Text:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		}
	}()
	return out, errCh
}

// ─── Plumbing ───────────────────────────────────────────

func (h *RelayClient) client() *http.Client {
	if h.cfg.HTTPClient != nil {
		return h.cfg.HTTPClient
	}
	return &http.Client{Timeout: h.cfg.Timeout}
}

func encodeBody(req MessagesRequest) ([]byte, error) {
	// Encode the canonical fields, then merge Extra.
	base, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(req.Extra) == 0 {
		return base, nil
	}
	var asMap map[string]interface{}
	if err := json.Unmarshal(base, &asMap); err != nil {
		return nil, err
	}
	for k, v := range req.Extra {
		asMap[k] = v
	}
	return json.Marshal(asMap)
}

func setJSONHeaders(r *http.Request, token string) {
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/json")
	if r.Body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
}

func readJSON(resp *http.Response) ([]byte, error) {
	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpError(resp, body)
	}
	return body, nil
}

func readBody(resp *http.Response) ([]byte, error) {
	const maxBody = 16 << 20
	buf := bytes.NewBuffer(make([]byte, 0, 4096))
	_, err := buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func httpError(resp *http.Response, body []byte) error {
	retry := time.Duration(0)
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			retry = time.Duration(secs) * time.Second
		}
	}
	return &Error{Status: resp.StatusCode, Body: string(body), RetryAfter: retry}
}

// errorf is a tiny helper that wraps a non-HTTP failure (network etc.)
// as a regular fmt error — keeps callers' switch on *biumind.Error
// straightforward.
func errorf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}
