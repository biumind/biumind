// RelayVisionCaller — production Caller backed by model-relay /v1/messages.
//
// Same auth pattern as enrich.RelayLLMCaller and reviews.HubLLMFilter:
// brain mints a 5-min JWT for the project owner so model-relay resolves the
// user's BYOK / pool credentials. The request body uses the multimodal
// `parts` shape model-relay passes through to Anthropic verbatim.
package vision

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

type RelayVisionCaller struct {
	RelayURL  string
	Model   string
	Signer  *bauth.Signer
	HTTP    *http.Client
	Timeout time.Duration
	Logger  *slog.Logger
}

func NewRelayVisionCaller(relayURL, model string, signer *bauth.Signer, logger *slog.Logger) *RelayVisionCaller {
	return &RelayVisionCaller{
		RelayURL:  strings.TrimRight(relayURL, "/"),
		Model:   model,
		Signer:  signer,
		HTTP:    &http.Client{Timeout: 90 * time.Second},
		Timeout: 75 * time.Second,
		Logger:  logger,
	}
}

func (h *RelayVisionCaller) Caption(ctx context.Context, ownerID uuid.UUID, imageBytes []byte, mediaType string) (string, error) {
	if h.RelayURL == "" || h.Signer == nil {
		return "", errors.New("model-relay vision caller misconfigured")
	}
	if len(imageBytes) == 0 {
		return "", errors.New("empty image bytes")
	}
	jwt, err := h.Signer.Sign(&bauth.Claims{UserID: ownerID.String()})
	if err != nil {
		return "", fmt.Errorf("mint jwt: %w", err)
	}

	// Anthropic-shape multimodal user content: text + image. model-relay passes
	// `parts` through verbatim so the upstream provider receives the
	// blocks it expects.
	parts := []map[string]any{
		{"type": "text", "text": CaptionPrompt},
		{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mediaType,
				"data":       EncodeBase64(imageBytes),
			},
		},
	}
	partsJSON, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}

	body := map[string]any{
		"model":      h.Model,
		"stream":     false,
		"max_tokens": 512,
		"messages": []map[string]any{
			{
				"role":  "user",
				"parts": json.RawMessage(partsJSON),
			},
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
