// BashOutput + KillBash — partner tools for Bash{run_in_background:true}.
//
// Contract:
//   * BashOutput polls a background task's captured stdout/stderr.
//     Supports delta polling (`since_line` lets the model fetch only
//     new content) so a long log tail doesn't blow context.
//   * KillBash sends SIGTERM, escalates to SIGKILL after a grace
//     window. Returns the final task snapshot.
//
// Both tools surface the bgtask.Store via env-supplied dependency
// injection (BgTasks field on the tool struct, set by Register).
// Without a store the tools soft-error rather than panic.

package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── BashOutput ──────────────────────────────────────────

type BashOutputTool struct {
	BgTasks *bgtask.Store
}

func (BashOutputTool) Name() string { return "BashOutput" }

func (BashOutputTool) Description(_ map[string]any) string {
	return "Poll captured stdout/stderr from a background Bash task. " +
		"Pass `task_id` (from a previous Bash{run_in_background:true}) and " +
		"optionally `since_line` to fetch only new content beyond your last " +
		"poll. Returns lines + a `next_line` cursor + the task's current " +
		"status. When status is `done` / `failed` / `killed` you don't need " +
		"to poll again."
}

func (BashOutputTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID returned by Bash{run_in_background:true}.",
			},
			"since_line": map[string]any{
				"type":        "integer",
				"description": "0-based line cursor. Pass back `next_line` from your previous BashOutput call to fetch only new content. 0 (default) returns the full buffer.",
			},
		},
		"required": []string{"task_id"},
	}
}

func (BashOutputTool) IsReadOnly(_ map[string]any) bool        { return true }
func (BashOutputTool) IsDestructive(_ map[string]any) bool     { return false }
func (BashOutputTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (BashOutputTool) InterruptBehavior() string               { return "cancel" }

func (b BashOutputTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if b.BgTasks == nil {
		return softErr("BashOutput", "background-task store not wired in this build"), nil
	}
	id, _ := input["task_id"].(string)
	if strings.TrimSpace(id) == "" {
		return softErr("BashOutput", "task_id is required"), nil
	}
	since := 0
	// JSON numbers come through as float64 in Go's interface{} —
	// also accept ints for clients that pre-parsed.
	if v, ok := input["since_line"].(float64); ok {
		since = int(v)
	}
	if v, ok := input["since_line"].(int); ok {
		since = v
	}

	lines, next, status, dropped, ok := b.BgTasks.Output(id, since)
	if !ok {
		return softErr("BashOutput", fmt.Sprintf("no task %q (was it spawned in this session?)", id)), nil
	}

	// Build a structured payload the model can parse without regex.
	// Fields are ordered: status / dropped / next_line / cursor /
	// content. Cursor is repeated on its own line so the model sees
	// it before it has to scan past the content body.
	var b2 strings.Builder
	fmt.Fprintf(&b2, "task: %s\nstatus: %s\nsince_line: %d\nnext_line: %d\nlines_returned: %d\n",
		id, status, since, next, len(lines))
	if dropped > 0 {
		fmt.Fprintf(&b2, "dropped: %d  (oldest %d lines fell off the buffer cap; sinceLine values lower than this are stale)\n",
			dropped, dropped)
	}
	if len(lines) == 0 {
		b2.WriteString("\n(no new output)\n")
	} else {
		b2.WriteString("\noutput:\n")
		for _, l := range lines {
			b2.WriteString(l)
			b2.WriteByte('\n')
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b2.String()}},
	}, nil
}

// ─── KillBash ────────────────────────────────────────────

type KillBashTool struct {
	BgTasks *bgtask.Store
}

func (KillBashTool) Name() string { return "KillBash" }

func (KillBashTool) Description(_ map[string]any) string {
	return "Terminate a background Bash task. Sends SIGTERM, escalates " +
		"to SIGKILL if the process doesn't exit within a short grace " +
		"window. Idempotent: calling on an already-terminated task " +
		"returns its final status without error."
}

func (KillBashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID returned by Bash{run_in_background:true}.",
			},
		},
		"required": []string{"task_id"},
	}
}

// KillBash is destructive in the conventional sense (it stops a
// process), but the runner's permission gate already governs every
// Bash spawn — adding a second permission gate per kill would just
// nag without adding security. Mark as destructive so the gate
// surfaces a confirmation in default mode.
func (KillBashTool) IsReadOnly(_ map[string]any) bool        { return false }
func (KillBashTool) IsDestructive(_ map[string]any) bool     { return true }
func (KillBashTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (KillBashTool) InterruptBehavior() string               { return "cancel" }

func (k KillBashTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if k.BgTasks == nil {
		return softErr("KillBash", "background-task store not wired in this build"), nil
	}
	id, _ := input["task_id"].(string)
	if strings.TrimSpace(id) == "" {
		return softErr("KillBash", "task_id is required"), nil
	}
	snap, err := k.BgTasks.Stop(id)
	if err != nil {
		return softErr("KillBash", err.Error()), nil
	}
	body := fmt.Sprintf("task: %s\nstatus: %s\nexit_code: %d\nlines_captured: %d\n",
		snap.ID, snap.Status, snap.ExitCode, snap.TotalLines)
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
	}, nil
}
