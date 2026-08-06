// Package builtin holds the canonical cloud-side tool implementations
// that ship with Brain Chat. Each file registers one tool family;
// init wiring pulls them into the global tools.Registry at startup
// (see cmd/brain/main.go).
//
// The point of having a `builtin` subpackage rather than registering
// inside `tools` itself is dependency direction: brain.memory /
// brain.search may depend on tools (e.g. to resolve a Descriptor),
// and tools must NOT pull in those packages or the import graph
// becomes a cycle. Builtin sits "below" memory/search in the import
// order and pulls them in freely.
package builtin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// TimeNow returns a tool that reports the current UTC time. It's the
// hello-world of cloud tools — proves the agent loop end-to-end with
// no external dependency.
//
// LLM model card: tell me what time it is → model emits tool_use{
// name: "time.now"} → AgentLoop invokes → tool_result is the
// timestamp → model produces a reply.
func TimeNow() tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "timezone": {
      "type": "string",
      "description": "IANA timezone name (e.g. \"Asia/Shanghai\"). Defaults to UTC."
    }
  }
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			// Anthropic restricts tool names to ^[a-zA-Z0-9_-]{1,128}$
			// — no dots. We snake_case across the entire builtin tool
			// family for consistency, regardless of which provider
			// receives them. Other providers (OpenAI / etc.) accept
			// the same shape.
			Name:        "time_now",
			Description: "Returns the current wall-clock time as RFC3339 timestamp. Optionally formatted in a specific IANA timezone.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeBoth,
		},
		ReadOnly: true,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Timezone string `json:"timezone"`
			}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &args)
			}
			loc := time.UTC
			if args.Timezone != "" {
				if l, err := time.LoadLocation(args.Timezone); err == nil {
					loc = l
				}
			}
			now := time.Now().In(loc)
			return map[string]any{
				"iso":      now.Format(time.RFC3339),
				"unix":     now.Unix(),
				"timezone": loc.String(),
			}, nil
		},
	}
}
