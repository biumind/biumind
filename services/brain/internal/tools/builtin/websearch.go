package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/services/brain/internal/search/searxng"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// WebSearch returns the SearxNG-backed websearch tool. The cloud
// AgentLoop sends this tool definition to the LLM; when the model
// emits a tool_use, the loop invokes this tool which queries the
// configured SearxNG instance and returns top-N results.
//
// Runtime is RuntimeBoth so client-mode agents (W7) can also call it
// — typically against the user's own SearxNG (corporate / self-host)
// rather than going through Brain. The server-side invoker is what
// this factory wires up; the client-side invoker lives in Flutter
// ToolHost.
//
// We deliberately do NOT support Tavily/Bocha here yet — SearxNG
// covers self-host + 70+ search engines and is the only provider with
// existing infrastructure in brain. Adding more is a follow-up
// (file-per-provider, then a router that picks based on user config).
func WebSearch(client *searxng.Client) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Search query (will be passed verbatim to the search engine)."
    },
    "limit": {
      "type": "integer",
      "description": "Max results to return; defaults to 8, capped at 20.",
      "minimum": 1,
      "maximum": 20
    }
  },
  "required": ["query"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "websearch",
			Description: "Search the web for up-to-date information. Returns ranked results with title, URL and snippet. Use when the user's question requires fresh information you don't have, or when citing sources matters.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeBoth,
		},
		ReadOnly:  true,
		Retrieval: true,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("websearch: bad input: %w", err)
			}
			if args.Query == "" {
				return nil, fmt.Errorf("websearch: query is required")
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 8
			}
			if limit > 20 {
				limit = 20
			}
			hits, err := client.Search(ctx, args.Query, limit)
			if err != nil {
				return nil, fmt.Errorf("websearch: %w", err)
			}
			// Project to a stable shape independent of the underlying
			// search backend so swapping SearxNG for Tavily later
			// doesn't change what the LLM sees.
			results := make([]map[string]any, 0, len(hits))
			for _, h := range hits {
				results = append(results, map[string]any{
					"title":   h.Title,
					"url":     h.URL,
					"snippet": h.Snippet,
					"score":   h.Score,
					"engine":  h.Engine,
				})
			}
			return map[string]any{
				"query":   args.Query,
				"results": results,
			}, nil
		},
	}
}
