// Package client — RelayProvider talks to BiuMind model-relay /v1/messages.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RelayProvider is Mode A / B: route through BiuMind model-relay.
type RelayProvider struct {
	Endpoint    string
	BearerToken string // JWT or virtual key (bk-live-*)
	HTTPClient  *http.Client
}

func NewRelayProvider(endpoint, token string) *RelayProvider {
	return &RelayProvider{
		Endpoint:    strings.TrimRight(endpoint, "/"),
		BearerToken: token,
		HTTPClient:  &http.Client{Timeout: 0}, // streaming
	}
}

// Backward-compatible alias for older code paths.
type Client = RelayProvider

func New(endpoint, token string) *RelayProvider {
	return NewRelayProvider(endpoint, token)
}

func (h *RelayProvider) Name() string { return "model-relay" }

type hubChatRequest struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

func (h *RelayProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan Frame, error) {
	body, err := json.Marshal(hubChatRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  req.Messages,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+h.BearerToken)
	resp, err := h.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Relay: %d: %s", resp.StatusCode, string(raw))
	}

	out := make(chan Frame, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		if err := readSSE(resp.Body, out); err != nil && !errors.Is(err, io.EOF) {
			out <- Frame{Kind: KindError, Err: err}
		}
	}()
	return out, nil
}

func readSSE(r io.Reader, out chan<- Frame) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var event string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			event = ""
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch event {
			case "delta":
				var d struct {
					Text string `json:"text"`
				}
				if json.Unmarshal([]byte(data), &d) == nil {
					out <- Frame{Kind: KindDelta, Text: d.Text}
				}
			case "stop":
				var d struct {
					Reason string `json:"reason"`
				}
				if json.Unmarshal([]byte(data), &d) == nil {
					out <- Frame{Kind: KindStop, Stop: d.Reason}
				}
			case "end":
				out <- Frame{Kind: KindEnd}
				return nil
			case "error":
				out <- Frame{Kind: KindError, Err: fmt.Errorf("%s", data)}
				return nil
			}
		}
	}
	return sc.Err()
}

// PingHealth verifies the model-relay is reachable (used by `biu doctor`).
func (h *RelayProvider) PingHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Endpoint+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status %d", resp.StatusCode)
	}
	return nil
}
