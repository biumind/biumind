// WebSearch — query a search engine and return ranked results.
//
// Backend is pluggable via the Provider interface so the operator
// can pick:
//
//   * SearxNG (self-hosted, default)
//   * BiuMind model-relay (forwarded to whatever search backend model-relay uses)
//   * any custom Provider
//
// The result format is intentionally plain — title + url + snippet,
// joined as numbered markdown — because the model can't be trusted to
// parse JSON consistently across providers.

package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// SearchResult is one ranked hit.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchProvider is the contract WebSearchTool depends on. SearxNG +
// model-relay adapters live in client/.
type SearchProvider interface {
	WebSearch(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// WebSearchTool wraps an injected SearchProvider. When Provider is
// nil the tool soft-errors so the model can fall back to WebFetch
// with a known URL.
type WebSearchTool struct {
	Provider SearchProvider
	// DefaultLimit is the results-per-query cap if input.limit is
	// missing. 5 by default.
	DefaultLimit int
}

func (WebSearchTool) Name() string { return "WebSearch" }

func (WebSearchTool) Description(_ map[string]any) string {
	return "Search the web. Returns titles + URLs + snippets ranked " +
		"by relevance. Follow with WebFetch on the most promising URL."
}

func (WebSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		"required": []string{"query"},
	}
}

func (WebSearchTool) IsReadOnly(_ map[string]any) bool        { return true }
func (WebSearchTool) IsDestructive(_ map[string]any) bool     { return false }
func (WebSearchTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (WebSearchTool) InterruptBehavior() string               { return "cancel" }

func (w WebSearchTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if w.Provider == nil {
		return softErr("WebSearch", "no search provider configured"), nil
	}
	query, _ := input["query"].(string)
	if strings.TrimSpace(query) == "" {
		return softErr("WebSearch", "query required"), nil
	}
	limit := w.DefaultLimit
	if limit == 0 {
		limit = 5
	}
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	results, err := w.Provider.WebSearch(ctx, query, limit)
	if err != nil {
		return softErr("WebSearch", err.Error()), nil
	}
	if len(results) == 0 {
		return text("No results."), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. [%s](%s)\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return text(strings.TrimRight(b.String(), "\n")), nil
}
