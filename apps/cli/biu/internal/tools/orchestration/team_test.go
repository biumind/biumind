package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// TestTeamCreateTool_Success — happy path round-trip.
func TestTeamCreateTool_Success(t *testing.T) {
	teams := engine.NewTeamRegistry()
	tool := TeamCreateTool{Teams: teams}
	res, _ := tool.Call(context.Background(), map[string]any{
		"team_name":   "auth-rewrite",
		"description": "auth refactor team",
	}, nil)
	if res.IsError {
		t.Fatalf("TeamCreate should succeed: %s", flatten(res))
	}
	if !strings.Contains(flatten(res), "Created team \"auth-rewrite\"") {
		t.Errorf("expected creation confirmation: %s", flatten(res))
	}
	if _, ok := teams.Get("auth-rewrite"); !ok {
		t.Errorf("team should be registered")
	}
}

// TestTeamCreateTool_Errors — missing name / duplicate / no registry.
func TestTeamCreateTool_Errors(t *testing.T) {
	teams := engine.NewTeamRegistry()
	tool := TeamCreateTool{Teams: teams}

	r1, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !r1.IsError || !strings.Contains(flatten(r1), "team_name is required") {
		t.Errorf("missing name should soft-error: %s", flatten(r1))
	}

	_, _ = tool.Call(context.Background(), map[string]any{"team_name": "x"}, nil)
	r2, _ := tool.Call(context.Background(), map[string]any{"team_name": "x"}, nil)
	if !r2.IsError || !strings.Contains(flatten(r2), "already exists") {
		t.Errorf("duplicate should soft-error: %s", flatten(r2))
	}

	nilTool := TeamCreateTool{Teams: nil}
	r3, _ := nilTool.Call(context.Background(), map[string]any{"team_name": "y"}, nil)
	if !r3.IsError {
		t.Errorf("nil Teams should soft-error")
	}
}

// TestTeamDeleteTool_Success_AndUnknown — happy path + missing team.
func TestTeamDeleteTool_Success_AndUnknown(t *testing.T) {
	teams := engine.NewTeamRegistry()
	_, _ = teams.Create("squad", "")
	_ = teams.AddMember("squad", "lead", "agent-1")
	_ = teams.AddMember("squad", "researcher", "agent-2")

	tool := TeamDeleteTool{Teams: teams}
	r1, _ := tool.Call(context.Background(), map[string]any{"team_name": "squad"}, nil)
	if r1.IsError {
		t.Errorf("delete should succeed: %s", flatten(r1))
	}
	if !strings.Contains(flatten(r1), "2 members") {
		t.Errorf("should report member count: %s", flatten(r1))
	}

	r2, _ := tool.Call(context.Background(), map[string]any{"team_name": "ghost"}, nil)
	if !r2.IsError {
		t.Errorf("unknown team should soft-error")
	}
}

// TestSendMessageTool_ByTeamMember — happy path via team+member
// resolution.
func TestSendMessageTool_ByTeamMember(t *testing.T) {
	teams := engine.NewTeamRegistry()
	inbox := engine.NewMessageInbox()
	_, _ = teams.Create("squad", "")
	_ = teams.AddMember("squad", "lead", "agent-7")

	tool := SendMessageTool{Teams: teams, Messages: inbox}
	res, _ := tool.Call(context.Background(), map[string]any{
		"team":    "squad",
		"member":  "lead",
		"message": "review the auth diff",
	}, nil)
	if res.IsError {
		t.Fatalf("send should succeed: %s", flatten(res))
	}
	if inbox.Depth("agent-7") != 1 {
		t.Errorf("inbox should have 1 message; got %d", inbox.Depth("agent-7"))
	}
}

// TestSendMessageTool_ByHandle — addressing by direct handle id
// without going through a team.
func TestSendMessageTool_ByHandle(t *testing.T) {
	inbox := engine.NewMessageInbox()
	tool := SendMessageTool{Messages: inbox}
	res, _ := tool.Call(context.Background(), map[string]any{
		"handle":  "agent-9",
		"message": "ping",
	}, nil)
	if res.IsError {
		t.Errorf("handle-only send should succeed: %s", flatten(res))
	}
	if inbox.Depth("agent-9") != 1 {
		t.Errorf("inbox depth: %d", inbox.Depth("agent-9"))
	}
}

// TestSendMessageTool_FromLabel — `from` field surfaces in dequeued
// PendingMessage so the receiving teammate can see who sent it.
func TestSendMessageTool_FromLabel(t *testing.T) {
	inbox := engine.NewMessageInbox()
	tool := SendMessageTool{Messages: inbox}
	_, _ = tool.Call(context.Background(), map[string]any{
		"handle":  "agent-x",
		"message": "hi",
		"from":    "researcher",
	}, nil)
	msg, ok := inbox.Dequeue("agent-x")
	if !ok || msg.From != "researcher" {
		t.Errorf("from label not preserved: %+v", msg)
	}
}

// TestSendMessageTool_MissingAddress — neither handle nor team+member
// supplied is a soft error.
func TestSendMessageTool_MissingAddress(t *testing.T) {
	tool := SendMessageTool{Messages: engine.NewMessageInbox()}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "no addr",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "either `handle`") {
		t.Errorf("missing addr should soft-error: %s", flatten(res))
	}
}

// TestSendMessageTool_UnknownMember — addressing a team member that
// doesn't exist soft-errors.
func TestSendMessageTool_UnknownMember(t *testing.T) {
	teams := engine.NewTeamRegistry()
	_, _ = teams.Create("squad", "")
	tool := SendMessageTool{Teams: teams, Messages: engine.NewMessageInbox()}
	res, _ := tool.Call(context.Background(), map[string]any{
		"team": "squad", "member": "ghost", "message": "x",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "no member") {
		t.Errorf("unknown member should soft-error: %s", flatten(res))
	}
}

// TestSendMessageTool_EmptyBody — empty message body is a soft error.
func TestSendMessageTool_EmptyBody(t *testing.T) {
	tool := SendMessageTool{Messages: engine.NewMessageInbox()}
	res, _ := tool.Call(context.Background(), map[string]any{
		"handle":  "agent-x",
		"message": "   ",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "body is required") {
		t.Errorf("empty body should soft-error: %s", flatten(res))
	}
}

// TestAgentBackground_RegistersIntoTeam — when team_name + member_name
// are supplied, the spawn registers into TeamRegistry.
func TestAgentBackground_RegistersIntoTeam(t *testing.T) {
	teams := engine.NewTeamRegistry()
	_, _ = teams.Create("squad", "")
	spawner := &stubAsyncSpawner{
		handle: engine.TeammateHandle{ID: "agent-42", AgentType: "explore"},
	}
	tool := AgentBackgroundTool{Teams: teams}
	env := &engine.ToolEnv{Spawner: spawner}
	res, _ := tool.Call(context.Background(), map[string]any{
		"prompt":      "research",
		"team_name":   "squad",
		"member_name": "lead",
	}, env)
	if res.IsError {
		t.Fatalf("registration spawn failed: %s", flatten(res))
	}
	id, ok := teams.ResolveMember("squad", "lead")
	if !ok || id != "agent-42" {
		t.Errorf("team registration didn't stick: ok=%v id=%q", ok, id)
	}
	if !strings.Contains(flatten(res), "Registered as member \"lead\"") {
		t.Errorf("result should mention team registration: %s", flatten(res))
	}
}

// TestAgentBackground_TeamNameWithoutMember — must supply both or neither.
func TestAgentBackground_TeamNameWithoutMember(t *testing.T) {
	teams := engine.NewTeamRegistry()
	_, _ = teams.Create("squad", "")
	tool := AgentBackgroundTool{Teams: teams}
	env := &engine.ToolEnv{Spawner: &stubAsyncSpawner{}}
	res, _ := tool.Call(context.Background(), map[string]any{
		"prompt":    "x",
		"team_name": "squad",
		// missing member_name
	}, env)
	if !res.IsError {
		t.Errorf("team_name without member_name should soft-error")
	}
}
