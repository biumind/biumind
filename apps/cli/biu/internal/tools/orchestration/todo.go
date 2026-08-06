// TodoWrite tool — manages the in-session todo checklist.
//
// The model uses this to keep itself honest on multi-step work; the
// UI renders the list as a side panel.
//
// Wire shape:
//
//   { "todos": [
//     {"id": "1", "content": "...", "status": "in_progress", "activeForm": "..."},
//     {"id": "2", "content": "...", "status": "pending"}
//   ]}
//
// Storage: AppState.Todos[agentID] (empty agentID = main session).
// When every item is completed the list is cleared, to match the
// model's expectation that "all done = clean slate".

package orchestration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// TodoWriteTool implements engine.Tool. The empty struct is fine —
// all state lives on AppState through ToolEnv.
type TodoWriteTool struct{}

func (TodoWriteTool) Name() string { return "TodoWrite" }

func (TodoWriteTool) Description(_ map[string]any) string {
	return "Maintain the in-session task checklist. Pass the FULL list each call; " +
		"the engine replaces the previous list. Status: pending | in_progress | completed."
}

func (TodoWriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "The complete updated todo list",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"content":    map[string]any{"type": "string"},
						"activeForm": map[string]any{"type": "string"},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
		"required": []string{"todos"},
	}
}

func (TodoWriteTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TodoWriteTool) IsDestructive(_ map[string]any) bool     { return false }
func (TodoWriteTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (TodoWriteTool) InterruptBehavior() string               { return "cancel" }

func (TodoWriteTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	raw, ok := input["todos"]
	if !ok {
		return softErr("TodoWrite", "missing required field: todos"), nil
	}
	// Re-serialize through JSON to coerce the loose map[string]any
	// into the strongly-typed slice. Cheap and tolerant of int vs
	// string status.
	buf, err := json.Marshal(raw)
	if err != nil {
		return softErr("TodoWrite", fmt.Sprintf("encode todos: %v", err)), nil
	}
	var items []state.TodoItem
	if err := json.Unmarshal(buf, &items); err != nil {
		return softErr("TodoWrite", fmt.Sprintf("decode todos: %v", err)), nil
	}
	if env == nil || env.AppState == nil {
		return softErr("TodoWrite", "no app state in tool env"), nil
	}

	prev := env.AppState.SetTodos(env.AgentID, items)
	allDone := len(items) > 0
	for _, it := range items {
		if it.Status != state.TodoCompleted {
			allDone = false
			break
		}
	}

	msg := "Todos updated. Continue using the list to track progress."
	if allDone {
		msg = "All todos completed; list cleared. Move on to the next phase."
	}
	// Keep the previous list summary in the result so the model can
	// audit what changed.
	summary := fmt.Sprintf("%s (%d items, %d previously)", msg, len(items), len(prev))
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: summary}},
	}, nil
}

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("%s error: %s", name, msg),
		}},
		IsError:   true,
		SoftError: msg,
	}
}
