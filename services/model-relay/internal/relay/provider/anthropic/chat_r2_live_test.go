// R2 — manual smoke test for source.type=url support against a real
// Anthropic-compatible endpoint. Skipped by default; run with:
//
//	BIUMIND_R2_LIVE=1 \
//	  ANTHROPIC_API_KEY=<key> \
//	  ANTHROPIC_BASE_URL=<base, e.g. https://api.anthropic.com> \
//	  go test -run TestR2_AnthropicURLImage \
//	    ./services/model-relay/internal/relay/provider/anthropic/
//
// Why a manual gate: the test sends the API key to an external service.
// CI / dev sandboxes shouldn't auto-run this. Adapter unit tests already
// confirm we generate the correct request shape; this test confirms the
// remote endpoint actually accepts source.type=url. Even if it doesn't,
// the adapter falls back to base64 inline (a.rewriteFileBlock fallback
// path), so this is risk-mitigation, not a critical-path verification.

package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestR2_AnthropicURLImage(t *testing.T) {
	if os.Getenv("BIUMIND_R2_LIVE") != "1" {
		t.Skip("R2 live test gated behind BIUMIND_R2_LIVE=1")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY unset")
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	// Tiny public PNG (Wikipedia transparency demo, ~7KB).
	imageURL := "https://upload.wikimedia.org/wikipedia/commons/thumb/4/47/PNG_transparency_demonstration_1.png/120px-PNG_transparency_demonstration_1.png"

	body := map[string]any{
		"model":      "claude-3-5-sonnet-20241022",
		"max_tokens": 50,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "Reply with just OK if you can see the image."},
				{"type": "image", "source": map[string]any{
					"type": "url", "url": imageURL,
				}},
			},
		}},
	}
	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("status=%d body=%s", resp.StatusCode, string(respBody))

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}
	// Sanity: response should mention "OK" somewhere (model saw image).
	// Don't be strict — just confirm we got a coherent response.
	if !bytes.Contains(respBody, []byte(`"text"`)) {
		t.Errorf("response missing text content: %s", respBody)
	}
}
