// AgentTool — fan out a sub-agent run.
//
// The model uses this to delegate a focused sub-task to a fresh
// QueryEngine instance with its own context budget. Parent only sees
// the final assistant text, so the sub-agent's tool turns don't leak
// into parent context.
//
// Invocation:
//
//   {
//     "subagent_type": "explore",
//     "description":   "Find auth code paths",
//     "prompt":        "Search the repo for files implementing JWT validation."
//   }
//
// Returns: the sub-agent's last assistant message as plain text.
//
// When `subagent_type` matches an entry in the AgentRegistry (loaded
// from ~/.biumind/agents/<name>.md), the registered system prompt,
// model, permission mode, and tool whitelist are applied. Unknown
// types fall back to "general-purpose" semantics — same catalog as
// the parent, no override.

package orchestration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// AgentRegistry is the lookup contract the AgentTool depends on. The
// agents package satisfies it directly; tests can plug in a stub
// without pulling the loader.
type AgentRegistry interface {
	Lookup(name string) (*agents.Definition, bool)
	Names() []string
}

type AgentTool struct {
	// Registry is optional — when nil, every subagent_type falls
	// through to "general-purpose" semantics (parent catalog, no
	// override). When set, the tool applies the matching definition.
	Registry AgentRegistry
}

func (a AgentTool) Name() string { return "Agent" }

func (a AgentTool) Description(_ map[string]any) string {
	base := "Spawn a sub-agent with a fresh context to handle a focused task. " +
		"The sub-agent has access to the same tools and returns one final " +
		"text answer to you. Use for parallel research, scoped exploration, " +
		"or to keep your own context lean on long tasks."
	if a.Registry == nil {
		return base
	}
	names := a.Registry.Names()
	if len(names) == 0 {
		return base
	}
	// Inline brief catalog of known agent types so the model knows
	// which subagent_type values are meaningful. We don't dump the
	// full description here — that's discovered via the registry on
	// dispatch.
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nRegistered subagent types:")
	defs := make([]*agents.Definition, 0, len(names))
	for _, n := range names {
		if d, ok := a.Registry.Lookup(n); ok {
			defs = append(defs, d)
		}
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	for _, d := range defs {
		fmt.Fprintf(&b, "\n  - %s — %s", d.Name, oneLine(d.Description))
	}
	return b.String()
}

func (a AgentTool) InputSchema() map[string]any {
	subtype := map[string]any{
		"type":        "string",
		"description": "Friendly label for the sub-agent type (e.g. 'general-purpose', 'explore').",
	}
	if a.Registry != nil {
		names := a.Registry.Names()
		if len(names) > 0 {
			// Add a JSON-Schema enum so the model is steered toward
			// known types rather than freestyling. We start with
			// "general-purpose" as a guaranteed-present fallback
			// (legacy callers without a registered Definition still
			// get vanilla-request semantics on this string), then
			// append registered names while skipping any duplicate
			// of the fallback so the enum doesn't repeat itself.
			values := make([]any, 0, len(names)+1)
			values = append(values, "general-purpose")
			seen := map[string]bool{"general-purpose": true}
			for _, n := range names {
				if seen[n] {
					continue
				}
				seen[n] = true
				values = append(values, n)
			}
			subtype["enum"] = values
		}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": subtype,
			"description": map[string]any{
				"type":        "string",
				"description": "Short (3-5 word) summary of what the sub-agent will do.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The full task description for the sub-agent.",
			},
		},
		"required": []string{"prompt"},
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// Sub-agents read; they may also write — but they manage their own
// state machine. From the *parent's* perspective the call is one
// opaque request → one opaque response: read-only, concurrency-safe.
// Multiple Agent calls in a single turn can run in parallel.
func (AgentTool) IsReadOnly(_ map[string]any) bool        { return true }
func (AgentTool) IsDestructive(_ map[string]any) bool     { return false }
func (AgentTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (AgentTool) InterruptBehavior() string               { return "cancel" }

func (a AgentTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.Spawner == nil {
		return softErr("Agent", "no sub-agent spawner configured"), nil
	}
	prompt, _ := input["prompt"].(string)
	if prompt == "" {
		return softErr("Agent", "prompt is required"), nil
	}
	agentType, _ := input["subagent_type"].(string)
	if agentType == "" {
		agentType = "general-purpose"
	}
	desc, _ := input["description"].(string)

	// Default to a vanilla request — every parent-inherited setting.
	// ParentToolUseID propagates from the running tool_use so events
	// the sub-agent emits link back to this AgentTool call (F13).
	req := engine.AgentSpawnRequest{
		AgentType:       agentType,
		Description:     desc,
		Prompt:          prompt,
		ParentToolUseID: env.ToolUseID,
	}
	// Apply registered definition's overrides when known.
	if a.Registry != nil {
		if def, ok := a.Registry.Lookup(agentType); ok {
			req.System = def.SystemPrompt
			req.Model = def.Model
			req.MaxTurns = def.MaxTurns
			if def.PermissionMode != "" {
				req.PermissionMode = string(def.PermissionMode)
			}
			req.AllowedTools = def.Tools
			req.DisallowedTools = def.DisallowedTools
		}
	}

	res, err := env.Spawner.Spawn(ctx, req)
	if err != nil {
		return softErr("Agent", fmt.Sprintf("sub-agent failed: %v", err)), nil
	}
	if res.Output == "" {
		return softErr("Agent", "sub-agent produced no output"), nil
	}
	// Annotate the result with the agent type so the parent's reading
	// knows it was a sub-agent.
	tagged := fmt.Sprintf("[%s] %s", agentType, res.Output)
	return text(tagged), nil
}
