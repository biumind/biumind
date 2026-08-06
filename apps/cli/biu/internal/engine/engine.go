// QueryEngine — biu's agent loop.
//
// Public surface is one method:
//
//   func (e *QueryEngine) Submit(ctx, prompt) <-chan Event
//
// Caller iterates the channel until close. Lifecycle of one Submit:
//
//   1. Hook: UserPromptSubmit / plan-drift / bg-task / teammate
//      attachments folded into the system message (turn.go:127–196).
//   2. Append user message to AppState
//   3. Turn loop:
//        emit RequestStart
//        provider.Stream(messages, tools, system) → frame chan
//        ParseStream → text + tool_use blocks → AssistantMessageEvent
//        append assistant message to AppState
//        if stop_reason == end_turn:    emit Done; return
//        if stop_reason == tool_use:    runBatches → tool_result msg → continue
//        if stop_reason == max_tokens:  runCompact → continue (max_tokens recovery)
//        if Interrupt() fires:          synthesise tool_results for unfinished
//                                        tool_use, emit Done{interrupted}; return
//        otherwise:                     emit Error; return
//
// Concurrency: one Submit at a time per QueryEngine instance. Re-entry
// returns ErrConcurrent. Sub-agents (AgentTool) get their own engine.

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/compact"
	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ErrConcurrentSubmit is returned (via the channel as ErrorEvent) when
// Submit is called while another Submit is in flight.
var ErrConcurrentSubmit = errors.New("engine: another Submit is in progress")

// Provider is the LLM adapter contract. Implementations live in
// internal/client/* — we keep the interface narrow so tests can plug
// in fakes.
type Provider interface {
	// Stream sends a chat request and returns a channel of stream
	// frames. The channel must close when the stream ends. Any error
	// before frames start (HTTP 4xx, network) is returned directly.
	Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error)
}

// StreamRequest is the canonical request shape. Provider adapters
// translate this to Anthropic / OpenAI / model-relay wire format.
type StreamRequest struct {
	Model     string
	System    string
	Messages  []state.Message
	Tools     []ToolSpec
	MaxTokens int

	// ContextManagement lets the provider tell the API server to
	// auto-clear old tool uses / thinking blocks when the input
	// crosses a threshold. Mirrors Anthropic's `context_management`
	// request field (see compact.APIContextManagement). nil =
	// don't include the field. Adapters that target non-Anthropic
	// providers should ignore.
	ContextManagement any
}

// ToolSpec is what we give the LLM so it knows the catalog. Mirrors
// Anthropic's tool spec shape (name + description + input_schema).
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Options configures the QueryEngine. SystemPrompt + Cwd are typical;
// the rest are advanced overrides for tests / sub-agents.
type Options struct {
	State    *state.AppState // required
	Tools    ToolRegistry    // required
	Provider Provider        // required
	Model    string          // required
	System   string
	Cwd      string

	// MaxToolTurns caps how many tool_use loops we run before giving
	// up. Defaults to 25. 0 = use default.
	MaxToolTurns int

	// MaxTokens forwarded to the provider. Defaults to 4096.
	MaxTokens int

	// BypassPermissions disables the ask flow entirely. Used by
	// `biu --dangerously-skip-permissions` and by some hook contexts.
	// Functionally equivalent to setting Permissions.SetMode(ModeBypass).
	BypassPermissions bool

	// Permissions is the rule + mode context the runner consults
	// before each tool call. Optional: when nil the engine builds a
	// fresh empty Context (BypassPermissions is then honoured by
	// flipping its mode to bypassPermissions).
	Permissions *permissions.Context

	// Hooks is the user-defined hook registry (shell commands fired at
	// PreToolUse / PostToolUse / UserPromptSubmit / Stop / etc).
	// Optional: nil = no hooks fire.
	Hooks *hooks.Registry

	// Cost is the per-session token + USD tracker. Optional: when nil
	// the engine builds a fresh one keyed off Model. Each LLM
	// stream_usage frame increments it.
	Cost *cost.Tracker

	// CompactMaxTokens caps the per-conversation token budget before
	// auto-compact fires.
	//
	// 0 (default) — derive from the model via
	// compact.AutoCompactThreshold(model). The threshold accounts for
	// max_output reservation (20K) + a 13K buffer, so a 200K model
	// gets a ~167K trigger. The buffer scales with the model's context
	// window; CLAUDE_AUTOCOMPACT_PCT_OVERRIDE / DISABLE_COMPACT env
	// vars work.
	// Negative — disable auto-compact entirely (manual /compact still
	// works). Tests use this for hermetic runs.
	// Positive — explicit override; bypasses the model-derived
	// threshold. Used by tests that need deterministic small windows.
	CompactMaxTokens int

	// CompactInstruction is appended to the standard compact prompt
	// (e.g. "focus on Go code changes"). Optional.
	CompactInstruction string

	// SessionMemory is the writer compact pushes summaries into
	// after a successful compact (Current State section). nil
	// disables — no cross-compact / cross-restart memory carry-
	// over. Wiring layer (cmd/biu/wiring) constructs a per-session
	// internal/sessionmemory.SessionMemory and passes it here.
	SessionMemory compact.SessionMemoryWriter

	// UsageLogger appends one JSONL record to ~/.biu/usage.jsonl per
	// completed turn. Wired from main.go / SDK so headless / bridge
	// runs all share the same on-disk history. Optional — nil
	// silently disables persistence.
	UsageLogger *cost.Logger

	// FileChanged is invoked whenever a file-mutating tool reports
	// it just wrote to disk. main.go wires this to the LSP pool's
	// Touch so language servers see incremental updates instead of
	// stale didOpen content. Optional — nil = no-op.
	FileChanged func(absPath string)

	// AgentID propagates into ToolEnv so tools can scope themselves
	// (sub-agent doesn't write to project memory, etc.). Empty for
	// the top-level engine. Also stamped onto every Event's SessionID
	// so embedders can route frames without per-call sessionID plumbing.
	AgentID string

	// ParentToolUseID is the outer AgentTool tool_use_id this engine
	// was spawned under (sub-agent case). Stamped onto every emitted
	// Event so the SDK protocol's parent_tool_use_id field can be
	// filled correctly without any caller bookkeeping. Empty string for
	// the user-facing root engine — the spawner sets it for sub-agents.
	ParentToolUseID string

	// PlanVerifier observes destructive tool calls when a plan is
	// active and surfaces drift attachments alongside the
	// approved-plan attachment. Optional — nil disables verification.
	PlanVerifier PlanVerifier

	// PlanDriftThreshold is the cumulative drift count that triggers
	// the `<plan-drift>` attachment. 0 → 1 (any drift surfaces).
	// Negative → never surface (still observe, useful for /plan diff
	// inspection only).
	PlanDriftThreshold int

	// PlanHinter analyses each user prompt and folds a "consider
	// EnterPlanMode" system note into the next turn when the prompt
	// looks like a large change. Nil disables the suggestion path.
	PlanHinter PlanHinter

	// BgTaskNotifier surfaces completed background tasks as system
	// attachments at the head of every user turn — without this, the
	// model has to poll BashOutput on a timer. Nil disables the
	// notification path.
	BgTaskNotifier BgTaskNotifier

	// Teams + TeamMessages let callers share team registries between
	// the engine and the orchestration tool layer (P20.53-2). When
	// nil, engine.New() creates fresh in-memory registries. Wiring
	// passes the same instances into both engine.New and
	// orchestration.Register so SendMessage and SpawnAsync route
	// through the same tables.
	Teams        *TeamRegistry
	TeamMessages *MessageInbox
}

// BgTaskNotifier is the small surface the engine consumes from the
// background-task store. Defined here (not imported) to keep the
// dependency direction one-way: bgtask depends on nothing engine-
// shaped, engine only sees this interface.
type BgTaskNotifier interface {
	// PendingCompletions returns + drains snapshots of background
	// tasks that reached a terminal state since the last call.
	// Returns nil when nothing is pending so the engine can cheaply
	// skip the attachment block.
	PendingCompletions() []BgTaskCompletion
}

// BgTaskCompletion is the engine's view of a finished bg task.
// Lean on purpose — only what the system attachment renders.
type BgTaskCompletion struct {
	ID       string
	Command  string
	Status   string // "done" / "failed" / "killed"
	ExitCode int
	// Tail is up to ~10 trailing output lines (after the buffer
	// cap, with [stderr] prefix preserved). Empty when the task
	// produced no captured output.
	Tail []string
}

// PlanHinter is the small surface the engine consumes from
// internal/planhint. Defined here to keep the dependency direction
// one-way.
type PlanHinter interface {
	Enabled() bool
	Analyse(prompt string) PlanHint
}

// PlanHint mirrors planhint.Suggestion — replicated here so engine
// callers don't have to import planhint just to inspect the result.
type PlanHint struct {
	Note           string
	MatchedKeyword string
}

// QueryEngine owns a session's worth of agent runtime.
type QueryEngine struct {
	state    *state.AppState
	tools    ToolRegistry
	provider Provider

	model           string
	system          string
	cwd             string
	maxToolTurns    int
	maxTokens       int
	perms           *permissions.Context
	hooks           *hooks.Registry
	cost            *cost.Tracker
	compact         *compact.Auto
	compactWarn     *compact.WarningState
	sessionMem      compact.SessionMemoryWriter
	usageLogger     *cost.Logger
	fileChanged     func(string)
	agentID         string
	parentToolUseID string // outer AgentTool tool_use_id for sub-agents; empty for root

	// planVerifier observes destructive tool calls when a plan is
	// active and surfaces drift attachments through the same channel
	// as the plan re-injection. Optional; nil = no verification.
	planVerifier PlanVerifier

	// planDriftThreshold is the drift count that triggers the system
	// attachment. 0 = disabled (verify but don't surface). Default 1
	// when a verifier is attached: any drift surfaces.
	planDriftThreshold int

	// planHinter analyses each user prompt and folds a "consider
	// EnterPlanMode" system note into the next turn when the prompt
	// looks like a large change. Nil disables the suggestion path.
	planHinter PlanHinter

	// bgTaskNotifier surfaces background-task completions at the
	// head of every user turn. nil = no auto-notification (model
	// must poll BashOutput manually). See BgTaskNotifier.
	bgTaskNotifier BgTaskNotifier

	// selections is the deferred-tool unlock set for this engine
	// (P20.51 Phase 2). Persists for the lifetime of the QueryEngine
	// — survives multi-turn conversations within one Submit call AND
	// across separate Submits on the same engine, so the user doesn't
	// re-search the same tool every prompt. ToolSearch wires its
	// matches in via env.Selections.Add; turn.go consults it when
	// building the wire-level tool catalog.
	selections *DeferredSelection

	// asyncAgents is the inbox for fire-and-forget sub-agents (P20.53).
	// Spawner.SpawnAsync writes a TeammateCompletion when the
	// background goroutine finishes; turn.go drains Pending() at the
	// head of every user-turn so the parent picks up results without
	// polling. nil = swarm support disabled (legacy callers + tests).
	asyncAgents AsyncAgentStore

	// teams is the named-group addressing layer for the swarm
	// (P20.53-2). Lets SendMessage route by friendly name instead of
	// handle id.
	teams *TeamRegistry

	// teamMessages is the per-teammate follow-up queue (P20.53-2).
	// SendMessageTool enqueues; SpawnAsync's goroutine drains the
	// head of the queue between Submits and re-Submits the teammate
	// with the queued prompt.
	teamMessages *MessageInbox

	// snapshotCapture, when non-nil, captures pre-edit file content
	// keyed by currentUserUUID (P20.57). Set by callers wiring
	// `biu --rewind-files`; nil disables the capture path.
	snapshotCapture func(uuid, path string) error
	// currentUserUUID is the in-flight user message's UUID. Updated
	// by Submit at turn start so file-mutating tools snapshot under
	// the right key.
	currentUserUUID string

	// Idempotency for SessionStart / SessionEnd hook firing. Allows
	// tests to call FireSessionStart / Close without fearing repeat
	// invocations spawning duplicate shell hooks.
	sessionStartOnce sync.Once
	sessionEndOnce   sync.Once

	// Inflight guard.
	inflight sync.Mutex
}

// PlanVerifier is the small surface the engine consumes from
// internal/planverify. Defined here (not imported) to keep the
// dependency direction one-way.
type PlanVerifier interface {
	HasPlan() bool
	Observe(tool string, args map[string]any) bool
	DriftCount() int
	BuildAttachment() string
	Reset()
	SetPlan(plan string)
}

// New constructs a QueryEngine. Fails if required fields are missing.
func New(opt Options) (*QueryEngine, error) {
	if opt.State == nil {
		return nil, errors.New("engine: State required")
	}
	if opt.Tools == nil {
		return nil, errors.New("engine: Tools required")
	}
	if opt.Provider == nil {
		return nil, errors.New("engine: Provider required")
	}
	if opt.Model == "" {
		return nil, errors.New("engine: Model required")
	}
	maxTurns := opt.MaxToolTurns
	if maxTurns == 0 {
		maxTurns = 25
	}
	maxTokens := opt.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	perms := opt.Permissions
	if perms == nil {
		perms = permissions.NewContext()
	}
	if opt.BypassPermissions {
		perms.SetMode(permissions.ModeBypass)
	}
	tracker := opt.Cost
	if tracker == nil {
		tracker = cost.NewTracker(opt.Model)
	}
	// Build the auto-compact runner unless the caller opted out.
	// Plan attachment closure: lets the compactor pull the most
	// recent ExitPlanMode payload from the permission context at
	// compact time.
	//
	// Threshold derivation: 0 → compact.AutoCompactThreshold(model);
	// positive → explicit override (legacy callers + tests);
	// negative → disable auto-compact (manual /compact still works).
	var compactor *compact.Auto
	if opt.CompactMaxTokens >= 0 && compact.IsAutoCompactEnabled() {
		maxTokens := opt.CompactMaxTokens
		if maxTokens == 0 {
			maxTokens = compact.AutoCompactThreshold(opt.Model)
		}
		stateRef := opt.State
		compactor = compact.New(compact.Options{
			Provider:    newEngineSummariser(opt.Provider, opt.Model),
			MaxTokens:   maxTokens,
			Instruction: opt.CompactInstruction,
			Attachments: func() []state.Message {
				var out []state.Message

				// Plan attachment (existing): re-inject the active
				// ExitPlanMode plan so post-compact tool calls keep
				// honouring it.
				if perms != nil {
					if body := perms.PlanAttachment(); body != "" {
						out = append(out, state.Message{
							Role: state.RoleSystem,
							Content: []state.ContentBlock{{
								Type: state.ContentText,
								Text: "Plan you committed to before this compaction. " +
									"Continue honouring it as you implement.\n\n" +
									"<approved-plan>\n" + body + "\n</approved-plan>",
							}},
						})
					}
				}

				// File attachments (MC5): the model was working on
				// a set of files pre-compact. Their summaries
				// mention the files but not the current contents;
				// re-attaching avoids forcing an immediate Read on
				// each one and prevents the model from operating
				// on stale recall. Capped at MaxFileAttachmentCount
				// + 32 KiB per file so the post-compact prefix
				// stays tractable.
				if stateRef != nil {
					out = append(out,
						compact.FileAttachmentsAsMessages(
							compact.BuildFileAttachments(stateRef),
						)...)
				}
				return out
			},
		})
	}
	return &QueryEngine{
		state:        opt.State,
		tools:        opt.Tools,
		provider:     opt.Provider,
		model:        opt.Model,
		system:       opt.System,
		cwd:          opt.Cwd,
		maxToolTurns: maxTurns,
		maxTokens:    maxTokens,
		perms:        perms,
		hooks:        opt.Hooks,
		cost:         tracker,
		compact:      compactor,
		sessionMem:   opt.SessionMemory,
		compactWarn: func() *compact.WarningState {
			if compactor == nil {
				return nil
			}
			// Use the same threshold the compactor was built against
			// so warnings calibrate against the actual firing line —
			// not the raw opt value, which could be 0 when the
			// caller wants the model-derived default.
			budget := opt.CompactMaxTokens
			if budget == 0 {
				budget = compact.AutoCompactThreshold(opt.Model)
			}
			if budget <= 0 {
				return nil
			}
			return compact.NewWarningState(compact.WarningOptions{
				MaxTokens: budget,
			})
		}(),
		usageLogger:        opt.UsageLogger,
		fileChanged:        opt.FileChanged,
		agentID:            opt.AgentID,
		parentToolUseID:    opt.ParentToolUseID,
		planVerifier:       opt.PlanVerifier,
		planDriftThreshold: planDriftThreshold(opt.PlanDriftThreshold),
		planHinter:         opt.PlanHinter,
		bgTaskNotifier:     opt.BgTaskNotifier,
		selections:         NewDeferredSelection(),
		asyncAgents:        NewAsyncAgentStore(),
		teams:              firstNonNilTeams(opt.Teams, NewTeamRegistry()),
		teamMessages:       firstNonNilMessages(opt.TeamMessages, NewMessageInbox()),
	}, nil
}

func firstNonNilTeams(in *TeamRegistry, fallback *TeamRegistry) *TeamRegistry {
	if in != nil {
		return in
	}
	return fallback
}

func firstNonNilMessages(in *MessageInbox, fallback *MessageInbox) *MessageInbox {
	if in != nil {
		return in
	}
	return fallback
}

// SetSnapshotCapture wires the file-history capture callback
// (P20.57). Pass a closure backed by *session.SnapshotStore.Capture
// or similar. nil clears the wiring.
func (e *QueryEngine) SetSnapshotCapture(cb func(uuid, path string) error) {
	if e == nil {
		return
	}
	e.snapshotCapture = cb
}

// SetCurrentUserUUID is called by the user-prompt machinery before
// each turn so file-mutating tools snapshot under the right key.
// Empty string disables snapshotting for that turn (e.g. internal
// re-submits with no addressable user message).
func (e *QueryEngine) SetCurrentUserUUID(uuid string) {
	if e == nil {
		return
	}
	e.currentUserUUID = uuid
}

// AsyncAgents returns the live store so callers (REPL diagnostic
// commands, swarm tools that want to introspect the active set)
// can inspect what teammates are running. nil-safe.
func (e *QueryEngine) AsyncAgents() AsyncAgentStore {
	if e == nil {
		return nil
	}
	return e.asyncAgents
}

// Teams returns the named-team registry for this engine. nil-safe.
func (e *QueryEngine) Teams() *TeamRegistry {
	if e == nil {
		return nil
	}
	return e.teams
}

// TeamMessages returns the follow-up message inbox keyed by handle
// id. SendMessageTool enqueues via this surface. nil-safe.
func (e *QueryEngine) TeamMessages() *MessageInbox {
	if e == nil {
		return nil
	}
	return e.teamMessages
}

// Cost exposes the session-wide cost tracker so callers (statusbar,
// /cost slash) can read and format it.
func (e *QueryEngine) Cost() *cost.Tracker { return e.cost }

// PlanVerifier exposes the verifier (when wired) for callers like the
// REPL `/plan diff` slash that need to inspect drift state without
// going through tool dispatch. Nil-safe.
func (e *QueryEngine) PlanVerifier() PlanVerifier { return e.planVerifier }

// State exposes the underlying AppState (read-only callers: REPL
// status bar, /todo slash, etc).
func (e *QueryEngine) State() *state.AppState { return e.state }

// Cwd returns the engine's current working directory. Tools that need
// to switch cwd (EnterWorktree) read this through CwdSwitcher.
func (e *QueryEngine) Cwd() string { return e.cwd }

// SetCwd changes the engine's working directory. Used by worktree
// tools; not goroutine-safe with concurrent Submit (worktree changes
// expect to happen between turns).
//
// Fires CwdChanged hook (P20.55) on actual change so observers can
// audit working-directory transitions across a session.
//
// R6.4 / D7 cwd-drift floor: when a remote-device floor is active (the
// permission Context has an OriginalCwd anchor + allowed roots, set via
// biumindkit Options.AllowedRoots), a switch to a path OUTSIDE the roots
// is REJECTED with an error and e.cwd is left unchanged. This closes the
// gap where Bash runs with cmd.Dir=e.cwd: without this, a worktree switch
// to a path outside the roots would let `bash -c "cat ./secret"` (relative)
// escape the floor, since Bash is capability-gated, not path-gated, and
// its shell-string paths aren't tool args the path gate can see. No floor
// (normal REPL/chat, OriginalCwd=="") → any switch allowed (zero change).
func (e *QueryEngine) SetCwd(p string) error {
	if e == nil {
		return nil
	}
	if e.cwd == p {
		return nil
	}
	if e.perms != nil {
		if anchor := e.perms.OriginalCwd(); anchor != "" &&
			!permissions.PathInAllowedWorkingPath(p, e.perms, anchor) {
			return fmt.Errorf("cwd %q is outside the allowed working roots", p)
		}
	}
	from := e.cwd
	e.cwd = p
	if e.hooks == nil || !e.hooks.Has(hooks.EventCwdChanged) {
		return nil
	}
	hooks.Run(context.Background(),
		e.hooks.For(hooks.EventCwdChanged, ""),
		hooks.EventCwdChanged,
		map[string]any{
			"session_id": e.agentID,
			"from":       from,
			"to":         p,
		})
	return nil
}

// SetSystem swaps the engine's system prompt. Used by /output-style
// to weave a style instruction in between turns.
func (e *QueryEngine) SetSystem(s string) { e.system = s }

// System returns the active system prompt (read-only callers).
func (e *QueryEngine) System() string { return e.system }

// SystemForTurn returns the system prompt the next provider call
// should send. Rebuilt every turn so that:
//
//   - /add-dir / settings reload: the model sees the latest set of
//     working directories without an engine restart.
//   - cwd correctness: the originalCwd is the same one used for
//     permission checks (state.OriginalCwd), not whatever Bash has
//     `cd`'d to mid-session.
func (e *QueryEngine) SystemForTurn() string {
	base := e.system
	suffix := e.workingDirsBlock()
	if suffix == "" {
		return base
	}
	if base == "" {
		return suffix
	}
	return base + "\n\n" + suffix
}

// workingDirsBlock formats the current working set as a system-
// prompt line. Empty string when there are no extras AND no cwd —
// in that case the model already gets cwd via the env block.
func (e *QueryEngine) workingDirsBlock() string {
	originalCwd := ""
	if e.state != nil {
		originalCwd = e.state.OriginalCwd
	}
	if originalCwd == "" {
		originalCwd = e.cwd
	}
	dirs := permissions.AllWorkingDirectories(e.perms, originalCwd)
	if len(dirs) == 0 {
		return ""
	}
	if len(dirs) == 1 {
		// Just cwd — already in env block; suppress redundancy.
		return ""
	}
	var b strings.Builder
	b.WriteString("Working directories (you may read and write within any of these):\n")
	for i, d := range dirs {
		if i == 0 {
			b.WriteString("  - ")
			b.WriteString(d)
			b.WriteString("  (current)\n")
			continue
		}
		b.WriteString("  - ")
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return b.String()
}

// Permissions exposes the underlying permission context so callers
// (REPL slash command, headless flags) can swap the active mode or
// inject session grants at runtime.
func (e *QueryEngine) Permissions() *permissions.Context { return e.perms }

// Hooks exposes the engine's hook registry — used by /hooks slash
// + tests that want to inspect registered hook entries without
// driving a full turn through the runner.
func (e *QueryEngine) Hooks() *hooks.Registry { return e.hooks }

// FireSessionStart runs the SessionStart hook chain in the
// background. Idempotent — wraps a sync.Once so repeated calls are
// no-ops.
//
// We don't fire from New() because tests construct ephemeral engines
// continuously and don't want to spawn user shell hooks on every
// fixture; main / SDK call this explicitly when they're ready to
// announce a real session.
//
// The hook runs on a fresh background context (decoupled from the
// caller's ctx) so a long-running SessionStart hook doesn't block the
// REPL launch. Errors are surfaced via the standard hook channel
// (stderr from the shell command).
func (e *QueryEngine) FireSessionStart() {
	e.sessionStartOnce.Do(func() {
		if e.hooks == nil || !e.hooks.Has(hooks.EventSessionStart) {
			return
		}
		ctx := context.Background()
		hooks.Run(ctx,
			e.hooks.For(hooks.EventSessionStart, ""),
			hooks.EventSessionStart,
			map[string]any{
				"session_id": e.agentID,
				"cwd":        e.cwd,
				"model":      e.model,
			})
	})
}

// Close fires the SessionEnd hook chain (idempotent via sync.Once)
// and releases any resources the engine still owns. Callers that
// need a clean shutdown — main / SDK on Ctrl-C — invoke this; tests
// can ignore it.
//
// Returns nil today; the signature reserves space for cleanup
// errors (e.g. sub-agent reaper drains) without breaking callers.
func (e *QueryEngine) Close() error {
	e.sessionEndOnce.Do(func() {
		if e.hooks == nil || !e.hooks.Has(hooks.EventSessionEnd) {
			return
		}
		ctx := context.Background()
		hooks.Run(ctx,
			e.hooks.For(hooks.EventSessionEnd, ""),
			hooks.EventSessionEnd,
			map[string]any{
				"session_id": e.agentID,
				"cwd":        e.cwd,
				"model":      e.model,
			})
	})
	return nil
}

// Submit kicks off a single turn cycle (which may contain many tool
// loops internally). Returns immediately with a channel; caller drains
// until close. Channel close = end of this Submit, regardless of
// success / error / cancellation.
//
// 等价于 SubmitContent(ctx, prompt, nil) — 老调用方保持二进制兼容。
// 多模态(图片附件)走 SubmitContent。
func (e *QueryEngine) Submit(ctx context.Context, prompt string) <-chan Event {
	return e.SubmitContent(ctx, prompt, nil)
}

// SubmitContent 跟 Submit 一样起一个 turn,但允许在 user message 里附加
// 文本之外的 ContentBlock(目前只用 ContentImage,未来可扩 file/audio)。
// attachments 顺序保留;turn.go 在写 user message 时拼到 prompt text 之后。
//
// hooks 的 prompt 仍以 string 形态投递,attachments 不进 hook payload —
// UserPromptSubmit 的 ReplacePrompt 只重写文本部分,图片不动。
func (e *QueryEngine) SubmitContent(
	ctx context.Context,
	prompt string,
	attachments []state.ContentBlock,
) <-chan Event {
	out := make(chan Event, 64)

	// Block re-entry. Releasing this lock is the responsibility of
	// the goroutine after it closes the channel.
	if !e.inflight.TryLock() {
		go func() {
			ev := &ErrorEvent{
				Err: ErrConcurrentSubmit, Source: ErrSrcInternal,
				Recoverable: false,
			}
			fillBase(ev, e.agentID, e.parentToolUseID)
			out <- ev
			close(out)
		}()
		return out
	}

	// Two-stage pipeline:
	//   runSubmit → inner → forwarder (stamps metadata) → out → caller
	//
	// Stamping happens at the boundary so internal SafeSend call sites
	// (turn.go / stream.go / runner.go / compact_run.go) don't have to
	// know about session/parent ids. The forwarder is a tiny goroutine
	// that reads inner and writes the same Event with EventSessionID +
	// EventParentToolUseID filled in. Both channels are buffered so
	// runSubmit doesn't block on the forwarder doing its bookkeeping.
	//
	// Forwarder DOES NOT gate on ctx.Done() — by contract, the receiver
	// drains `out` until it closes (biumindkit / bridge / tests all
	// follow this). Gating would drop legitimate post-cancel events
	// (e.g. F5's synthetic DoneEvent{StopReason:"interrupted"} that
	// fires AFTER ctx is canceled). With a 64-slot buffer + the reader
	// always draining, blocking sends here are well-defined.
	inner := make(chan Event, 64)
	go func() {
		defer e.inflight.Unlock()
		defer close(out)
		for ev := range inner {
			fillBase(ev, e.agentID, e.parentToolUseID)
			out <- ev
		}
	}()
	go func() {
		defer close(inner)
		e.runSubmit(ctx, inner, prompt, attachments)
	}()
	return out
}
