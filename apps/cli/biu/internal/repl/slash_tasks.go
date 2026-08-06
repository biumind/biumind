// /tasks slash + helpers. Surfaces the bgtask store the engine's
// Bash{run_in_background:true} writes into.

package repl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
)

// handleTasks dispatches /tasks subcommands. Stateless — every call
// resolves against the in-memory bgtask store directly.
func (m model) handleTasks(parts []string) string {
	if m.bgTasks == nil {
		return "/tasks: background tasks aren't enabled in this build"
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	switch sub {
	case "", "list":
		return m.renderTaskList()
	case "output":
		if len(parts) < 3 {
			return "/tasks: usage: /tasks output <id> [since-line]"
		}
		since := 0
		if len(parts) >= 4 {
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				return "/tasks: since-line must be an integer; got " + parts[3]
			}
			since = n
		}
		return m.renderTaskOutput(parts[2], since)
	case "kill":
		if len(parts) < 3 {
			return "/tasks: usage: /tasks kill <id>"
		}
		snap, err := m.bgTasks.Stop(parts[2])
		if err != nil {
			return "/tasks: " + err.Error()
		}
		return fmt.Sprintf("/tasks: %s → %s (exit=%d)",
			snap.ID, snap.Status, snap.ExitCode)
	case "killall":
		n := m.bgTasks.StopAll()
		return fmt.Sprintf("/tasks: killed %d running task(s)", n)
	default:
		return "/tasks: usage: /tasks [list|output <id> [n]|kill <id>|killall]"
	}
}

// renderTaskList renders the table the user sees on bare `/tasks`.
// Format: id status runtime command (truncated). Status drives a
// small symbol so running tasks stand out.
func (m model) renderTaskList() string {
	rows := m.bgTasks.List()
	if len(rows) == 0 {
		return "/tasks: no background tasks. Spawn one with " +
			"`Bash{run_in_background:true}` or have the model do it."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "background tasks (%d):\n", len(rows))
	for _, r := range rows {
		runtime := time.Since(r.StartedAt).Round(time.Second)
		if !r.EndedAt.IsZero() {
			runtime = r.EndedAt.Sub(r.StartedAt).Round(time.Second)
		}
		cmd := r.Command
		if len(cmd) > 60 {
			cmd = cmd[:57] + "…"
		}
		symbol := taskStatusSymbol(r.Status)
		fmt.Fprintf(&b, "  %s %-6s %s  %-7s  %s\n",
			symbol, r.ID, runtime, r.Status, cmd)
	}
	b.WriteString("  (output: /tasks output <id>; kill: /tasks kill <id>)")
	return b.String()
}

// renderTaskOutput pretty-prints captured output for one task. Caps
// at the last ~50 lines to keep the system note readable; the model
// uses BashOutput for full programmatic access.
func (m model) renderTaskOutput(id string, since int) string {
	lines, next, status, dropped, ok := m.bgTasks.Output(id, since)
	if !ok {
		return "/tasks: no such task " + id
	}
	const tailCap = 50
	clipped := lines
	if len(clipped) > tailCap {
		clipped = clipped[len(clipped)-tailCap:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "task: %s  status: %s  next-line: %d", id, status, next)
	if dropped > 0 {
		fmt.Fprintf(&b, "  (dropped %d oldest)", dropped)
	}
	b.WriteByte('\n')
	if len(clipped) == 0 {
		b.WriteString("(no output yet)")
		return b.String()
	}
	if len(lines) > tailCap {
		fmt.Fprintf(&b, "(showing last %d of %d new lines)\n", tailCap, len(lines))
	}
	b.WriteString(strings.Join(clipped, "\n"))
	return b.String()
}

// taskStatusSymbol returns a single-char glyph for the status line:
// running gets a spinner-ish dot, terminal states get a check / cross.
func taskStatusSymbol(s bgtask.Status) string {
	switch s {
	case bgtask.StatusRunning:
		return "⏵"
	case bgtask.StatusDone:
		return "✓"
	case bgtask.StatusFailed:
		return "✗"
	case bgtask.StatusKilled:
		return "□"
	default:
		return "·"
	}
}
