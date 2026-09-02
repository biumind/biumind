package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/services/brain/internal/tools"
	"github.com/biumind/biumind/services/brain/internal/wiki/chunks"
)

// WikiSearch returns the cloud RAG retrieval tool. It embeds the
// query, runs a vector ANN against brain.wiki_chunks, and returns
// the top hits scoped to the calling user (and optionally a project).
//
// owner-scoping is critical here — the registry's Invoke signature
// doesn't carry user identity, so we pull it from
// tools.UserIDFromContext. Callers MUST attach a userID via
// tools.WithUserID before invoking; chat.HandleSend already does this
// at the start of every send. A missing user id returns an error
// rather than silently leaking other users' wiki content.
//
// Runtime is RuntimeBoth: server invokes via the chunks store
// directly; client invokes via the /v1/tools/invoke proxy (W4.4).
// The proxy enforces JWT → user id binding, so the same owner-scope
// guard works in both modes.
func WikiSearch(store *chunks.Store, embedder embed.Embedder) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Natural-language question. Will be embedded and matched against the user's wiki via cosine ANN."
    },
    "project_id": {
      "type": "string",
      "description": "Optional UUID; restricts search to this project. Omit for cross-project."
    },
    "limit": {
      "type": "integer",
      "description": "Max hits to return; default 8, capped at 30.",
      "minimum": 1,
      "maximum": 30
    }
  },
  "required": ["query"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			// Anthropic restricts tool names to ^[a-zA-Z0-9_-]{1,128}$
			// — no dots. snake_case across builtin tool family.
			Name:        "wiki_search",
			Description: "Search the user's personal wiki for relevant passages. Best for grounded answers about the user's projects, notes and decisions. Returns ranked excerpts with page titles.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeBoth,
		},
		ReadOnly:  true,
		Retrieval: true,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			ownerID := tools.UserIDFromContext(ctx)
			if ownerID == uuid.Nil {
				return nil, errors.New("wiki.search: missing user identity in context")
			}

			var args struct {
				Query     string `json:"query"`
				ProjectID string `json:"project_id"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("wiki.search: bad input: %w", err)
			}
			if args.Query == "" {
				return nil, errors.New("wiki.search: query required")
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 8
			}
			if limit > 30 {
				limit = 30
			}

			vec, err := embedder.Embed(ctx, args.Query)
			if err != nil {
				return nil, fmt.Errorf("wiki.search: embed: %w", err)
			}

			in := chunks.SearchInput{
				OwnerID:        ownerID,
				QueryEmbedding: vec,
				Limit:          limit,
			}
			if args.ProjectID != "" {
				pid, err := uuid.Parse(args.ProjectID)
				if err != nil {
					return nil, fmt.Errorf("wiki.search: bad project_id: %w", err)
				}
				in.ProjectID = &pid
			}

			hits, err := store.Search(ctx, in)
			if err != nil {
				return nil, fmt.Errorf("wiki.search: %w", err)
			}

			results := make([]map[string]any, 0, len(hits))
			for _, h := range hits {
				results = append(results, map[string]any{
					"page_id": h.PageID,
					"title":   h.Title,
					"snippet": h.Snippet,
					"score":   h.Score,
					"updated_at": h.UpdatedAt.Format(
						"2006-01-02T15:04:05Z07:00"),
				})
			}
			return map[string]any{
				"query":   args.Query,
				"results": results,
			}, nil
		},
	}
}
