// RelayLLMCaller — production LLMCaller backed by model-relay /v1/messages.
//
// Mirrors services/brain/internal/wiki/reviews/llm_filter.go's pattern:
// brain mints a short-lived JWT for the project owner, then posts a
// non-streaming chat completion to model-relay. model-relay resolves the user's BYOK
// or platform pool, runs the model, returns a single message.
//
// Caller lifetime: one instance per brain process. Safe for concurrent
// use — the underlying http.Client is the only shared mutable state.
package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type RelayLLMCaller struct {
	RelayURL  string
	Model   string
	Signer  *bauth.Signer
	HTTP    *http.Client
	Timeout time.Duration
	Logger  *slog.Logger
}

func NewRelayLLMCaller(relayURL, model string, signer *bauth.Signer, logger *slog.Logger) *RelayLLMCaller {
	return &RelayLLMCaller{
		RelayURL:  strings.TrimRight(relayURL, "/"),
		Model:   model,
		Signer:  signer,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		Timeout: 45 * time.Second,
		Logger:  logger,
	}
}

func (h *RelayLLMCaller) Chat(ctx context.Context, ownerID uuid.UUID, system, user string) (string, error) {
	if h.RelayURL == "" || h.Signer == nil {
		return "", errors.New("model-relay caller misconfigured: RelayURL or Signer empty")
	}
	jwt, err := h.Signer.Sign(&bauth.Claims{UserID: ownerID.String()})
	if err != nil {
		return "", fmt.Errorf("mint jwt: %w", err)
	}
	body := map[string]any{
		"model":      h.Model,
		"stream":     false,
		"max_tokens": 1024,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": user},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	cctx := ctx
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		h.RelayURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("model-relay %d: %s", resp.StatusCode, string(buf))
	}
	// model-relay /v1/messages mirrors OpenAI shape — choices[0].message.content.
	var hubResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hubResp); err != nil {
		return "", fmt.Errorf("decode Relay: %w", err)
	}
	if len(hubResp.Choices) == 0 {
		return "", errors.New("model-relay returned no choices")
	}
	return hubResp.Choices[0].Message.Content, nil
}
