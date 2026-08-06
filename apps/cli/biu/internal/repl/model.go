// bubbletea Model + Update + View for the biu REPL.
//
// State machine:
//
//   idle      ─ user typing in the textarea, viewport scrollable
//   sending   ─ POSTed to provider, awaiting first delta
//   streaming ─ deltas arriving, appended to the trailing assistant msg
//   error     ─ stream errored; user presses Enter to dismiss
//
// Messaging plan (tea.Cmd → tea.Msg flow):
//
//   user hits Enter ─→ sendCmd(prompt) ─→ deltaMsg / streamErrMsg / streamDoneMsg
//   Ctrl-C ─→ cancelCmd ─→ stream stops, idle
//   /clear ─→ clears history slice + viewport
//   /compact ─→ compactCmd (P6.5; for now just shows hint)

package repl

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
	"github.com/biumind/biumind/apps/cli/biu/internal/output"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/plans"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/statusline"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// Options is the public entry surface (kept compatible with the old
// repl.Run signature so cmd/biu/main.go barely changes).
//
// When Engine is non-nil, the REPL routes user prompts through the
// new agent loop (tools, permissions, compact). Otherwise it falls
// back to legacy chat-only via Provider.ChatStream.
type Options struct {
	Provider client.Provider
	Engine   *engine.QueryEngine // optional — agent loop path
	Model    string
	System   string
	Session  *session.Writer
	In       io.Reader
	Out      io.Writer

	// Styles is the output-style registry powering /output-style.
	// Optional: nil ⇒ a fresh registry with builtins is used.
	Styles *output.Registry

	// MemoryFiles surfaces what the engine actually loaded so /memory
	// can show provenance + sizes. Optional.
	MemoryFiles []memory.File

	// StatusLine, when non-nil, is invoked at every status-bar
	// render to produce a custom right-side segment. Wired by main /
	// SDK from the layered settings.json. nil → no segment.
	StatusLine *statusline.Runner

	// BgTasks gives the REPL visibility into Bash{run_in_background}
	// tasks for the /tasks slash command. Same instance the engine's
	// Bash tool registers with so /tasks sees the live queue.
	// Optional — nil disables /tasks with a friendly message.
	BgTasks *bgtask.Store

	// MCP is the connected-server registry the engine routes
	// `mcp__<server>__<tool>` calls through. Optional — nil
	// disables /mcp with a "feature not enabled" message. Same
	// instance the engine catalog uses so /mcp sees what the model
	// actually has access to.
	MCP *mcp.Registry

	// Trust gates the per-directory "trust this project to run shell
	// hooks" feature. When non-nil, /trust manages persistent grants
	// and the status-line script runner consults it before forking.
	// nil = trust gate disabled (every dir treated as trusted —
	// matches biu's pre-P20.15 posture for backward compat).
	Trust *trust.Store

	// Skills is the file-based SKILL.md registry surfaced by
	// `/<skill-name>` in the slash dispatcher. The body's $ARGS
	// placeholder is substituted with everything after the slash so
	// `/code-review main` expands to the skill body with `$ARGS`
	// replaced by `main`. Lookup falls back to user commands when no
	// skill matches.
	//
	// Optional — nil disables the per-skill slash and the dropdown
	// strips skill rows. Same registry as the engine consults via
	// SkillTool, so editing a SKILL.md file mid-session takes effect
	// after the next /reload (the engine cache is invalidated by the
	// same reload path).
	Skills *skills.Registry
}

type runState int

const (
	stateIdle runState = iota
	stateSending
	stateStreaming
	stateError
)

type model struct {
	// Config
	provider     client.Provider
	engine       *engine.QueryEngine // when set, prefer agent loop path
	modelID      string
	system       string
	sessionLog   *session.Writer
	sessionTitle string // user-supplied label via /rename; "" = unnamed
	theme        string // dark | light | system; empty = follow system
	cacheBreaker string // one-shot nonce armed by /break-cache; cleared after the next send

	// Conversation
	history      []client.Message

	// UI components
	viewport     viewport.Model
	textarea     textarea.Model
	spinner      spinner.Model

	// Streaming state
	state        runState
	streamCancel context.CancelFunc
	pending      strings.Builder // accumulates the in-flight assistant message
	lastErr      error

	// Engine path: per-call decoration shown above the assistant text
	toolRows        []toolRow
	permissionAsk   *engine.PermissionAskEvent
	questionAsk     *engine.UserQuestionAskEvent
	questionCursor  int            // currently focused option index (highlights preview)
	questionPicked  map[int]bool   // multi-select toggle state
	questionOther   bool           // true when the synthesised "Other" row is focused / picked
	questionTyping  bool           // true when the textarea captures free-text for "Other" / notes
	questionTypeFor int            // 0 = Other free-text answer; 1 = notes annotation
	lastUsageInput       int
	lastUsageOutput      int
	lastUsageCacheRead   int
	lastUsageCacheCreate int
	lastStopReason       string

	// Slash-command UI
	slashOpen    bool
	slashItems   []SlashCmd
	slashCursor  int

	// Layout
	width        int
	height       int

	// Markdown renderer (re-built when width changes)
	md           *glamour.TermRenderer

	// Status counters
	turnsCount   int
	startedAt    time.Time

	// Output styles + memory views (Phase D additions).
	styles      *output.Registry
	activeStyle string
	memoryFiles []memory.File

	// Optional user status-line runner. Nil → no custom segment.
	statusLine *statusline.Runner

	// Background-task store wired to the engine's Bash tool. Nil →
	// /tasks soft-warns the feature isn't enabled.
	bgTasks *bgtask.Store

	// MCP registry surfaced by the /mcp slash. Nil → not configured.
	mcp *mcp.Registry

	// Trust gate consulted by the status-line runner + surfaced by
	// the /trust slash. Nil = legacy "everything trusted" mode.
	trust *trust.Store

	// Skills registry; see Options.Skills for semantics. nil
	// disables /<skill-name> dispatch + drops skill rows from the
	// slash dropdown.
	skills *skills.Registry
}

// ─── tea.Msg types ─────────────────────────────────────

// Streaming
type deltaMsg struct{ text string }
type streamErrMsg struct{ err error }
type streamDoneMsg struct{}

// One-shot for the spinner tick (forwarded by spinner.Tick)
// (handled inline)

// ─── Init ──────────────────────────────────────────────

func New(opt Options) tea.Model {
	if opt.Out == nil {
		opt.Out = os.Stdout
	}
	if opt.In == nil {
		opt.In = os.Stdin
	}

	ta := textarea.New()
	ta.Placeholder = "Type a message…  (try: /help)"
	ta.Prompt = "❯ "
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Focus()
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends; Shift+Enter inserts newline

	vp := viewport.New(80, 20)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpinner

	md, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("auto"),
		glamour.WithWordWrap(80),
	)

	styles := opt.Styles
	if styles == nil {
		styles = output.NewRegistry()
	}

	m := model{
		provider:   opt.Provider,
		engine:     opt.Engine,
		modelID:    opt.Model,
		system:     opt.System,
		sessionLog: opt.Session,
		viewport:   vp,
		textarea:   ta,
		spinner:    sp,
		md:         md,
		startedAt:  time.Now(),
		styles:     styles,
		activeStyle: "default",
		memoryFiles: opt.MemoryFiles,
		statusLine: opt.StatusLine,
		bgTasks:    opt.BgTasks,
		mcp:        opt.MCP,
		trust:      opt.Trust,
		skills:     opt.Skills,
	}
	m.viewport.SetContent(m.welcome())
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m model) welcome() string {
	hi := lipgloss.NewStyle().Foreground(colorPurple).Bold(true).
		Render("BiuMind Code")
	hint := lipgloss.NewStyle().Foreground(colorMuted).
		Render("type your prompt; / to see commands; Ctrl-C cancels; Ctrl-D exits")
	return hi + "\n" + hint + "\n"
}

// ─── Update ────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if m.state == stateSending || m.state == stateStreaming {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case launchOkMsg:
		// Goroutine launched; just keep waiting on the pipe.
		return m, waitPipeCmd

	case deltaMsg:
		m.pending.WriteString(msg.text)
		m.state = stateStreaming
		m.refreshBody()
		// Re-arm: keep pulling the next event off the pipe.
		return m, waitPipeCmd

	case streamErrMsg:
		m.lastErr = msg.err
		m.state = stateError
		m.refreshBody()
		// Roll back the optimistic user msg so retry doesn't double.
		if len(m.history) > 0 && m.history[len(m.history)-1].Role == "user" {
			m.history = m.history[:len(m.history)-1]
		}
		m.pending.Reset()
		return m, nil

	case streamDoneMsg:
		final := m.pending.String()
		m.pending.Reset()
		if final != "" {
			m.history = append(m.history,
				client.Message{Role: "assistant", Content: final})
			if m.sessionLog != nil {
				_ = m.sessionLog.Append(session.Event{
					Type: "assistant_message", Content: final,
				})
			}
		}
		m.turnsCount++
		m.state = stateIdle
		m.refreshBody()
		return m, nil

	case engineLaunchedMsg:
		// Goroutine is now draining engine events into enginePipe;
		// keep arming waitEngineCmd so each event reaches Update.
		return m, waitEngineCmd

	case engineEventMsg:
		// Always re-arm: the engine goroutine will push an
		// engineDoneSentinel once the channel closes, and that's the
		// true terminator. handleEngineEvent only updates display state.
		updated, _ := m.handleEngineEvent(msg.ev)
		return updated, waitEngineCmd

	case engineDoneSentinel:
		// Engine channel closed. Stop arming the pipe.
		if m.state != stateError {
			m.state = stateIdle
		}
		m.refreshBody()
		return m, nil
	}

	// Defer to viewport / textarea for everything else (mouse, etc.)
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// User-question panel steals keys while it's up.
	if m.questionAsk != nil {
		return m.handleQuestionKey(msg)
	}

	// Permission dialog steals key handling while it's up.
	if m.permissionAsk != nil {
		key := msg.String()
		// Suggestion hotkeys come first so a suggestion's HotKey
		// can override the standard "a/d/q" path. Suggestions are
		// pre-computed by runner.generateAskSuggestions and carry
		// a single character HotKey (e.g. "w" for "Allow + add to
		// working dirs"). On match, apply ALL of that suggestion's
		// updates (currently always 1) and return Allow.
		for _, sg := range m.permissionAsk.Suggestions {
			if sg.HotKey == "" || sg.HotKey != key {
				continue
			}
			m.permissionAsk.Decision <- engine.PermissionAnswer{
				Decision:       engine.PermAllow,
				AppliedUpdates: []sdkproto.PermissionUpdate{sg.Update},
			}
			m.permissionAsk = nil
			m.refreshBody()
			return m, nil
		}
		switch key {
		case "a":
			m.permissionAsk.Decision <- engine.PermissionAnswer{Decision: engine.PermAllow}
			m.permissionAsk = nil
			m.refreshBody()
			return m, nil
		case "A", "shift+a", "s":
			m.permissionAsk.Decision <- engine.PermissionAnswer{
				Decision: engine.PermAllow, Remember: true,
			}
			m.permissionAsk = nil
			m.refreshBody()
			return m, nil
		case "d":
			m.permissionAsk.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny}
			m.permissionAsk = nil
			m.refreshBody()
			return m, nil
		case "q", "esc":
			m.permissionAsk.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny}
			m.permissionAsk = nil
			if m.streamCancel != nil {
				m.streamCancel()
			}
			m.refreshBody()
			return m, nil
		}
		// Other keys: swallow until decided.
		return m, nil
	}

	// Slash menu has priority for navigation keys.
	if m.slashOpen {
		switch msg.String() {
		case "esc":
			m.slashOpen = false
			return m, nil
		case "down", "ctrl+n":
			if m.slashCursor < len(m.slashItems)-1 {
				m.slashCursor++
			}
			return m, nil
		case "up", "ctrl+p":
			if m.slashCursor > 0 {
				m.slashCursor--
			}
			return m, nil
		case "tab", "enter":
			if msg.String() == "tab" {
				if len(m.slashItems) > 0 {
					m.textarea.SetValue(m.slashItems[m.slashCursor].Name + " ")
					m.slashOpen = false
				}
				return m, nil
			}
			// Enter inside slash menu: execute that command directly.
			if len(m.slashItems) > 0 {
				return m.runSlash(m.slashItems[m.slashCursor].Name)
			}
		}
	}

	switch msg.String() {
	case "ctrl+c":
		// 1st press during stream: cancel the stream.
		// During idle: prompt to confirm exit (just exit for now).
		if m.state == stateStreaming || m.state == stateSending {
			if m.streamCancel != nil {
				m.streamCancel()
			}
			m.state = stateIdle
			m.pending.Reset()
			m.refreshBody()
			return m, nil
		}
		return m, tea.Quit

	case "ctrl+d":
		return m, tea.Quit

	case "enter":
		// Don't send while streaming.
		if m.state == stateStreaming || m.state == stateSending {
			return m, nil
		}
		raw := strings.TrimSpace(m.textarea.Value())
		if raw == "" {
			return m, nil
		}
		if strings.HasPrefix(raw, "/") {
			m.textarea.Reset()
			m.slashOpen = false
			return m.runSlash(raw)
		}
		// Send to LLM.
		m.textarea.Reset()
		m.slashOpen = false
		// Optimistic user-message append shared by both paths.
		m.history = append(m.history, client.Message{Role: "user", Content: raw})
		var msgUUID string
		if m.sessionLog != nil {
			// AppendWithUUID auto-generates a UUID for user_message
			// events (P20.57). The id flows into the engine via
			// SetCurrentUserUUID so file-mutating tools snapshot
			// under the right key.
			msgUUID, _ = m.sessionLog.AppendWithUUID(session.Event{
				Type: "user_message", Content: raw,
			})
		}
		if m.engine != nil {
			m.engine.SetCurrentUserUUID(msgUUID)
			return m.startEngineStream(raw)
		}
		return m.startStream(raw)

	case "shift+enter", "ctrl+j":
		m.textarea.InsertString("\n")
		return m, nil
	}

	// Forward to textarea, then update slash menu if the buffer became
	// (or stopped being) a slash trigger.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cur := strings.TrimSpace(m.textarea.Value())
	if isSlashTrigger(cur) {
		m.slashOpen = true
		extra := append(m.userSlashItems(), m.skillSlashItems()...)
		m.slashItems = matchSlash(cur, extra)
		if m.slashCursor >= len(m.slashItems) {
			m.slashCursor = 0
		}
	} else {
		m.slashOpen = false
	}
	// Forward viewport scrolling
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, tea.Batch(cmd, vpCmd)
}

// runSlash dispatches a "/foo" command. Returns the new model + any cmd.
func (m model) runSlash(line string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return m, nil
	}
	switch parts[0] {
	case "/quit", "/exit":
		return m, tea.Quit
	case "/clear":
		m.history = m.history[:0]
		m.lastErr = nil
		// /clear drops any plan attachment so the fresh session
		// doesn't auto-inject a stale plan after the next compact.
		// Also wipes any allowedPrompts staged by the previous plan —
		// semantic approvals shouldn't outlive the conversation that
		// earned them.
		if m.engine != nil {
			m.engine.Permissions().SetPlanAttachment("")
			m.engine.Permissions().ClearAllowedPrompts()
		}
		m.refreshBody()
		return m, nil
	case "/help":
		m.appendSystemNote(helpText())
		return m, nil
	case "/cost":
		// 默认渲染 session 聚合;`/cost --by-tool` 切到按工具拆分视图
		// (F4 落地后,Agent.CostByTool() 暴露每个工具的 Calls / ElapsedMs /
		// OutputBytes / Errors,这里把它格式化成 leaderboard)。
		if len(parts) > 1 && (parts[1] == "--by-tool" || parts[1] == "by-tool") {
			m.appendSystemNote(m.costByToolNote())
			return m, nil
		}
		m.appendSystemNote(m.costNote())
		return m, nil
	case "/telemetry":
		// /telemetry                     — config status + recent count
		// /telemetry tail [N]            — print last N events (default 10)
		// /telemetry export <path>       — copy events.jsonl to <path>
		// /telemetry enable <endpoint>   — flip on + persist
		// /telemetry disable             — flip off
		m.appendSystemNote(m.handleTelemetry(parts))
		return m, nil
	case "/workflow":
		// /workflow                — list available workflows
		// /workflow show <name>    — preview rendered body + pre-flights
		// /workflow <name> [args]  — verify checks → dispatch
		return m.handleWorkflow(parts, line)
	case "/compact":
		if m.engine == nil {
			m.appendSystemNote("compact: requires engine path (set [providers.anthropic].api_key + mode=direct).")
			return m, nil
		}
		// Run compact in a goroutine so the UI stays responsive.
		// Engine emits CompactStart/CompactDone via its event stream;
		// for the slash invocation we drain a private channel and
		// surface the outcome inline.
		m.appendSystemNote("compact: running…")
		eng := m.engine
		go func() {
			ch := make(chan engine.Event, 16)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = eng.Compact(context.Background(), ch)
				close(ch)
			}()
			for ev := range ch {
				enginePipe <- engineEventMsg{ev: ev}
			}
			<-done
		}()
		return m, nil
	case "/model":
		if len(parts) >= 2 {
			m.modelID = parts[1]
			m.appendSystemNote("model switched: " + m.modelID)
		} else {
			m.appendSystemNote("usage: /model <model-id>")
		}
		return m, nil
	case "/mode":
		if len(parts) < 2 {
			m.appendSystemNote("usage: /mode <default|acceptEdits|plan|bypassPermissions>")
			return m, nil
		}
		if m.engine == nil {
			m.appendSystemNote("/mode: requires engine path")
			return m, nil
		}
		newMode := permissions.ModeFromString(parts[1])
		ctx := m.engine.Permissions()
		// Route plan mode through EnterPlanMode/ExitPlanMode so the
		// session remembers what to restore to. Direct SetMode for the
		// rest — they don't have a "previous" concept.
		switch {
		case newMode == permissions.ModePlan:
			prev := ctx.EnterPlanMode()
			if prev != "" && prev != permissions.ModePlan {
				m.appendSystemNote("permission mode: plan (was " + string(prev) + ")")
			} else {
				m.appendSystemNote("permission mode: plan")
			}
		case ctx.Mode() == permissions.ModePlan:
			// Leaving plan via /mode → restore via the same path the tool
			// uses so prePlanMode bookkeeping clears.
			ctx.ExitPlanMode()
			ctx.SetMode(newMode)
			m.appendSystemNote("permission mode: " + string(newMode))
		default:
			ctx.SetMode(newMode)
			m.appendSystemNote("permission mode: " + string(newMode))
		}
		return m, nil
	case "/reload":
		if m.engine == nil {
			m.appendSystemNote("/reload: requires engine path")
			return m, nil
		}
		cwd, _ := os.Getwd()
		layered, err := clauseSettings.Load(cwd)
		if err != nil {
			m.appendSystemNote("/reload: " + err.Error())
			return m, nil
		}
		// Clear + re-apply rules per source. Hooks aren't reloaded
		// (engine has them by reference and there's no swap).
		ctx := m.engine.Permissions()
		for _, src := range []permissions.Source{
			permissions.SrcUserSettings,
			permissions.SrcProjectSettings,
			permissions.SrcLocalSettings,
		} {
			ctx.ReplaceRules(src, permissions.BehaviorAllow, nil)
			ctx.ReplaceRules(src, permissions.BehaviorDeny, nil)
			ctx.ReplaceRules(src, permissions.BehaviorAsk, nil)
		}
		layered.ApplyToContext(ctx)
		m.appendSystemNote(
			"settings reloaded — permissions refreshed; hooks need restart to pick up.")
		return m, nil
	case "/output-style":
		if len(parts) < 2 {
			var b strings.Builder
			b.WriteString("available styles:\n")
			for _, s := range m.styles.All() {
				b.WriteString("  ")
				b.WriteString(s.Name)
				b.WriteString(" — ")
				b.WriteString(s.Description)
				b.WriteString("\n")
			}
			b.WriteString("usage: /output-style <name>")
			m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
			return m, nil
		}
		picked := m.styles.Get(parts[1])
		m.activeStyle = picked.Name
		// Push the new style onto the engine's system prompt so the
		// next turn picks it up. We rebuild from the original system
		// + the new style — earlier styles drop off.
		if m.engine != nil {
			m.engine.SetSystem(picked.Apply(m.system))
		}
		m.appendSystemNote("output style: " + picked.Name + " — " + picked.Description)
		return m, nil
	case "/memory":
		// Subcommands:
		//   /memory          — list loaded BIUMIND.md + auto-memory state
		//   /memory reload   — re-read BIUMIND.md + MEMORY.md and re-merge
		//                      into the engine system prompt (live, no
		//                      restart). Useful after editing memory
		//                      files in another window.
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}
		switch sub {
		case "", "list":
			m.appendSystemNote(m.memoryStatusNote())
		case "reload":
			m.appendSystemNote(m.reloadMemory())
		default:
			m.appendSystemNote("usage: /memory [list|reload]")
		}
		return m, nil
	case "/remember":
		// Quick capture. Default type is `user`; pass `-t feedback`
		// (project / reference) to override. Body is the rest of the
		// line. After the write we automatically reload the engine's
		// system prompt so the just-saved memory is visible to the
		// model on the very next turn — closes the read-write loop
		// without requiring `/memory reload`.
		rest := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		m.appendSystemNote(m.handleRemember(rest))
		return m, nil
	case "/init":
		// /init                — write BIUMIND.md if missing
		// /init --force        — overwrite an existing BIUMIND.md
		// /init --dry-run      — print without writing
		//
		// Detector reads go.mod / package.json / Cargo.toml /
		// pyproject.toml / pom.xml / build.gradle / Gemfile /
		// composer.json + scans for cursor / copilot rules / docker
		// / Makefile / etc. Output is a starter doc the user is
		// expected to edit; `--force` lets them re-run after
		// changing the project layout.
		m.appendSystemNote(m.handleInit(parts))
		return m, nil
	case "/todo":
		if m.engine == nil {
			m.appendSystemNote("/todo: requires engine path")
			return m, nil
		}
		todos := m.engine.State().TodosFor("")
		if len(todos) == 0 {
			m.appendSystemNote("(no todos)")
			return m, nil
		}
		var b strings.Builder
		for _, t := range todos {
			fmt.Fprintf(&b, "  [%s] %s\n", t.Status, t.Content)
		}
		m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
		return m, nil
	case "/agents":
		// /agents                                — list registered types
		// /agents create <name> [--scope user|project] [--from <preset>] [--force]
		//                                        — scaffold a new agent .md
		m.appendSystemNote(m.handleAgents(parts))
		return m, nil
	case "/ultraplan":
		// Spawn the built-in Plan sub-agent on the user's task by
		// synthesising a parent prompt that explicitly delegates via
		// AgentTool[subagent_type=Plan] + folds the returned plan
		// into ExitPlanMode. Works without a dedicated runtime path
		// because the model already has all the tools it needs.
		task := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		if task == "" {
			m.appendSystemNote("usage: /ultraplan <task description>")
			return m, nil
		}
		if m.engine == nil {
			m.appendSystemNote("/ultraplan: requires engine path (cloud mode lacks AgentTool today)")
			return m, nil
		}
		bridgePrompt := "Use the `Agent` tool with `subagent_type=\"Plan\"` to design " +
			"an implementation plan for the following task. Once the Plan agent " +
			"returns, review its output, then call `ExitPlanMode` with the polished " +
			"plan as the `plan` argument and an `allowedPrompts` list covering the " +
			"shell commands the plan will require.\n\n" +
			"Task:\n" + task
		// Display the user's actual phrasing in the transcript before
		// we dispatch the synthesised prompt to the engine.
		m.history = append(m.history,
			client.Message{Role: "user", Content: "/ultraplan " + task})
		if m.sessionLog != nil {
			_ = m.sessionLog.Append(session.Event{
				Type: "user_message", Content: "/ultraplan " + task,
			})
		}
		return m.startEngineStream(bridgePrompt)
	case "/review":
		// /review [scope]
		//   scope = "" (default)   → review the current branch's diff
		//                            against `main`/`master` (resolved
		//                            by the agent itself via git)
		//   scope = file paths    → review just those files
		//   scope = arbitrary text → free-form scope passed verbatim
		//
		// Stateless bridge: synthesise a prompt that delegates to the
		// CodeReview built-in via AgentTool. Mirrors /ultraplan; no
		// new runtime path needed because the model already has both
		// the Agent tool and the registered CodeReview definition.
		scope := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		if m.engine == nil {
			m.appendSystemNote("/review: requires engine path (cloud mode lacks AgentTool today)")
			return m, nil
		}
		if scope == "" {
			scope = "the diff between the current branch and the default base branch (main / master). Use `git diff` to enumerate changed files; do not assume — verify."
		}
		bridgePrompt := "Use the `Agent` tool with `subagent_type=\"CodeReview\"` " +
			"to review the change described below. Once the CodeReview " +
			"agent returns, surface its findings to me unmodified — do " +
			"not summarise or edit the verdict.\n\n" +
			"Scope:\n" + scope
		m.history = append(m.history,
			client.Message{Role: "user", Content: "/review " + scope})
		if m.sessionLog != nil {
			_ = m.sessionLog.Append(session.Event{
				Type: "user_message", Content: "/review " + scope,
			})
		}
		return m.startEngineStream(bridgePrompt)
	case "/verify":
		// /verify [scope]
		//
		// Sister command to /review. Where /review READS the change,
		// /verify EXECUTES it: spins up servers / runs tests / probes
		// edge cases and returns a PASS/FAIL/PARTIAL verdict with
		// command outputs as evidence.
		//
		// Stateless bridge: synthesise a prompt that delegates to the
		// Verification built-in via AgentTool.
		scope := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		if m.engine == nil {
			m.appendSystemNote("/verify: requires engine path (cloud mode lacks AgentTool today)")
			return m, nil
		}
		if scope == "" {
			scope = "the implementation in the current branch's diff against the default base. Read BIUMIND.md for build/test commands first; run them; pick at least one adversarial probe relevant to the change type."
		}
		bridgePrompt := "Use the `Agent` tool with `subagent_type=\"Verification\"` " +
			"to verify the change described below. The agent will end with " +
			"`VERDICT: PASS|FAIL|PARTIAL` — surface the entire reply unmodified.\n\n" +
			"Scope:\n" + scope
		m.history = append(m.history,
			client.Message{Role: "user", Content: "/verify " + scope})
		if m.sessionLog != nil {
			_ = m.sessionLog.Append(session.Event{
				Type: "user_message", Content: "/verify " + scope,
			})
		}
		return m.startEngineStream(bridgePrompt)
	case "/plan":
		// /plan          → list newest 5 with previews
		// /plan show [id]→ print body inline
		dir, err := plans.Dir()
		if err != nil {
			m.appendSystemNote("/plan: " + err.Error())
			return m, nil
		}
		sub := ""
		ref := ""
		if len(parts) >= 2 {
			sub = parts[1]
		}
		if len(parts) >= 3 {
			ref = parts[2]
		}
		switch sub {
		case "", "list":
			rows, err := plans.ListPlans(dir)
			if err != nil {
				m.appendSystemNote("/plan: " + err.Error())
				return m, nil
			}
			if len(rows) == 0 {
				m.appendSystemNote("(no plans yet — saved when ExitPlanMode runs)")
				return m, nil
			}
			var b strings.Builder
			max := 5
			if len(rows) < max {
				max = len(rows)
			}
			for _, p := range rows[:max] {
				fmt.Fprintf(&b, "  %s  %s  %s\n",
					p.ID, p.ModTime.Format("2006-01-02 15:04"), p.FirstLine)
			}
			if len(rows) > max {
				fmt.Fprintf(&b, "  …(%d more; run `biu plan list`)", len(rows)-max)
			}
			m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
		case "show":
			lookup := ref
			if lookup == "" {
				lookup = "latest"
			}
			p, ok := plans.FindByID(dir, lookup)
			if !ok {
				m.appendSystemNote("/plan: no plan matching " + lookup)
				return m, nil
			}
			body, err := plans.Read(p)
			if err != nil {
				m.appendSystemNote("/plan: " + err.Error())
				return m, nil
			}
			m.appendSystemNote("# " + p.ID + "\n\n" + strings.TrimSpace(body))
		case "diff":
			// Show drift between the active plan and what the model
			// has actually run since ExitPlanMode. Surfaces the same
			// data the engine would auto-attach next turn.
			if m.engine == nil {
				m.appendSystemNote("/plan diff: requires engine path")
				return m, nil
			}
			v := m.engine.PlanVerifier()
			if v == nil {
				m.appendSystemNote("/plan diff: no verifier wired")
				return m, nil
			}
			if !v.HasPlan() {
				m.appendSystemNote("/plan diff: no active plan")
				return m, nil
			}
			body := v.BuildAttachment()
			if body == "" {
				m.appendSystemNote("/plan diff: no drift detected — every tool call traces back to the plan.")
				return m, nil
			}
			m.appendSystemNote(body)
		case "approvals":
			// List the semantic batch-approvals currently staged so
			// the user can audit what auto-allows are still active.
			if m.engine == nil {
				m.appendSystemNote("/plan approvals: requires engine path")
				return m, nil
			}
			perms := m.engine.Permissions()
			if perms == nil {
				m.appendSystemNote("/plan approvals: no permissions context")
				return m, nil
			}
			ap := perms.AllowedPrompts()
			if len(ap) == 0 {
				m.appendSystemNote("/plan approvals: none staged. ExitPlanMode populates these from the model's `allowedPrompts` list.")
				return m, nil
			}
			var b strings.Builder
			b.WriteString("Active plan approvals (auto-allowed when classifier matches):\n")
			for _, p := range ap {
				b.WriteString("  - ")
				b.WriteString(p.Tool)
				b.WriteString(": ")
				b.WriteString(p.Prompt)
				b.WriteString("\n")
			}
			m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
		default:
			m.appendSystemNote("usage: /plan [list] | /plan show [<id>|latest] | /plan diff | /plan approvals")
		}
		return m, nil
	case "/tasks":
		// /tasks                  — list every background task with status
		// /tasks output <id> [n]  — print captured output (delta optional)
		// /tasks kill <id>        — terminate a running task
		// /tasks killall          — terminate everything still running
		m.appendSystemNote(m.handleTasks(parts))
		return m, nil
	case "/mcp":
		// /mcp                — list connected MCP servers + tool counts
		// /mcp <server>       — show that server's tools with descriptions
		m.appendSystemNote(m.handleMCP(parts))
		return m, nil
	case "/plugin":
		// /plugin                       — list installed plugins
		// /plugin <name>                — show one plugin's details
		// /plugin enable|disable <n>    — toggle in settings.json
		// /plugin reload                — re-scan plugin dirs
		m.appendSystemNote(m.handlePlugin(parts))
		return m, nil
	case "/doctor":
		// Runs every diagnostic and renders the aggregate report.
		m.appendSystemNote(m.handleDoctor(parts))
		return m, nil
	case "/permissions":
		// Show active permission rules + mode.
		m.appendSystemNote(m.handlePermissions(parts))
		return m, nil
	case "/add-dir":
		// Register an extra working directory; --remember persists.
		m.appendSystemNote(m.handleAddDir(parts))
		return m, nil
	case "/remove-dir":
		// Drop a working directory; persisted entries also removed.
		m.appendSystemNote(m.handleRemoveDir(parts))
		return m, nil
	case "/hooks":
		// List registered hooks; optional event-name filter.
		m.appendSystemNote(m.handleHooks(parts))
		return m, nil
	case "/rewind":
		// List + apply file-state snapshots from this session.
		m.appendSystemNote(m.handleRewind(parts))
		return m, nil
	case "/release-notes":
		// Show embedded biu release notes.
		m.appendSystemNote(m.handleReleaseNotes(parts))
		return m, nil
	case "/export":
		// Write current session transcript to a user-supplied path.
		m.appendSystemNote(m.handleExport(parts))
		return m, nil
	case "/login":
		// Show OAuth token state (not signed in / signed in + expiry).
		m.appendSystemNote(m.handleLogin(parts))
		return m, nil
	case "/logout":
		// Delete locally-stored OAuth tokens.
		m.appendSystemNote(m.handleLogout(parts))
		return m, nil
	case "/branch":
		// Git branch state — current branch, upstream, dirty, log.
		m.appendSystemNote(m.handleBranch(parts))
		return m, nil
	case "/ide":
		// IDE bridge status + setup instructions.
		m.appendSystemNote(m.handleIDE(parts))
		return m, nil
	case "/share":
		// Export session to tmp + copy path to clipboard.
		m.appendSystemNote(m.handleShare(parts))
		return m, nil
	case "/install":
		// Binary diagnostics — version + install method + update.
		m.appendSystemNote(m.handleInstall(parts))
		return m, nil
	case "/effort":
		// Tiered model switch (high/medium/low or explicit id).
		updated, note := m.handleEffort(parts)
		updated.appendSystemNote(note)
		return updated, nil
	case "/fast":
		// Shortcut for /effort low.
		updated, note := m.handleFast(parts)
		updated.appendSystemNote(note)
		return updated, nil
	case "/usage":
		// Historical token + USD totals.
		m.appendSystemNote(m.handleUsage(parts))
		return m, nil
	case "/diff":
		// git diff vs HEAD / staged / a branch range.
		m.appendSystemNote(m.handleDiff(parts))
		return m, nil
	case "/copy":
		// Copy last assistant text / code / pattern match.
		m.appendSystemNote(m.handleCopy(parts))
		return m, nil
	case "/stats":
		// Current session statistics.
		m.appendSystemNote(m.handleStats(parts))
		return m, nil
	case "/summary":
		// Structured CPU-only paragraph of what this session covered.
		m.appendSystemNote(m.handleSummary(parts))
		return m, nil
	case "/onboarding":
		// First-run guide.
		m.appendSystemNote(m.handleOnboarding(parts))
		return m, nil
	case "/theme":
		// Switch colour palette.
		updated, note := m.handleTheme(parts)
		updated.appendSystemNote(note)
		return updated, nil
	case "/trust":
		// /trust                  — show current dir's trust state + the list
		// /trust here             — persist trust for the current cwd
		// /trust session          — trust the current cwd for this session only
		// /trust add <path>       — persist trust for a specific path
		// /trust remove <path>    — revoke a persistent grant
		m.appendSystemNote(m.handleTrust(parts))
		return m, nil
	case "/sessions":
		dir, err := session.SessionsDir()
		if err != nil {
			m.appendSystemNote("/sessions: " + err.Error())
			return m, nil
		}
		rows, err := session.ListSessions(dir)
		if err != nil {
			m.appendSystemNote("/sessions: " + err.Error())
			return m, nil
		}
		if len(rows) == 0 {
			m.appendSystemNote("(no saved sessions)")
			return m, nil
		}
		var b strings.Builder
		max := 10
		if len(rows) < max {
			max = len(rows)
		}
		for _, r := range rows[:max] {
			fmt.Fprintf(&b, "  %s  %d msgs  %s\n", r.ID, r.MessageCount, r.FirstPrompt)
		}
		if len(rows) > max {
			fmt.Fprintf(&b, "  …(%d more; run `biu sessions list` for the full set)", len(rows)-max)
		}
		m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
		return m, nil
	case "/rename":
		newM, note := m.handleRename(parts)
		newM.appendSystemNote(note)
		return newM, nil
	case "/commit":
		m.appendSystemNote(m.handleCommit(parts))
		return m, nil
	case "/pr":
		m.appendSystemNote(m.handlePR(parts))
		return m, nil
	case "/issue":
		m.appendSystemNote(m.handleIssue(parts))
		return m, nil
	case "/pr-comments":
		m.appendSystemNote(m.handlePRComments(parts))
		return m, nil
	case "/upgrade":
		m.appendSystemNote(m.handleUpgrade(parts))
		return m, nil
	case "/tag":
		m.appendSystemNote(m.handleTag(parts))
		return m, nil
	case "/env":
		m.appendSystemNote(m.handleEnv(parts))
		return m, nil
	case "/feedback":
		m.appendSystemNote(m.handleFeedback(parts))
		return m, nil
	case "/break-cache":
		newM, note := m.handleBreakCache(parts)
		newM.appendSystemNote(note)
		return newM, nil
	case "/resume":
		if m.engine == nil {
			m.appendSystemNote("/resume: requires engine path")
			return m, nil
		}
		dir, err := session.SessionsDir()
		if err != nil {
			m.appendSystemNote("/resume: " + err.Error())
			return m, nil
		}
		// Empty argument → show a numbered picker. Stateless: the user
		// re-runs `/resume #<n>` (or `/resume latest`) from the menu.
		// This avoids the REPL needing a modal "pending pick" state and
		// keeps every transition replayable from history.
		if len(parts) < 2 {
			m.appendSystemNote(buildResumeMenu(dir))
			return m, nil
		}
		s, ok := resolveResumeArg(dir, parts[1])
		if !ok {
			m.appendSystemNote("/resume: no session matching " + parts[1] +
				" (try `/resume` for the picker)")
			return m, nil
		}
		if err := session.Replay(s.Path, m.engine.State()); err != nil {
			m.appendSystemNote("/resume: " + err.Error())
			return m, nil
		}
		// Mirror replay into REPL history so the viewport renders it.
		m.history = m.history[:0]
		for _, msg := range m.engine.State().Snapshot() {
			text := ""
			for _, b := range msg.Content {
				if b.Type == state.ContentText {
					text += b.Text
				}
			}
			if text == "" {
				continue
			}
			m.history = append(m.history, client.Message{
				Role: string(msg.Role), Content: text,
			})
		}
		m.appendSystemNote(fmt.Sprintf("/resume: loaded %d messages from %s",
			s.MessageCount, s.ID))
		return m, nil
	default:
		// Unknown built-in command → user-defined commands first
		// (lighter-weight markdown templates), then file-based skills
		// (SKILL.md packages) before giving up. Order keeps the
		// existing /commands behaviour stable; collisions are
		// vanishingly rare since the two file systems are separate
		// (`~/.biumind/commands/<n>.md` vs
		// `~/.biumind/skills/<n>/SKILL.md`).
		if cmd, args, ok := m.lookupUserCommand(parts[0], line); ok {
			return m.runUserCommand(cmd, args)
		}
		if sk, args, ok := m.lookupSkill(parts[0], line); ok {
			return m.runSkill(sk, args)
		}
		m.appendSystemNote("unknown command: " + parts[0])
		return m, nil
	}
}

// ─── Streaming ─────────────────────────────────────────

func (m model) startStream(prompt string) (tea.Model, tea.Cmd) {
	// User message + session log are appended by the caller (handleKey)
	// before we get here so both legacy + engine paths share the same
	// optimistic-append behaviour.
	_ = prompt
	m.pending.Reset()
	m.state = stateSending
	m.refreshBody()

	parent := context.Background()
	ctx, cancel := context.WithCancel(parent)
	m.streamCancel = cancel

	hist := append([]client.Message(nil), m.history...)
	model := m.modelID
	system := applyCacheBreaker(m.system, m.cacheBreaker)
	m.cacheBreaker = "" // one-shot
	provider := m.provider

	// Cmd 1: launch the network call + goroutine that fan-outs frames
	// into the global streamPipe. Returns nil (no immediate Msg) — the
	// real output flows through Cmd 2.
	launchCmd := func() tea.Msg {
		frames, err := provider.ChatStream(ctx, client.ChatRequest{
			Model:     model,
			System:    system,
			Messages:  hist,
			MaxTokens: 4096,
		})
		if err != nil {
			return streamErrMsg{err: err}
		}
		go func() {
			for f := range frames {
				switch f.Kind {
				case client.KindDelta:
					streamPipe <- deltaMsg{text: f.Text}
				case client.KindError:
					streamPipe <- streamErrMsg{err: f.Err}
				}
			}
			streamPipe <- streamDoneMsg{}
		}()
		// Returning nil is invalid — wrap as a passthrough.
		return launchOkMsg{}
	}

	return m, tea.Batch(launchCmd, waitPipeCmd, m.spinner.Tick)
}

// launchOkMsg is the no-op signal that the goroutine has started.
type launchOkMsg struct{}

// streamPipe is the bridge from the streaming goroutine back to the
// tea event loop. Buffered enough to absorb a fast LLM without
// blocking the streamCmd goroutine.
var streamPipe = make(chan tea.Msg, 256)

// waitPipeCmd is the Cmd that pulls the next event off streamPipe and
// hands it to Update. We re-arm it on every delta so the next event
// is always being waited on.
func waitPipeCmd() tea.Msg {
	return <-streamPipe
}

// ─── View ──────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return "initializing…"
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.statusBar(),
		m.viewport.View(),
		m.inputArea(),
	)
}

// ─── Helpers ───────────────────────────────────────────

func (m *model) applySize(w, h int) {
	m.width = w
	m.height = h
	taHeight := 3
	statusHeight := 1
	hintHeight := 1
	gap := 1
	vpHeight := h - taHeight - statusHeight - hintHeight - gap*2
	if vpHeight < 5 {
		vpHeight = 5
	}
	m.textarea.SetWidth(w - 4)
	m.viewport.Width = w
	m.viewport.Height = vpHeight
	// Re-render markdown at new width (glamour wraps to width).
	if w > 8 {
		md, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("auto"),
			glamour.WithWordWrap(w-4),
		)
		if err == nil {
			m.md = md
		}
	}
	m.refreshBody()
}

func (m *model) refreshBody() {
	var b strings.Builder
	b.WriteString(m.welcome())
	for _, msg := range m.history {
		switch msg.Role {
		case "user":
			b.WriteString(renderUserPrefix())
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString(renderAssistantPrefix())
			b.WriteString(m.renderMarkdown(msg.Content))
			b.WriteString("\n")
		case "system":
			b.WriteString(styleHint.Render(msg.Content))
			b.WriteString("\n\n")
		}
	}
	// Tool rows (engine path) — rendered above any in-flight text so the
	// user sees ⏺ Read foo.go ahead of the assistant's narration.
	if rows := renderToolRows(m.toolRows); rows != "" {
		b.WriteString(rows)
	}
	// In-flight assistant
	if m.state == stateSending || m.state == stateStreaming {
		b.WriteString(renderAssistantPrefix())
		if m.pending.Len() == 0 {
			b.WriteString(m.spinner.View() + " thinking…")
		} else {
			b.WriteString(m.renderMarkdown(m.pending.String()))
		}
		b.WriteString("\n")
	}
	if m.state == stateError && m.lastErr != nil {
		b.WriteString(renderErrorPrefix())
		b.WriteString(m.lastErr.Error())
		b.WriteString("\n\n")
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m *model) appendSystemNote(s string) {
	m.history = append(m.history, client.Message{Role: "system", Content: s})
	m.refreshBody()
}

func (m model) renderMarkdown(s string) string {
	if m.md == nil {
		return s
	}
	out, err := m.md.Render(s)
	if err != nil {
		return s
	}
	return strings.TrimRight(out, "\n")
}

func (m model) inputArea() string {
	box := styleInputBoxFocused.Render(m.textarea.View())
	hint := styleHint.Render(m.hintLine())
	body := box + "\n" + hint
	if m.slashOpen {
		body = m.slashPanel() + "\n" + body
	}
	if ask := renderPermissionAsk(m.permissionAsk); ask != "" {
		body = ask + "\n" + body
	}
	if q := renderQuestionAsk(m); q != "" {
		body = q + "\n" + body
	}
	return body
}

func (m model) hintLine() string {
	switch m.state {
	case stateSending, stateStreaming:
		return "  Ctrl-C cancels · Shift-Enter for newline"
	case stateError:
		return "  press Enter to dismiss · / for commands · Ctrl-D exits"
	}
	return "  Enter sends · Shift-Enter newline · / commands · Ctrl-D exits"
}

func (m model) slashPanel() string {
	if len(m.slashItems) == 0 {
		return styleSlashPanel.Render("(no matching command)")
	}
	var b strings.Builder
	for i, c := range m.slashItems {
		row := c.Name
		if c.Args != "" {
			row += " " + c.Args
		}
		row += "  " + c.Description
		if i == m.slashCursor {
			b.WriteString(styleSlashSelected.Render(row))
		} else {
			b.WriteString(styleSlashDim.Render(row))
		}
		if i < len(m.slashItems)-1 {
			b.WriteString("\n")
		}
	}
	return styleSlashPanel.Render(b.String())
}


func helpText() string {
	return strings.TrimSpace(`
biu REPL commands:
  /help               this list
  /init               write a starter BIUMIND.md in cwd
  /memory             show loaded BIUMIND.md sources
  /clear              wipe conversation history
  /compact            summarise old turns to save context
  /cost [--by-tool]   running token + $ usage; --by-tool = per-tool leaderboard
  /todo               print the in-session todo checklist
  /agents             list registered sub-agent types
  /model <id>         switch model (this session only)
  /mode <m>           switch permission mode (default / acceptEdits / plan / bypassPermissions)
  /output-style <n>   switch output style (concise / explanatory / teaching / default)
  /quit               exit
shortcuts:
  Enter               send
  Shift-Enter         newline
  Ctrl-C              cancel current stream
  Ctrl-D              exit
`)
}



