// Team / SendMessage tools (P20.53-2). Layer on top of the async
// agent runtime (engine.swarm.go): teams give friendly names to
// teammates the model spawns via AgentBackground; SendMessage
// queues follow-up prompts for a still-running teammate.
//
// Design notes:
//
//   - Teams are process-scoped. The current biu CLI session owns the
//     graph; restart loses it. A future commit can persist to disk
//     for cross-restart continuity (e.g. ~/.biumind/teams/<name>/config.json).
//   - SendMessage delivers via *queue*, not interrupt. The teammate's
//     current Submit completes, then SpawnAsync's goroutine pulls
//     the head of the queue and re-Submits with the queued body.
//     This means messages can pile up if a teammate is busy; that's
//     fine for the typical "team-lead delegates a few tasks" pattern
//     and avoids the engine-side surgery a true mid-run interrupt
//     would need.
//   - Resolution: the model addresses by `(team, name)` pair. The
//     tool resolves to a handle id via TeamRegistry, then enqueues
//     into MessageInbox. Teammates that aren't in any team can be
//     addressed by handle id directly via the `to` field.

package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── TeamCreate ───────────────────────────────────────────────────

const TeamCreateToolName = "TeamCreate"

type TeamCreateTool struct {
	Teams *engine.TeamRegistry
}

func (TeamCreateTool) Name() string { return TeamCreateToolName }
func (TeamCreateTool) Description(_ map[string]any) string {
	return "Create a named team to coordinate multiple async sub-agents. " +
		"Teams give your teammates friendly names (\"researcher\", " +
		"\"tester\") that you can use with SendMessage and AgentBackground. " +
		"Use proactively when a task has parallelisable work — research + " +
		"implementation, frontend + backend, planning + execution.\n\n" +
		"After TeamCreate, spawn teammates with AgentBackground{team_name, " +
		"member_name, ...} and assign work via SendMessage{team, member, " +
		"message, ...}."
}
func (TeamCreateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"team_name": map[string]any{
				"type":        "string",
				"description": "Short identifier for the team (e.g. 'auth-rewrite'). Must be unique within this session.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional one-line description of what the team is for.",
			},
		},
		"required": []string{"team_name"},
	}
}
func (TeamCreateTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TeamCreateTool) IsDestructive(_ map[string]any) bool     { return false }
func (TeamCreateTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (TeamCreateTool) InterruptBehavior() string               { return "block" }

func (t TeamCreateTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if t.Teams == nil {
		return softErr(TeamCreateToolName, "team registry not available on this engine"), nil
	}
	name, _ := input["team_name"].(string)
	desc, _ := input["description"].(string)
	if name == "" {
		return softErr(TeamCreateToolName, "team_name is required"), nil
	}
	team, err := t.Teams.Create(name, desc)
	if err != nil {
		return softErr(TeamCreateToolName, err.Error()), nil
	}
	msg := fmt.Sprintf(
		"Created team %q. Spawn teammates with AgentBackground "+
			"(pass team_name=%q + member_name=<friendly-name>) and "+
			"address them later via SendMessage.",
		team.Name, team.Name)
	return plainResult(msg), nil
}

// ─── TeamDelete ───────────────────────────────────────────────────

const TeamDeleteToolName = "TeamDelete"

type TeamDeleteTool struct {
	Teams *engine.TeamRegistry
}

func (TeamDeleteTool) Name() string { return TeamDeleteToolName }
func (TeamDeleteTool) Description(_ map[string]any) string {
	return "Delete a team. In-flight teammates of this team are NOT " +
		"cancelled — they finish their current work and post their final " +
		"output to your next user-turn attachment as usual. The team " +
		"deletion just removes the addressing layer (you can no longer " +
		"reach members by name; use handle ids if you still need to " +
		"send messages)."
}
func (TeamDeleteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"team_name": map[string]any{
				"type":        "string",
				"description": "Name of the team to delete.",
			},
		},
		"required": []string{"team_name"},
	}
}
func (TeamDeleteTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TeamDeleteTool) IsDestructive(_ map[string]any) bool     { return true }
func (TeamDeleteTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (TeamDeleteTool) InterruptBehavior() string               { return "block" }

func (t TeamDeleteTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if t.Teams == nil {
		return softErr(TeamDeleteToolName, "team registry not available on this engine"), nil
	}
	name, _ := input["team_name"].(string)
	if name == "" {
		return softErr(TeamDeleteToolName, "team_name is required"), nil
	}
	team, ok := t.Teams.Delete(name)
	if !ok {
		return softErr(TeamDeleteToolName,
			fmt.Sprintf("no team %q (use TeamList to see active teams)", name)), nil
	}
	count := len(team.Members)
	return plainResult(fmt.Sprintf(
		"Deleted team %q (%d members were registered). In-flight teammates "+
			"will still report their final outputs.", team.Name, count)), nil
}

// ─── SendMessage ──────────────────────────────────────────────────

const SendMessageToolName = "SendMessage"

type SendMessageTool struct {
	Teams    *engine.TeamRegistry
	Messages *engine.MessageInbox
}

func (SendMessageTool) Name() string { return SendMessageToolName }
func (SendMessageTool) Description(_ map[string]any) string {
	return "Send a follow-up message to a teammate (a sub-agent spawned " +
		"via AgentBackground). The teammate will receive the message as " +
		"its next prompt after its current Submit completes — i.e. once " +
		"the teammate goes idle. Until then the message stays queued.\n\n" +
		"Address forms:\n" +
		"  team + member  — friendly name within a team (preferred)\n" +
		"  handle         — direct teammate handle id (e.g. 'agent-3')\n\n" +
		"Use this to: assign new work to an idle teammate, send corrections, " +
		"forward results from one teammate to another, or shut down a team."
}
func (SendMessageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"team": map[string]any{
				"type":        "string",
				"description": "Team name (use with member). Pair with `member` to address by friendly name.",
			},
			"member": map[string]any{
				"type":        "string",
				"description": "Friendly member name within the team (use with team).",
			},
			"handle": map[string]any{
				"type":        "string",
				"description": "Direct teammate handle id (e.g. 'agent-3'). Use when the teammate isn't in a team or you already know its id.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Plain-text body of the follow-up. Will be delivered as the teammate's next user prompt.",
			},
			"from": map[string]any{
				"type":        "string",
				"description": "Optional sender label that surfaces in the teammate's <message-from sender=\"…\"/> system note. Defaults to 'team-lead'.",
			},
		},
		"required": []string{"message"},
	}
}
func (SendMessageTool) IsReadOnly(_ map[string]any) bool        { return false }
func (SendMessageTool) IsDestructive(_ map[string]any) bool     { return false }
func (SendMessageTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (SendMessageTool) InterruptBehavior() string               { return "cancel" }

func (s SendMessageTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if s.Messages == nil {
		return softErr(SendMessageToolName, "message inbox not available on this engine"), nil
	}
	body, _ := input["message"].(string)
	body = strings.TrimSpace(body)
	if body == "" {
		return softErr(SendMessageToolName, "message body is required"), nil
	}
	from, _ := input["from"].(string)
	if from == "" {
		from = "team-lead"
	}

	handle, _ := input["handle"].(string)
	if handle == "" {
		// Resolve via team + member.
		team, _ := input["team"].(string)
		member, _ := input["member"].(string)
		if team == "" || member == "" {
			return softErr(SendMessageToolName,
				"either `handle` OR (`team` AND `member`) is required"), nil
		}
		if s.Teams == nil {
			return softErr(SendMessageToolName,
				"team registry not available; use `handle` directly"), nil
		}
		resolved, ok := s.Teams.ResolveMember(team, member)
		if !ok {
			return softErr(SendMessageToolName,
				fmt.Sprintf("no member %q in team %q", member, team)), nil
		}
		handle = resolved
	}

	depth := s.Messages.Enqueue(handle, engine.PendingMessage{Body: body, From: from})
	return plainResult(fmt.Sprintf(
		"Queued message for %s (%d in queue from %s). It will be "+
			"delivered as the teammate's next prompt once it goes idle.",
		handle, depth, from)), nil
}

// Compile-time interface checks.
var (
	_ engine.Tool = TeamCreateTool{}
	_ engine.Tool = TeamDeleteTool{}
	_ engine.Tool = SendMessageTool{}
)

// teamPlainResult / teamSoftErr — local helpers so this file doesn't
// depend on todo.go's softErr name resolution order. (Kept private
// because they're implementation details, not API.)
//
// Actually we already have plainResult/softErr in this package via
// todo.go and toolsearch.go — reuse those rather than redefining.
// (Documenting the choice so future readers don't add duplicates.)

// state import is used by callers that build ToolResultPayload via
// our shared plainResult helper.
var _ = state.ContentText
