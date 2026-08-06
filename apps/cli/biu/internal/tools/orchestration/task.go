// Task* tools — CRUD on the structured task list (distinct from the
// transient TodoWrite list).
//
// We implement the *bookkeeping* half of the Task* family here
// (TaskCreate / TaskGet / TaskList / TaskUpdate / TaskOutput /
// TaskStop): AgentTool (the one that actually spawns a sub-agent run)
// is a separate file because it depends on engine sub-agent
// infrastructure that we'll wire later.
//
// All Task* tools are concurrency-safe: they only mutate AppState.Tasks
// under the existing AppState lock.

package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// taskCounter assigns incrementing numeric task IDs ("1", "2", ...) so
// the LLM can refer to them by short label. The short numeric form is
// friendlier in chat than ULIDs. Unique within a process.
var taskCounter int64

func nextTaskID() state.TaskID {
	return state.TaskID(strconv.FormatInt(atomic.AddInt64(&taskCounter, 1), 10))
}

// ─── TaskCreate ────────────────────────────────────────

type TaskCreateTool struct{}

func (TaskCreateTool) Name() string { return "TaskCreate" }
func (TaskCreateTool) Description(_ map[string]any) string {
	return "Add a new task to the task list. Status starts as 'pending'. " +
		"Returns the assigned task ID."
}
func (TaskCreateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject":     map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"activeForm":  map[string]any{"type": "string"},
		},
		"required": []string{"subject", "description"},
	}
}
func (TaskCreateTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TaskCreateTool) IsDestructive(_ map[string]any) bool     { return false }
func (TaskCreateTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (TaskCreateTool) InterruptBehavior() string               { return "cancel" }

func (TaskCreateTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.AppState == nil {
		return softErr("TaskCreate", "no app state"), nil
	}
	subject, _ := input["subject"].(string)
	desc, _ := input["description"].(string)
	if strings.TrimSpace(subject) == "" {
		return softErr("TaskCreate", "subject is required"), nil
	}
	id := nextTaskID()
	env.AppState.PutTask(&state.Task{
		ID: id, Status: "pending", Description: desc,
		Type: "checklist",
	})
	// Fire TaskCreated hook (P20.55) so users can wire
	// "post tasks to Linear" / "Slack-notify lead". Best-effort.
	if env.FireHook != nil {
		env.FireHook("TaskCreated", map[string]any{
			"task_id":     string(id),
			"subject":     subject,
			"description": desc,
		})
	}
	return text(fmt.Sprintf("Task #%s created: %s", id, subject)), nil
}

// ─── TaskList ─────────────────────────────────────────

type TaskListTool struct{}

func (TaskListTool) Name() string { return "TaskList" }
func (TaskListTool) Description(_ map[string]any) string {
	return "Return every task in the task list with its status, owner, " +
		"and dependency information."
}
func (TaskListTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object", "properties": map[string]any{},
	}
}
func (TaskListTool) IsReadOnly(_ map[string]any) bool        { return true }
func (TaskListTool) IsDestructive(_ map[string]any) bool     { return false }
func (TaskListTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (TaskListTool) InterruptBehavior() string               { return "cancel" }

func (TaskListTool) Call(_ context.Context, _ map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.AppState == nil {
		return softErr("TaskList", "no app state"), nil
	}
	tasks := env.AppState.TasksSnapshot()
	if len(tasks) == 0 {
		return text("(no tasks)"), nil
	}
	// Compact summary; full detail goes through TaskGet.
	summary := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		summary = append(summary, map[string]any{
			"id":          string(t.ID),
			"status":      t.Status,
			"description": t.Description,
			"blockedBy":   t.BlockedBy,
		})
	}
	buf, _ := json.MarshalIndent(summary, "", "  ")
	return text(string(buf)), nil
}

// ─── TaskGet ──────────────────────────────────────────

type TaskGetTool struct{}

func (TaskGetTool) Name() string { return "TaskGet" }
func (TaskGetTool) Description(_ map[string]any) string {
	return "Return full details for a single task by ID."
}
func (TaskGetTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"taskId": map[string]any{"type": "string"},
		},
		"required": []string{"taskId"},
	}
}
func (TaskGetTool) IsReadOnly(_ map[string]any) bool        { return true }
func (TaskGetTool) IsDestructive(_ map[string]any) bool     { return false }
func (TaskGetTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (TaskGetTool) InterruptBehavior() string               { return "cancel" }

func (TaskGetTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.AppState == nil {
		return softErr("TaskGet", "no app state"), nil
	}
	id, _ := input["taskId"].(string)
	for _, t := range env.AppState.TasksSnapshot() {
		if string(t.ID) == id {
			buf, _ := json.MarshalIndent(t, "", "  ")
			return text(string(buf)), nil
		}
	}
	return softErr("TaskGet", "no task with id "+id), nil
}

// ─── TaskUpdate ───────────────────────────────────────

type TaskUpdateTool struct{}

func (TaskUpdateTool) Name() string { return "TaskUpdate" }
func (TaskUpdateTool) Description(_ map[string]any) string {
	return "Mutate a task: change status (pending | in_progress | completed | deleted), " +
		"description, or dependency edges. Set status to 'deleted' to remove."
}
func (TaskUpdateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"taskId":       map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"addBlocks":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"addBlockedBy": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"taskId"},
	}
}
func (TaskUpdateTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TaskUpdateTool) IsDestructive(_ map[string]any) bool     { return false }
func (TaskUpdateTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (TaskUpdateTool) InterruptBehavior() string               { return "cancel" }

func (TaskUpdateTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.AppState == nil {
		return softErr("TaskUpdate", "no app state"), nil
	}
	id, _ := input["taskId"].(string)
	if id == "" {
		return softErr("TaskUpdate", "taskId required"), nil
	}
	status, hasStatus := input["status"].(string)
	desc, hasDesc := input["description"].(string)
	addBlocks := stringSlice(input["addBlocks"])
	addBlockedBy := stringSlice(input["addBlockedBy"])

	if status == "deleted" {
		// Soft remove: PutTask doesn't delete, so we model deletion as
		// status change so referential integrity stays intact.
		ok := env.AppState.UpdateTask(state.TaskID(id), func(t *state.Task) {
			t.Status = "deleted"
		})
		if !ok {
			return softErr("TaskUpdate", "no task with id "+id), nil
		}
		return text("Task " + id + " marked deleted."), nil
	}

	var prevStatus string
	ok := env.AppState.UpdateTask(state.TaskID(id), func(t *state.Task) {
		prevStatus = t.Status
		if hasStatus {
			t.Status = status
		}
		if hasDesc {
			t.Description = desc
		}
		for _, b := range addBlocks {
			t.Blocks = append(t.Blocks, state.TaskID(b))
		}
		for _, b := range addBlockedBy {
			t.BlockedBy = append(t.BlockedBy, state.TaskID(b))
		}
	})
	if !ok {
		return softErr("TaskUpdate", "no task with id "+id), nil
	}
	// Fire TaskCompleted hook (P20.55) on the pending/in_progress →
	// completed transition. Skip the no-op case where the task was
	// already completed (defensive against duplicate updates).
	if hasStatus && status == "completed" && prevStatus != "completed" &&
		env.FireHook != nil {
		env.FireHook("TaskCompleted", map[string]any{
			"task_id":       id,
			"prev_status":   prevStatus,
			"new_status":    status,
		})
	}
	return text("Task " + id + " updated."), nil
}

// ─── helpers ──────────────────────────────────────────

func text(s string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: s}},
	}
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
