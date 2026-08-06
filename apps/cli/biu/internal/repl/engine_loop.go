// engine_loop.go — bridges QueryEngine events into bubbletea messages.
//
// When repl.Options.Engine is non-nil, the REPL routes user prompts
// through engine.Submit() instead of the legacy provider.ChatStream
// path. The two paths coexist because:
//
//   1. The engine path is the long-term home (tool calls, compact,
//      hooks). Users with an Anthropic API key configured get full
//      agent behaviour today.
//   2. The legacy path remains for model-relay mode until model-relay gains tool
//      forwarding (separate work item).
//
// Tea-side flow:
//
//   model.startEngineStream(prompt)
//     → engine.Submit returns <-chan engine.Event
//     → goroutine drains events into a global pipe channel
//     → waitEngineCmd reads one event from the pipe and returns it
//        as engineEventMsg
//     → Update branches on the underlying engine.Event type and
//        re-arms waitEngineCmd until DoneEvent / ErrorEvent

package repl

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// enginePipe is the bridge from the engine goroutine to the tea event
// loop. One pipe per REPL — bubbletea programs are single-instance so
// this is fine. Buffer is generous to absorb a fast tool burst.
var enginePipe = make(chan tea.Msg, 256)

// waitEngineCmd is the tea.Cmd that pulls the next engine event off
// the pipe. Rearmed by model.Update on every event.
func waitEngineCmd() tea.Msg { return <-enginePipe }

// engineEventMsg wraps a single engine.Event so bubbletea can route
// it. We use a wrapper rather than letting engine.Event be tea.Msg
// directly so the engine package stays free of UI dependencies.
type engineEventMsg struct{ ev engine.Event }

// engineLaunchedMsg signals "the engine goroutine is running, please
// arm the pipe". Returned by the launch Cmd before the first event
// arrives so we don't miss the very first frame.
type engineLaunchedMsg struct{}

// startEngineStream replaces startStream when the model is configured
// with an engine. Returns the launch + first wait Cmd.
func (m model) startEngineStream(prompt string) (tea.Model, tea.Cmd) {
	if m.engine == nil {
		// Defensive — caller should have routed to legacy.
		return m, nil
	}
	m.pending.Reset()
	m.state = stateSending
	m.toolRows = nil
	m.refreshBody()

	parent := context.Background()
	ctx, cancel := context.WithCancel(parent)
	m.streamCancel = cancel

	eng := m.engine

	launchCmd := func() tea.Msg {
		ch := eng.Submit(ctx, prompt)
		go func() {
			for ev := range ch {
				enginePipe <- engineEventMsg{ev: ev}
			}
			// Channel closed — engine done. Push a sentinel so the
			// tea loop knows to stop waiting.
			enginePipe <- engineDoneSentinel{}
		}()
		return engineLaunchedMsg{}
	}
	return m, tea.Batch(launchCmd, waitEngineCmd, m.spinner.Tick)
}

// engineDoneSentinel signals "the engine channel closed — stop
// arming waitEngineCmd". Distinct from engine.DoneEvent (which is the
// normal end-of-turn signal); the sentinel covers cancellation /
// error paths where DoneEvent never fires.
type engineDoneSentinel struct{}

// handleEngineEvent dispatches an engine.Event. Returns updated model
// + Cmd. Caller (Update) re-arms waitEngineCmd unless we hit a
// terminal event.
func (m model) handleEngineEvent(ev engine.Event) (model, bool /*terminal*/) {
	switch e := ev.(type) {
	case *engine.RequestStartEvent:
		m.state = stateSending
		m.refreshBody()

	case *engine.StreamTokenEvent:
		m.pending.WriteString(e.Text)
		m.state = stateStreaming
		m.refreshBody()

	case *engine.StreamUsageEvent:
		m.lastUsageInput = e.InputTokens
		m.lastUsageOutput = e.OutputTokens
		// Cache stats feed the context-window indicator: a turn with
		// large cache_read still consumes the input budget even though
		// it doesn't pay full input price.
		m.lastUsageCacheRead = e.CacheReadTokens
		m.lastUsageCacheCreate = e.CacheCreateTokens
		m.refreshBody()

	case *engine.AssistantMessageEvent:
		// Persist the final assistant text into history so the user
		// can scroll back past streaming flicker.
		text := strings.TrimRight(m.pending.String(), "\n")
		m.pending.Reset()
		if text != "" {
			m.history = append(m.history,
				client.Message{Role: "assistant", Content: text})
			if m.sessionLog != nil {
				_ = m.sessionLog.Append(session.Event{
					Type: "assistant_message", Content: text,
					Reason: e.StopReason,
				})
			}
		}
		m.lastStopReason = e.StopReason
		m.refreshBody()

	case *engine.ToolUseStartEvent:
		m.toolRows = append(m.toolRows, toolRow{
			ID: e.ID, Name: e.Name, Input: e.Input,
			StartedAt: time.Now(), Phase: "running",
		})
		if m.sessionLog != nil {
			_ = m.sessionLog.Append(session.Event{
				Type: "tool_use", Name: e.Name,
				CallID: e.ID, Args: e.Input,
			})
		}
		m.refreshBody()

	case *engine.ToolUseProgressEvent:
		// Append to the matching toolRow's ring buffer so users see
		// live stdout / hits. Bash emits {kind:"stdout"|"stderr",
		// line:"..."}; Grep emits {kind:"grep_progress",matched:N}.
		// We render the freshest ProgressLineCap entries; older lines
		// scroll off.
		for i := range m.toolRows {
			if m.toolRows[i].ID != e.ID {
				continue
			}
			line := formatProgress(e.Data)
			if line == "" {
				break
			}
			pr := append(m.toolRows[i].ProgressLines, line)
			if len(pr) > ProgressLineCap {
				pr = pr[len(pr)-ProgressLineCap:]
			}
			m.toolRows[i].ProgressLines = pr
			break
		}
		m.refreshBody()

	case *engine.ToolUseResultEvent:
		for i := range m.toolRows {
			if m.toolRows[i].ID == e.ID {
				m.toolRows[i].Phase = "done"
				m.toolRows[i].Elapsed = e.Elapsed
				m.toolRows[i].IsError = e.Result.IsError
				if e.Result.IsError {
					m.toolRows[i].Phase = "error"
					m.toolRows[i].Message = e.Result.SoftError
				}
				// Pull the diff out of the result body when present
				// (Edit/Write/MultiEdit always include it).
				body := ""
				for _, b := range e.Result.Content {
					body += b.Text
				}
				m.toolRows[i].Diff = extractDiff(body)
				break
			}
		}
		if m.sessionLog != nil {
			body := ""
			for _, b := range e.Result.Content {
				body += b.Text
			}
			_ = m.sessionLog.Append(session.Event{
				Type: "tool_result", Name: e.Name,
				CallID: e.ID, Output: body,
			})
		}
		m.refreshBody()

	case *engine.PermissionAskEvent:
		// Park the request in model state; let the user answer via
		// keyboard. Update() handles 'a' / 'd' / 'q' keys when
		// m.permissionAsk is non-nil.
		m.permissionAsk = e
		m.refreshBody()

	case *engine.UserQuestionAskEvent:
		// Same pattern as permissionAsk: park the request, let
		// handleKey route arrow + Enter into Decision.
		m.questionAsk = e
		m.questionCursor = 0
		m.refreshBody()

	case *engine.CompactStartEvent:
		// Compact lands in Phase D; for now just flag the state.
		m.state = stateSending
		m.refreshBody()

	case *engine.CompactDoneEvent:
		m.refreshBody()

	case *engine.ErrorEvent:
		m.lastErr = e.Err
		m.state = stateError
		m.refreshBody()
		return m, true

	case *engine.DoneEvent:
		m.state = stateIdle
		m.lastStopReason = e.StopReason
		m.turnsCount++
		m.refreshBody()
		return m, true
	}
	return m, false
}

// renderToolRows builds the cluster of ⏺ rows shown above the in-flight
// assistant text. Mirrors Claude Code's rendering style:
//
//   ⏺ Read foo.go
//   ⏺ Edit foo.go (5 changes)               1.2s
//   ⏺ Bash:rm -rf /tmp/x                    ✗ denied by user
func renderToolRows(rows []toolRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(styleToolBullet.Render("⏺ "))
		b.WriteString(r.Name)
		// Compact arg summary — first arg's value if present.
		if summary := summariseInput(r.Input); summary != "" {
			b.WriteString(" ")
			b.WriteString(styleToolDesc.Render(summary))
		}
		switch r.Phase {
		case "running":
			b.WriteString(styleToolDesc.Render("  …"))
		case "done":
			if r.Elapsed > 0 {
				b.WriteString(styleToolDesc.Render(
					fmt.Sprintf("  %s", r.Elapsed.Round(time.Millisecond))))
			}
		case "error":
			b.WriteString("  ")
			msg := r.Message
			if msg == "" {
				msg = "error"
			}
			b.WriteString(styleErrorPrefix.Render("✗ " + msg))
		}
		b.WriteString("\n")
		// Stream the latest progress lines under a running row so
		// users see live Bash stdout / Grep hits without expanding
		// anything. The buffer is capped at ProgressLineCap so a
		// noisy `find /` won't blow out the viewport.
		if r.Phase == "running" {
			for _, line := range r.ProgressLines {
				b.WriteString(styleToolDesc.Render(line))
				b.WriteByte('\n')
			}
		}
		if r.Diff != "" {
			b.WriteString(renderDiff(r.Diff))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// extractDiff pulls the unified-diff block out of a tool_result body.
// Edit / Write / MultiEdit always emit "<message>\n\n<unified diff>"
// when there's a real change. Returns the empty string when no diff
// is present.
func extractDiff(body string) string {
	idx := strings.Index(body, "--- a/")
	if idx == -1 {
		return ""
	}
	return body[idx:]
}

// renderDiff colorises a unified-diff block for the TUI viewport. We
// keep this a 4-line lookup table rather than pulling a syntax
// highlighter — diff is too simple to warrant the dependency.
func renderDiff(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(styleDiffMeta.Render(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(styleDiffHunk.Render(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(styleDiffAdd.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(styleDiffDel.Render(line))
		default:
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// summariseInput pulls the most "interesting" arg value for a one-line
// preview. Falls back to the JSON-encoded args when no obvious
// candidate exists.
func summariseInput(input map[string]any) string {
	for _, k := range []string{"path", "command", "pattern", "url", "query"} {
		if v, ok := input[k]; ok {
			return ellipsize(fmt.Sprintf("%v", v), 80)
		}
	}
	if len(input) == 0 {
		return ""
	}
	// Pick any single field deterministically.
	for k, v := range input {
		return ellipsize(fmt.Sprintf("%s=%v", k, v), 80)
	}
	return ""
}

func ellipsize(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// renderPermissionAsk produces the inline confirmation prompt block.
// Shown right above the input box when m.permissionAsk is non-nil.
//
// When the runner attached suggestions (e.g. an "Allow + add dir to
// working dirs" shortcut), each gets its own line above the standard
// hotkey hint, rendered inline rather than as a separate dialog.
func renderPermissionAsk(ask *engine.PermissionAskEvent) string {
	if ask == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleErrorPrefix.Render("⚠ Permission required"))
	b.WriteString("\n  ")
	b.WriteString(ask.ToolName)
	if s := summariseInput(ask.Input); s != "" {
		b.WriteString("  ")
		b.WriteString(styleToolDesc.Render(s))
	}
	b.WriteString("\n  ")
	b.WriteString(styleHint.Render(ask.Reason))
	for _, sg := range ask.Suggestions {
		if sg.HotKey == "" || sg.Label == "" {
			continue
		}
		b.WriteString("\n  ")
		b.WriteString(styleHint.Render(
			"[" + sg.HotKey + "] " + sg.Label))
	}
	b.WriteString("\n  ")
	b.WriteString(styleHint.Render(
		"[a]llow once · [s]hift+a always · [d]eny · [q]uit"))
	return b.String()
}

// renderQuestionAsk shapes the AskUserQuestion panel:
//
//   * header chip + full question
//   * vertical option list with `▸` for cursor + `[x]` for picks
//     (multi-select only)
//   * synthesised "Other" row that lets the user type a free-text
//     answer
//   * preview pane to the right of the option list when the focused
//     option carries a `preview` payload
func renderQuestionAsk(m model) string {
	ask := m.questionAsk
	if ask == nil {
		return ""
	}
	q := ask.Question
	otherIdx := len(q.Options)

	var left strings.Builder
	left.WriteString(styleErrorPrefix.Render("? "))
	if q.Header != "" {
		left.WriteString(styleToolDesc.Render("[" + q.Header + "] "))
	}
	left.WriteString(q.Question)
	if q.MultiSelect {
		left.WriteString(styleToolDesc.Render("  (multi-select)"))
	}
	left.WriteByte('\n')

	picked := func(i int) bool {
		if !q.MultiSelect {
			return false
		}
		return m.questionPicked[i]
	}

	for i, opt := range q.Options {
		marker := "  "
		if i == m.questionCursor {
			marker = "▸ "
		}
		check := ""
		if q.MultiSelect {
			if picked(i) {
				check = "[x] "
			} else {
				check = "[ ] "
			}
		}
		left.WriteString(marker)
		left.WriteString(check)
		left.WriteString(opt.Label)
		if opt.Description != "" {
			left.WriteString(styleToolDesc.Render("  — " + opt.Description))
		}
		left.WriteByte('\n')
	}

	// Synthesised "Other" row — always last, identical key handling
	// regardless of multi/single select.
	otherMarker := "  "
	if m.questionCursor == otherIdx {
		otherMarker = "▸ "
	}
	left.WriteString(otherMarker)
	if q.MultiSelect {
		left.WriteString("    ") // align with checkbox column
	}
	left.WriteString(styleToolDesc.Render("Other (type your own answer)"))
	left.WriteByte('\n')

	// Hints depend on mode + state.
	left.WriteByte('\n')
	if m.questionTyping {
		hint := "Enter submit · Esc cancel"
		left.WriteString(styleHint.Render(hint))
	} else if q.MultiSelect {
		hint := "↑/↓ move · Space toggle · Enter submit · O other · N notes · Esc cancel"
		left.WriteString(styleHint.Render(hint))
	} else {
		hint := "↑/↓ move · Enter pick · O other · N notes · Esc cancel"
		left.WriteString(styleHint.Render(hint))
	}

	// Preview pane: shown when the focused option (not "Other") has a
	// non-empty preview. Renders to the right with a vertical bar.
	preview := ""
	if m.questionCursor < len(q.Options) {
		preview = q.Options[m.questionCursor].Preview
	}

	leftBlock := left.String()
	if preview == "" {
		return leftBlock
	}
	return joinSideBySide(leftBlock, preview)
}

// joinSideBySide places `right` to the right of `left` with a "│"
// vertical bar separator. Falls back to stacked rendering if the
// terminal is too narrow.
func joinSideBySide(left, right string) string {
	leftLines := strings.Split(strings.TrimRight(left, "\n"), "\n")
	rightLines := strings.Split(strings.TrimRight(right, "\n"), "\n")
	maxLeft := 0
	for _, l := range leftLines {
		if w := lipgloss.Width(l); w > maxLeft {
			maxLeft = w
		}
	}
	// Pad each row so the right column starts at the same column.
	rowCount := len(leftLines)
	if len(rightLines) > rowCount {
		rowCount = len(rightLines)
	}
	var b strings.Builder
	for i := 0; i < rowCount; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		r := ""
		if i < len(rightLines) {
			r = rightLines[i]
		}
		pad := maxLeft - lipgloss.Width(l)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(l)
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString("  ")
		b.WriteString(styleToolDesc.Render("│ "))
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return b.String()
}

// toolRow is the model's per-call display state. Built up from
// ToolUseStart / ToolUseResult events.
type toolRow struct {
	ID        string
	Name      string
	Input     map[string]any
	StartedAt time.Time
	Elapsed   time.Duration
	Phase     string // "running" | "done" | "error"
	IsError   bool
	Message   string
	// Diff holds the unified-diff text emitted by Edit/Write/MultiEdit
	// — extracted from the tool result so renderToolRows can colorize
	// it without re-parsing every render.
	Diff string
	// ProgressLines is a ring buffer of the most recent streaming
	// updates from the running tool (Bash stdout/stderr, Grep
	// hits, Glob progress, …). Capped at ProgressLineCap so a noisy
	// command doesn't blow out the viewport.
	ProgressLines []string
}

// ProgressLineCap is the max number of streaming progress lines
// retained per running tool. 5 is a good trade between context and
// vertical real estate.
const ProgressLineCap = 5

// formatProgress turns a ProgressData payload into a single display
// line. Returns empty string when the payload has nothing user-
// visible — handler skips append so the spinner doesn't tick.
func formatProgress(d engine.ProgressData) string {
	if d == nil {
		return ""
	}
	if line, ok := d["line"].(string); ok && line != "" {
		kind, _ := d["kind"].(string)
		switch kind {
		case "stderr":
			return "  ! " + line
		default:
			return "    " + line
		}
	}
	switch v := d["matched"].(type) {
	case int:
		return fmt.Sprintf("    matched: %d", v)
	case float64:
		return fmt.Sprintf("    matched: %d", int(v))
	}
	return ""
}

