// TaskOutput + TaskStop — unified background-task control surface.
//
// Why these exist alongside BashOutput / KillBash:
//
//   * The modern tool surface migrated from the bash-specific
//     BashOutput / KillShell pair to the type-agnostic TaskOutput /
//     TaskStop. The old names live on as aliases so existing
//     transcripts replay cleanly. The unified shape lets models
//     trained against either prompt set work here.
//   * TaskOutput adds blocking semantics — `block: true` + a timeout
//     lets the model say "wait for this to finish, then return"
//     rather than spinning a poll loop in the agent loop. Cheaper
//     than chaining N BashOutput calls when the model already knows
//     it wants the final answer.
//   * TaskStop accepts the deprecated `shell_id` parameter so prompts
//     authored against KillShell don't break. Mapped to task_id
//     internally; either field is acceptable.
//
// Today biu only has one task type (local_bash); the unified tools
// still emit `task_type` in the result so when local_agent / remote
// task types arrive (P20.x roadmap) the wire shape doesn't break.

package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// taskOutputPollInterval bounds how aggressively TaskOutput polls
// the store while blocking. 200ms balances "feels live" against
// burning CPU when many TaskOutput calls fire in parallel. The wall
// clock budget is governed by the timeout parameter, not this rate.
const taskOutputPollInterval = 200 * time.Millisecond

// taskOutputDefaultTimeoutMs is what `block: true` waits when the
// caller doesn't pass a timeout. 30s keeps blocking calls short.
const taskOutputDefaultTimeoutMs = 30000

// taskOutputMaxTimeoutMs caps the upper-bound a single TaskOutput
// call can hold the agent loop hostage. 10 minutes is the cap; the
// schema clamps anything higher to keep one call from sequestering
// the conversation indefinitely.
const taskOutputMaxTimeoutMs = 600000

// ─── TaskOutput ─────────────────────────────────────────

// TaskOutputTool reports a background task's current output and
// status, optionally blocking until the task leaves the running
// state. Type-agnostic shape so future task types (local_agent /
// remote_agent) plug in without a tool-rename.
type TaskOutputTool struct {
	BgTasks *bgtask.Store
}

func (TaskOutputTool) Name() string { return "TaskOutput" }

func (TaskOutputTool) Description(_ map[string]any) string {
	return "Read output from a running or completed background task. " +
		"Pass `task_id` (returned by Bash{run_in_background:true} or a " +
		"future async-agent tool). Set `block: true` (the default) to wait " +
		"until the task exits, up to `timeout` ms (default 30s, max 10min). " +
		"Set `block: false` for a non-blocking snapshot of the current state."
}

func (TaskOutputTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Task ID returned by a previous spawn call (e.g. Bash{run_in_background:true}).",
			},
			"block": map[string]any{
				"type":        "boolean",
				"description": "Whether to wait until the task leaves the running state. Default true.",
				"default":     true,
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Max wait time in milliseconds when block=true. Default 30000, max 600000.",
				"default":     taskOutputDefaultTimeoutMs,
				"minimum":     0,
				"maximum":     taskOutputMaxTimeoutMs,
			},
		},
		"required": []string{"task_id"},
	}
}

func (TaskOutputTool) IsReadOnly(_ map[string]any) bool        { return true }
func (TaskOutputTool) IsDestructive(_ map[string]any) bool     { return false }
func (TaskOutputTool) IsConcurrencySafe(_ map[string]any) bool { return true }

// InterruptBehavior=cancel — when the user hits Esc the tool
// abandons the wait without trying to stop the task itself. The
// task keeps running; a follow-up TaskStop is the explicit kill
// path.
func (TaskOutputTool) InterruptBehavior() string { return "cancel" }

func (t TaskOutputTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if t.BgTasks == nil {
		return softErr("TaskOutput", "background-task store not wired in this build"), nil
	}
	id, _ := input["task_id"].(string)
	if strings.TrimSpace(id) == "" {
		return softErr("TaskOutput", "task_id is required"), nil
	}

	block := true // default per the tool's blocking contract
	if v, ok := input["block"].(bool); ok {
		block = v
	}

	timeoutMs := taskOutputDefaultTimeoutMs
	if v, ok := input["timeout"].(float64); ok {
		timeoutMs = int(v)
	}
	if v, ok := input["timeout"].(int); ok {
		timeoutMs = v
	}
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	if timeoutMs > taskOutputMaxTimeoutMs {
		// Clamp rather than reject — schema's `maximum` is advisory
		// for many providers, and a clamp lets the call still
		// succeed with a sensible bound.
		timeoutMs = taskOutputMaxTimeoutMs
	}

	retrieval := "success"
	var lines []string
	var status bgtask.Status
	var dropped int

	if block {
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for {
			var ok bool
			lines, _, status, dropped, ok = t.BgTasks.Output(id, 0)
			if !ok {
				return softErr("TaskOutput", fmt.Sprintf("no task %q", id)), nil
			}
			if status != bgtask.StatusRunning {
				break
			}
			// Time-based exit + ctx-aware sleep so Esc cancels
			// promptly and we don't overshoot the deadline.
			now := time.Now()
			if !now.Before(deadline) {
				retrieval = "timeout"
				break
			}
			remaining := deadline.Sub(now)
			wait := taskOutputPollInterval
			if wait > remaining {
				wait = remaining
			}
			select {
			case <-ctx.Done():
				retrieval = "timeout"
				return formatTaskOutput(id, status, lines, dropped, retrieval, t.BgTasks), nil
			case <-time.After(wait):
			}
		}
	} else {
		var ok bool
		lines, _, status, dropped, ok = t.BgTasks.Output(id, 0)
		if !ok {
			return softErr("TaskOutput", fmt.Sprintf("no task %q", id)), nil
		}
		if status == bgtask.StatusRunning {
			retrieval = "not_ready"
		}
	}
	return formatTaskOutput(id, status, lines, dropped, retrieval, t.BgTasks), nil
}

// formatTaskOutput renders the unified output payload. Includes the
// task snapshot fields (command, exit_code) when available so the
// model sees enough context without a follow-up call.
func formatTaskOutput(id string, status bgtask.Status, lines []string, dropped int, retrieval string, store *bgtask.Store) *engine.ToolResultPayload {
	var b strings.Builder
	fmt.Fprintf(&b, "task: %s\ntask_type: local_bash\nstatus: %s\nretrieval_status: %s\n",
		id, status, retrieval)

	// Snapshot lookup is cheap and gives us exit_code + command.
	if t, ok := store.Get(id); ok {
		snap := t.Snapshot()
		fmt.Fprintf(&b, "command: %s\nexit_code: %d\n", snap.Command, snap.ExitCode)
	}
	fmt.Fprintf(&b, "lines_returned: %d\n", len(lines))
	if dropped > 0 {
		fmt.Fprintf(&b, "dropped: %d  (oldest %d lines fell off the buffer cap)\n", dropped, dropped)
	}
	if len(lines) == 0 {
		b.WriteString("\n(no output)\n")
	} else {
		b.WriteString("\noutput:\n")
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}
}

// ─── TaskStop ───────────────────────────────────────────

// TaskStopTool stops a running background task by ID. Accepts both
// `task_id` (preferred) and the deprecated `shell_id` (compat with
// the older KillShell tool name).
type TaskStopTool struct {
	BgTasks *bgtask.Store
}

func (TaskStopTool) Name() string { return "TaskStop" }

func (TaskStopTool) Description(_ map[string]any) string {
	return "Stop a running background task by ID. Sends SIGTERM, " +
		"escalates to SIGKILL after a short grace window. Idempotent: " +
		"calling on an already-terminated task returns its final status. " +
		"`shell_id` is accepted as an alias for backward compatibility " +
		"with prompts written against the deprecated KillShell tool."
}

func (TaskStopTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the background task to stop.",
			},
			"shell_id": map[string]any{
				"type":        "string",
				"description": "Deprecated alias for task_id (KillShell compat).",
			},
		},
	}
}

// TaskStop is destructive (terminates a process) so the permission
// gate surfaces a confirmation in default mode.
func (TaskStopTool) IsReadOnly(_ map[string]any) bool        { return false }
func (TaskStopTool) IsDestructive(_ map[string]any) bool     { return true }
func (TaskStopTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (TaskStopTool) InterruptBehavior() string               { return "cancel" }

func (t TaskStopTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if t.BgTasks == nil {
		return softErr("TaskStop", "background-task store not wired in this build"), nil
	}
	id, _ := input["task_id"].(string)
	if id == "" {
		// Backward-compat: accept the legacy shell_id parameter.
		id, _ = input["shell_id"].(string)
	}
	if strings.TrimSpace(id) == "" {
		return softErr("TaskStop", "task_id is required"), nil
	}
	snap, err := t.BgTasks.Stop(id)
	if err != nil {
		return softErr("TaskStop", err.Error()), nil
	}
	body := fmt.Sprintf(
		"task: %s\ntask_type: local_bash\nstatus: %s\nexit_code: %d\nlines_captured: %d\ncommand: %s\nmessage: stopped task %s\n",
		snap.ID, snap.Status, snap.ExitCode, snap.TotalLines, snap.Command, snap.ID)
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
	}, nil
}
