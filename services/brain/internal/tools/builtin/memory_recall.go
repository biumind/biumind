package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	memorystore "github.com/biumind/biumind/services/brain/internal/memory/store"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// MemoryRecall returns the long-term-memory recall tool. The model
// supplies a project_id + free-text query; the tool runs the same
// hybrid (semantic + lexical + salience + recency) ranking as the
// brain.memory recall API and returns top hits.
//
// owner-scoping: ownerID is taken from tools.UserIDFromContext (set
// by chat.HandleSend or the /v1/tools/invoke proxy). uuid.Nil → hard
// error so cross-user leakage is impossible.
//
// Why project_id is in the tool input rather than ctx: brain.memory
// is project-scoped, and a single chat thread may legitimately want
// to search memories from project A then project B in successive
// turns. Putting it in input lets the LLM pick.
func MemoryRecall(store *memorystore.Store, embedder embed.Embedder) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Free-text query — what are you trying to remember?"
    },
    "project_id": {
      "type": "string",
      "description": "Project UUID to search within. Memories are project-scoped; ask the user when unclear."
    },
    "kind": {
      "type": "string",
      "enum": ["recall", "preference", "habit"],
      "description": "Optional filter. Omit to search all kinds."
    },
    "limit": {
      "type": "integer",
      "description": "Max hits; default 8, capped at 30.",
      "minimum": 1,
      "maximum": 30
    }
  },
  "required": ["query", "project_id"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			// Anthropic restricts tool names to ^[a-zA-Z0-9_-]{1,128}$
			// — no dots. snake_case across builtin tool family.
			Name:        "memory_recall",
			Description: "Recall long-term memories — facts, preferences, and habits the user has accumulated in a Brain project. Best for grounded answers about the user's past decisions, stated preferences, and learned patterns. Use BEFORE making assumptions; prefer recall over guessing.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeBoth,
		},
		ReadOnly: true,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			ownerID := tools.UserIDFromContext(ctx)
			if ownerID == uuid.Nil {
				return nil, errors.New("memory.recall: missing user identity in context")
			}

			var args struct {
				Query     string `json:"query"`
				ProjectID string `json:"project_id"`
				Kind      string `json:"kind"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("memory.recall: bad input: %w", err)
			}
			if args.Query == "" {
				return nil, errors.New("memory.recall: query required")
			}
			if args.ProjectID == "" {
				return nil, errors.New("memory.recall: project_id required")
			}
			pid, err := uuid.Parse(args.ProjectID)
			if err != nil {
				return nil, fmt.Errorf("memory.recall: bad project_id: %w", err)
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 8
			}
			if limit > 30 {
				limit = 30
			}
			if args.Kind != "" && !memorystore.ValidKind(args.Kind) {
				return nil, fmt.Errorf("memory.recall: invalid kind %q", args.Kind)
			}

			in := memorystore.RecallInput{
				ProjectID: pid,
				OwnerID:   ownerID,
				Query:     args.Query,
				Kind:      args.Kind,
				Limit:     limit,
			}

			// Embedder is optional — store.Recall falls back to lexical
			// when QueryEmbedding is empty. We only skip the embed call
			// when the embedder is nil (env says EMBED_PROVIDER=) so
			// we don't pay an unnecessary network round-trip.
			if embedder != nil {
				vec, err := embedder.Embed(ctx, args.Query)
				if err != nil {
					return nil, fmt.Errorf("memory.recall: embed: %w", err)
				}
				in.QueryEmbedding = vec
			}

			hits, err := store.Recall(ctx, in)
			if err != nil {
				return nil, fmt.Errorf("memory.recall: %w", err)
			}

			results := make([]map[string]any, 0, len(hits))
			for _, m := range hits {
				results = append(results, map[string]any{
					"id":               m.ID.String(),
					"kind":             m.Kind,
					"content":          m.Content,
					"salience":         m.Salience,
					"last_accessed_at": m.LastAccessedAt.Format("2006-01-02T15:04:05Z07:00"),
				})
			}
			return map[string]any{
				"query":   args.Query,
				"results": results,
			}, nil
		},
	}
}
