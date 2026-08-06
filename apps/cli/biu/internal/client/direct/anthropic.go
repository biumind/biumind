// Package direct implements client.Provider variants that talk directly to LLM
// providers (bypassing BiuMind model-relay). Used by biu CLI's "Mode C / direct".
package direct

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

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

const defaultAnthropicURL = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

// Anthropic implements client.Provider via Anthropic Messages API.
type Anthropic struct {
	APIKey  string
	BaseURL string // overrideable; "" → defaultAnthropicURL
	HTTP    *http.Client
}

func NewAnthropic(apiKey, baseURL string) *Anthropic {
	return &Anthropic{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 0}, // streaming
	}
}

func (a *Anthropic) Name() string { return "anthropic-direct" }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
}

func (a *Anthropic) ChatStream(ctx context.Context, req client.ChatRequest) (<-chan client.Frame, error) {
	if a.APIKey == "" {
		return nil, errors.New("anthropic-direct: missing api_key")
	}
	upstream := anthropicRequest{
		Model:     req.Model,
		System:    req.System,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	if upstream.MaxTokens == 0 {
		upstream.MaxTokens = 4096
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			if upstream.System == "" {
				upstream.System = m.Content
			}
			continue
		}
		upstream.Messages = append(upstream.Messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	base := a.BaseURL
	if base == "" {
		base = defaultAnthropicURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := a.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(raw))
	}

	out := make(chan client.Frame, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		if err := readAnthropicSSE(resp.Body, out); err != nil && !errors.Is(err, io.EOF) {
			out <- client.Frame{Kind: client.KindError, Err: err}
		}
	}()
	return out, nil
}

func readAnthropicSSE(r io.Reader, out chan<- client.Frame) error {
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
			case "content_block_delta":
				var d struct {
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
				}
				if json.Unmarshal([]byte(data), &d) == nil && d.Delta.Type == "text_delta" {
					out <- client.Frame{Kind: client.KindDelta, Text: d.Delta.Text}
				}
			case "message_delta":
				var d struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
				}
				if json.Unmarshal([]byte(data), &d) == nil && d.Delta.StopReason != "" {
					out <- client.Frame{Kind: client.KindStop, Stop: d.Delta.StopReason}
				}
			case "message_stop":
				out <- client.Frame{Kind: client.KindEnd}
				return nil
			}
		}
	}
	out <- client.Frame{Kind: client.KindEnd}
	return sc.Err()
}
