// AgentBackground — fire-and-forget variant of AgentTool.
//
// Implements the "spawn into team" pattern at minimal scope (P20.53):
// the model wants a long-running sub-task to proceed in parallel with
// its own continued work, not to block the current turn. The tool
// returns a handle synchronously; the teammate runs in a goroutine
// and its final output is delivered as a system message at the head
// of the next user turn (see engine.swarm.go).
//
// When to use AgentBackground vs Agent:
//
//   - Agent (sync): the parent NEEDS the result before continuing.
//     Returns the answer text inline. Default choice for
//     "explore X then summarise" or "answer this question for me."
//
//   - AgentBackground (async): the parent wants to start work and
//     keep moving. Returns just the handle. Useful for
//     "research X while I work on Y" or "kick off the test suite
//     and tell me when it finishes."

package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// AgentBackgroundToolName is the model-facing name. Matches the
// established CamelCase convention biu uses for engine tools.
const AgentBackgroundToolName = "AgentBackground"

// AgentBackgroundTool is the async sibling of AgentTool. Same input
// shape (so the model can swap between sync/async with one flag),
// but it doesn't block — Call returns once the goroutine is spawned.
type AgentBackgroundTool struct {
	// Registry is the same agent definition registry AgentTool uses.
	// Optional: nil ⇒ subagent_type is just a label, no overrides.
	Registry AgentRegistry

	// Teams, when non-nil, lets AgentBackground register the spawned
	// teammate under a team's friendly-name addressing layer. The
	// model passes team_name + member_name in input; we look up the
	// team, store the member→handle mapping, and SendMessage routes
	// future follow-ups by that name. Optional: when nil, the tool
	// works as in P20.53-1 (handle-only addressing).
	Teams *engine.TeamRegistry
}

func (AgentBackgroundTool) Name() string { return AgentBackgroundToolName }

func (a AgentBackgroundTool) Description(_ map[string]any) string {
	base := "Spawn a sub-agent in the background. Returns immediately " +
		"with a teammate handle (e.g. 'agent-3'); the sub-agent runs " +
		"to completion in its own goroutine and its final output is " +
		"delivered as a system attachment at the head of your next " +
		"user turn. Use this when you want to fire off long-running " +
		"work (research, test runs, code analysis) without blocking " +
		"your own progress."
	if a.Registry != nil {
		if names := a.Registry.Names(); len(names) > 0 {
			base += "\n\nRegistered subagent types: " + strings.Join(names, ", ")
		}
	}
	return base
}

func (AgentBackgroundTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Friendly label for the sub-agent type (e.g. 'general-purpose', 'explore'). Defaults to 'general-purpose'.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short (3-5 word) summary of the sub-agent's task — appears in the teammate-completion attachment so you can recognise the result.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The full task description for the sub-agent.",
			},
			"team_name": map[string]any{
				"type":        "string",
				"description": "Optional: register this teammate into the named team (must already exist via TeamCreate). Pair with member_name to enable SendMessage{team, member, ...} routing.",
			},
			"member_name": map[string]any{
				"type":        "string",
				"description": "Optional: friendly name for this teammate within the team (e.g. 'researcher', 'lead'). Required when team_name is set.",
			},
		},
		"required": []string{"prompt"},
	}
}

// Async spawn is read-only from the parent's perspective: it just
// stashes a handle. Concurrency-safe because the spawner serialises
// MarkActive on its own mutex.
func (AgentBackgroundTool) IsReadOnly(_ map[string]any) bool        { return true }
func (AgentBackgroundTool) IsDestructive(_ map[string]any) bool     { return false }
func (AgentBackgroundTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (AgentBackgroundTool) InterruptBehavior() string               { return "cancel" }

func (a AgentBackgroundTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.Spawner == nil {
		return softErr(AgentBackgroundToolName, "no sub-agent spawner configured"), nil
	}
	asyncSpawner, ok := env.Spawner.(engine.AsyncSpawner)
	if !ok {
		return softErr(AgentBackgroundToolName,
			"engine does not support async spawning (use Agent for synchronous delegation)"), nil
	}
	prompt, _ := input["prompt"].(string)
	if prompt == "" {
		return softErr(AgentBackgroundToolName, "prompt is required"), nil
	}
	agentType, _ := input["subagent_type"].(string)
	if agentType == "" {
		agentType = "general-purpose"
	}
	desc, _ := input["description"].(string)

	req := engine.AgentSpawnRequest{
		AgentType:       agentType,
		Description:     desc,
		Prompt:          prompt,
		ParentToolUseID: env.ToolUseID,
	}
	// Apply the registered definition's overrides so AgentBackground
	// honours system prompt / tool whitelist / model overrides exactly
	// like AgentTool does. Skip when no registry is wired.
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

	handle, err := asyncSpawner.SpawnAsync(ctx, req)
	if err != nil {
		return softErr(AgentBackgroundToolName,
			fmt.Sprintf("spawn failed: %v", err)), nil
	}

	// Optional team registration. When team_name + member_name are
	// supplied AND the engine has a TeamRegistry wired, link the
	// freshly-spawned handle into the team's friendly-name graph.
	teamName, _ := input["team_name"].(string)
	memberName, _ := input["member_name"].(string)
	teamSuffix := ""
	if teamName != "" || memberName != "" {
		if teamName == "" || memberName == "" {
			return softErr(AgentBackgroundToolName,
				"team_name and member_name must both be set, or both empty"), nil
		}
		if a.Teams == nil {
			return softErr(AgentBackgroundToolName,
				"team registry not available on this engine — drop team_name / member_name"), nil
		}
		if err := a.Teams.AddMember(teamName, memberName, handle.ID); err != nil {
			return softErr(AgentBackgroundToolName,
				fmt.Sprintf("team registration: %v", err)), nil
		}
		teamSuffix = fmt.Sprintf(" Registered as member %q in team %q.",
			memberName, teamName)
	}

	msg := fmt.Sprintf(
		"Spawned %s (type=%s)%s — running in the background.%s "+
			"Its final output will arrive as a system attachment at the "+
			"head of your next user turn. Continue with other work.",
		handle.ID, handle.AgentType, descSuffix(desc), teamSuffix)
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: msg}},
	}, nil
}

func descSuffix(desc string) string {
	if desc == "" {
		return ""
	}
	return " — " + desc
}

// Compile-time check: AgentBackgroundTool also satisfies engine.Tool.
var _ engine.Tool = AgentBackgroundTool{}

// agents package import is intentional — even when we don't use it
// directly here, it documents the dependency that AgentRegistry
// implementations (the typical caller passes agents.Registry) carry.
var _ = agents.Definition{}
