// WebFetch — pull a URL's text content into a tool result.
//
// Implementation is intentionally minimal: GET the URL, strip HTML to
// text, truncate to a safe size. SSL verification stays on; redirects
// follow up to 10 hops; non-2xx responses surface as soft errors so
// the model can react.
//
// We don't run a headless browser — JS-rendered SPAs return their
// pre-render HTML, which is usually empty for those sites. When fetch
// returns an empty body the model is told to use the search tool
// instead.

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

const webFetchMaxBytes = 256 * 1024 // 256 KB cap

type WebFetchTool struct {
	HTTP *http.Client
}

func (WebFetchTool) Name() string { return "WebFetch" }

func (WebFetchTool) Description(_ map[string]any) string {
	return "Fetch a URL and return its text content. HTML is stripped " +
		"to a markdown-ish plain text. Use for static documentation " +
		"pages; SPAs may return empty bodies."
}

func (WebFetchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "format": "uri"},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Optional: ignored by biu (kept for prompt-compat).",
			},
		},
		"required": []string{"url"},
	}
}

func (WebFetchTool) IsReadOnly(_ map[string]any) bool        { return true }
func (WebFetchTool) IsDestructive(_ map[string]any) bool     { return false }
func (WebFetchTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (WebFetchTool) InterruptBehavior() string               { return "cancel" }

func (w WebFetchTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	url, _ := input["url"].(string)
	if url == "" {
		return softErr("WebFetch", "url required"), nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return softErr("WebFetch", "url must start with http:// or https://"), nil
	}
	cl := w.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return softErr("WebFetch", err.Error()), nil
	}
	req.Header.Set("User-Agent", "biu-webfetch/1.0")
	req.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.5")

	resp, err := cl.Do(req)
	if err != nil {
		return softErr("WebFetch", err.Error()), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return softErr("WebFetch",
			fmt.Sprintf("%s returned %d", url, resp.StatusCode)), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes))
	if err != nil && !errors.Is(err, io.EOF) {
		return softErr("WebFetch", err.Error()), nil
	}

	contentType := resp.Header.Get("content-type")
	cleaned := htmlToText(string(body))
	if !strings.Contains(contentType, "html") && !strings.Contains(contentType, "xml") {
		// Plain text already.
		cleaned = string(body)
	}
	if len(cleaned) > webFetchMaxBytes {
		cleaned = cleaned[:webFetchMaxBytes] + "\n…(truncated)"
	}
	return text(fmt.Sprintf("URL: %s\nStatus: %d\n\n%s",
		url, resp.StatusCode, cleaned)), nil
}

// htmlToText is a deliberately tiny HTML stripper. We don't pull
// goquery for this — the model rarely needs perfect parsing, just
// readable text. Strips:
//
//   - <script>...</script> blocks (with content)
//   - <style>...</style> blocks
//   - any other HTML tag (keeps inner text)
//   - collapses whitespace runs
//
// Entities are not decoded (&amp; survives) — fine for the LLM, which
// will infer them.
var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t]+`)
	reBlank  = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, "")
	s = reSpace.ReplaceAllString(s, " ")
	s = reBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
