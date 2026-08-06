package orchestration

import (
	"context"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// TestTaskCreate_FiresTaskCreatedHook — TaskCreate.Call writes
// payload through env.FireHook with event="TaskCreated". The runner
// is what wires FireHook to the hook registry; we test the tool half
// here in isolation.
func TestTaskCreate_FiresTaskCreatedHook(t *testing.T) {
	var firedEvent string
	var firedPayload map[string]any
	env := &engine.ToolEnv{
		AppState: state.New(),
		FireHook: func(event string, payload map[string]any) {
			firedEvent = event
			firedPayload = payload
		},
	}
	out, err := TaskCreateTool{}.Call(context.Background(), map[string]any{
		"subject":     "hook-fire test",
		"description": "verify TaskCreated emits",
	}, env)
	if err != nil || out.IsError {
		t.Fatalf("create: %v %+v", err, out)
	}
	if firedEvent != "TaskCreated" {
		t.Errorf("event = %q, want TaskCreated", firedEvent)
	}
	if firedPayload["subject"] != "hook-fire test" {
		t.Errorf("payload subject lost: %+v", firedPayload)
	}
}

// TestTaskUpdate_FiresTaskCompletedOnTransition — only fires on the
// pending/in_progress → completed transition, not on a status update
// that's already completed.
func TestTaskUpdate_FiresTaskCompletedOnTransition(t *testing.T) {
	calls := 0
	env := &engine.ToolEnv{
		AppState: state.New(),
		FireHook: func(event string, _ map[string]any) {
			if event == "TaskCompleted" {
				calls++
			}
		},
	}
	// Seed a task.
	out, _ := TaskCreateTool{}.Call(context.Background(), map[string]any{
		"subject": "x", "description": "y",
	}, env)
	if out.IsError {
		t.Fatalf("seed failed: %s", flatten(out))
	}
	id := env.AppState.TasksSnapshot()[0].ID

	// First update to completed → fires.
	_, _ = TaskUpdateTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id), "status": "completed",
	}, env)
	if calls != 1 {
		t.Errorf("first transition should fire once; calls=%d", calls)
	}

	// Second update with completed → no-op transition, no second fire.
	_, _ = TaskUpdateTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id), "status": "completed",
	}, env)
	if calls != 1 {
		t.Errorf("redundant completed update should not re-fire; calls=%d", calls)
	}
}

// TestTaskUpdate_NoCompletedHookOnNonCompletion — status changes
// other than → completed should not fire TaskCompleted.
func TestTaskUpdate_NoCompletedHookOnNonCompletion(t *testing.T) {
	calls := 0
	env := &engine.ToolEnv{
		AppState: state.New(),
		FireHook: func(event string, _ map[string]any) {
			if event == "TaskCompleted" {
				calls++
			}
		},
	}
	out, _ := TaskCreateTool{}.Call(context.Background(), map[string]any{
		"subject": "x", "description": "y",
	}, env)
	if out.IsError {
		t.Fatalf("seed: %s", flatten(out))
	}
	id := env.AppState.TasksSnapshot()[0].ID
	_, _ = TaskUpdateTool{}.Call(context.Background(), map[string]any{
		"taskId": string(id), "status": "in_progress",
	}, env)
	if calls != 0 {
		t.Errorf("in_progress update should NOT fire TaskCompleted; calls=%d", calls)
	}
}
