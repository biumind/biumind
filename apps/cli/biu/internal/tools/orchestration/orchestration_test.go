package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func newEnv(t *testing.T) *engine.ToolEnv {
	t.Helper()
	return &engine.ToolEnv{AppState: state.New()}
}

func TestTodoWriteRoundTrip(t *testing.T) {
	env := newEnv(t)
	out, err := TodoWriteTool{}.Call(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "do thing", "status": "in_progress"},
			map[string]any{"id": "2", "content": "next thing", "status": "pending"},
		},
	}, env)
	if err != nil || out.IsError {
		t.Fatalf("write 1: %+v %v", out, err)
	}
	if got := env.AppState.TodosFor(""); len(got) != 2 || got[0].Status != state.TodoInProgress {
		t.Errorf("stored todos wrong: %+v", got)
	}

	// All-completed call clears the list.
	_, _ = TodoWriteTool{}.Call(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "do thing", "status": "completed"},
			map[string]any{"id": "2", "content": "next thing", "status": "completed"},
		},
	}, env)
	if got := env.AppState.TodosFor(""); len(got) != 0 {
		t.Errorf("all-completed should clear; still: %+v", got)
	}
}

func TestTodoWriteRejectsBadInput(t *testing.T) {
	env := newEnv(t)
	out, _ := TodoWriteTool{}.Call(context.Background(), map[string]any{}, env)
	if !out.IsError {
		t.Errorf("missing todos should soft-error")
	}
}

func TestTaskCRUDFlow(t *testing.T) {
	env := newEnv(t)

	out, _ := TaskCreateTool{}.Call(context.Background(), map[string]any{
		"subject":     "Wire auth",
		"description": "Implement OAuth flow end-to-end",
	}, env)
	if out.IsError {
		t.Fatalf("create failed: %+v", out)
	}
	created := flatten(out)
	if !strings.Contains(created, "Task #") {
		t.Fatalf("create response missing id: %q", created)
	}

	// List — description is the user-visible body (subject lives in
	// the create response only, not stored on Task today).
	listOut, _ := TaskListTool{}.Call(context.Background(), nil, env)
	if listOut.IsError || !strings.Contains(flatten(listOut), "OAuth") {
		t.Errorf("list missing task body: %s", flatten(listOut))
	}

	// Update status
	id := env.AppState.TasksSnapshot()[0].ID
	updOut, _ := TaskUpdateTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id), "status": "in_progress",
	}, env)
	if updOut.IsError {
		t.Fatalf("update: %+v", updOut)
	}
	if env.AppState.TasksSnapshot()[0].Status != "in_progress" {
		t.Errorf("status not updated")
	}

	// Get returns full json
	getOut, _ := TaskGetTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id),
	}, env)
	if getOut.IsError || !strings.Contains(flatten(getOut), "OAuth") {
		t.Errorf("get response missing description: %s", flatten(getOut))
	}

	// Delete (status=deleted)
	delOut, _ := TaskUpdateTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id), "status": "deleted",
	}, env)
	if delOut.IsError {
		t.Fatalf("delete: %+v", delOut)
	}
	if env.AppState.TasksSnapshot()[0].Status != "deleted" {
		t.Errorf("delete didn't take")
	}
}

func TestAgentToolRequiresSpawner(t *testing.T) {
	env := newEnv(t) // no spawner
	out, _ := AgentTool{}.Call(context.Background(), map[string]any{
		"prompt": "do something",
	}, env)
	if !out.IsError || !strings.Contains(out.SoftError, "spawner") {
		t.Errorf("expected no-spawner soft-error; got %+v", out)
	}
}

// fakeSpawner runs no real engine — it just echoes the prompt back so
// we can exercise the AgentTool wiring.
type fakeSpawner struct {
	gotReq engine.AgentSpawnRequest
}

func (f *fakeSpawner) Spawn(_ context.Context, req engine.AgentSpawnRequest) (*engine.AgentSpawnResult, error) {
	f.gotReq = req
	return &engine.AgentSpawnResult{
		Output:     "FOUND: " + req.Prompt,
		StopReason: "end_turn",
	}, nil
}

func TestAgentToolWiresSpawner(t *testing.T) {
	env := newEnv(t)
	sp := &fakeSpawner{}
	env.Spawner = sp
	out, _ := AgentTool{}.Call(context.Background(), map[string]any{
		"subagent_type": "Explore",
		"description":   "find auth",
		"prompt":        "search for jwt",
	}, env)
	if out.IsError {
		t.Fatalf("unexpected error: %+v", out)
	}
	if !strings.Contains(flatten(out), "FOUND: search for jwt") {
		t.Errorf("output missing: %s", flatten(out))
	}
	if !strings.Contains(flatten(out), "[Explore]") {
		t.Errorf("agent type not tagged: %s", flatten(out))
	}
	if sp.gotReq.AgentType != "Explore" || sp.gotReq.Description != "find auth" {
		t.Errorf("spawn request fields lost: %+v", sp.gotReq)
	}
}

func flatten(p *engine.ToolResultPayload) string {
	out := ""
	for _, b := range p.Content {
		out += b.Text
	}
	return out
}
