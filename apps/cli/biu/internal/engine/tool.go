// Engine-side Tool abstraction.
//
// We deliberately don't import the existing internal/tools package
// from engine. Two reasons:
//
//   1. Import cycle risk:tools/ will eventually want to render
//      Event-shaped progress, which imports engine.
//   2. Migration runway: the legacy tools.Tool interface returns a
//      plain `string`. Real tools need typed ContentBlock results,
//      soft errors, newMessages injection. We
//      define the new shape here and bridge legacy tools as adapters
//      in tools/registry_adapter.go (later phase).
//
// Anything that wants to be invocable by the engine implements `Tool`
// here. ToolRegistry is a tiny lookup interface so the engine doesn't
// care whether tools come from builtin / MCP / Skills.

package engine

import (
	"context"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// Tool is the engine-facing tool contract:
//
//   - declarative metadata (IsReadOnly / IsConcurrencySafe /
//     IsDestructive) so the batch scheduler can group calls correctly
//   - Call returns a typed result, plus may inject side-effect
//     messages (used by AgentTool / TaskCreateTool to dump child
//     output back into the conversation)
type Tool interface {
	Name() string
	Description(input map[string]any) string
	InputSchema() map[string]any

	// Behaviour metadata — input-dependent because the *same* tool
	// (e.g. Bash) is read-only for `git status` and destructive for
	// `rm -rf`. Implementations parse args to decide.
	IsReadOnly(input map[string]any) bool
	IsDestructive(input map[string]any) bool
	IsConcurrencySafe(input map[string]any) bool

	// InterruptBehavior tells the engine what to do when the user
	// hits Ctrl-C while this tool is running:
	//
	//   "cancel" — propagate cancellation, drop tool result
	//   "block"  — let the tool finish; user must ctrl-c twice
	//
	// Default ("") = "block" (defensive — most tools shouldn't be
	// interrupted mid-write).
	InterruptBehavior() string

	// Call is the actual execution. Implementations should respect
	// ctx cancellation and call env.OnProgress periodically for any
	// long-running work.
	Call(ctx context.Context, input map[string]any, env *ToolEnv) (*ToolResultPayload, error)
}

// Warner is an optional sub-interface a Tool can implement to
// surface human-readable warnings into the permission ask dialog.
// Used today by BashTool to flag destructive commands (rm -rf,
// git reset --hard, terraform destroy, …) so the user sees the cost
// of approval, not just the command. The runner type-asserts on this
// interface — tools that don't implement it pay zero cost.
//
// Warnings are advisory; they do NOT change the permission decision.
// The Allow/Deny/Ask outcome is still entirely up to permissions.Decide.
type Warner interface {
	// Warnings returns zero or more short notes (no leading prefix,
	// no trailing newline). Empty slice / nil is the common case.
	Warnings(input map[string]any) []string
}

// ToolEnv is the per-call execution context. Engine constructs one
// per tool invocation.
type ToolEnv struct {
	AppState   *state.AppState
	OnProgress func(ProgressData)

	// Set when the call is happening inside a sub-agent (AgentTool),
	// empty for the top-level engine. Tools can use this to scope
	// operations (e.g. don't write to the project's main MEMORY.md
	// from inside a sub-agent's session).
	AgentID string

	// ToolUseID is the tool_use id the LLM assigned to *this* call. The
	// engine fills it before each Tool.Call so spawning tools (AgentTool /
	// AgentBackground) can pass it as ParentToolUseID into their
	// AgentSpawnRequest — that way every event the sub-agent emits
	// links back to the AgentTool invocation that started it. Empty for
	// out-of-band callers; tools that just need to know "what's my id"
	// for logging are fine to read it.
	ToolUseID string

	// Cwd is the working directory the engine considers "the project
	// root". File-based tools resolve relative paths against it.
	Cwd string

	// Spawner lets a tool create a nested sub-agent run (used by
	// AgentTool). Nil when sub-agent dispatch isn't available.
	Spawner AgentSpawner

	// AskUser, when non-nil, lets a tool ask the user a multi-choice
	// question and block until the UI returns an answer. AskUserQuestion
	// is the canonical caller. Nil = the tool soft-errors (e.g. headless
	// runs where there's no human to ask).
	AskUser func(ctx context.Context, q UserQuestion) (UserAnswer, error)

	// FileChanged, when non-nil, is called by file-mutating tools
	// (Edit / Write / MultiEdit / NotebookEdit) right after they
	// commit a change to disk. The engine wires this to the LSP
	// pool so language servers see incremental updates instead of
	// stale didOpen content. Empty path or nil callback ⇒ no-op.
	FileChanged func(absPath string)

	// Selections is the deferred-tool unlock set for this run. The
	// ToolSearchTool calls Selections.Add(name) on a successful
	// match; the engine consults Selections.Has(name) when building
	// the next turn's wire-level tool catalog. nil = deferred tools
	// stay hidden (Add is a no-op, Has returns false).
	Selections *DeferredSelection

	// FireHook lets a tool emit a user-defined hook event without
	// importing the hooks package directly (tool.go can't, due to
	// the depended-from-everywhere position of the engine package).
	// nil-safe — tools should always check before calling. Used by
	// TaskCreate / TaskUpdate to fire TaskCreated / TaskCompleted
	// (P20.55) without taking on a registry dependency.
	FireHook func(event string, payload map[string]any)

	// SnapshotFile is the file-history capture callback (P20.57).
	// File-mutating tools (Edit / Write / MultiEdit / NotebookEdit)
	// call this with the absolute path BEFORE writing — the engine
	// captures pre-edit content keyed by the current user message
	// UUID so `biu --rewind-files <uuid>` can restore later. nil-safe.
	SnapshotFile func(absPath string) error
}

// UserQuestion is the structured prompt rendered by the REPL when a
// tool needs the user to make a choice.
type UserQuestion struct {
	Question    string
	Header      string // short label / chip displayed
	Options     []UserOption
	MultiSelect bool
}

// UserOption is one choice in a UserQuestion. Description gives the
// "what does this do" sub-line; Preview is a side-by-side mockup the
// REPL can render in monospace.
type UserOption struct {
	Label       string
	Description string
	Preview     string
}

// UserAnswer is what the REPL writes back. Selected is the indices
// the user picked (single value for non-multi). Notes captures any
// free-form text the user typed via the "Other" path or an inline
// annotation. Cancelled is true when the user dismissed the panel
// (Esc/q) without making a selection — the tool short-circuits to a
// soft error so the model knows.
type UserAnswer struct {
	Selected  []int
	Notes     string
	Cancelled bool
}

// AgentSpawner launches a nested QueryEngine for a sub-agent run.
// The contract is synchronous — the caller blocks until the sub-agent
// terminates, then receives a single Result.
type AgentSpawner interface {
	Spawn(ctx context.Context, req AgentSpawnRequest) (*AgentSpawnResult, error)
}

// AgentSpawnRequest configures one sub-agent invocation.
type AgentSpawnRequest struct {
	AgentType   string // friendly label ("Explore", "general-purpose")
	Description string // one-line summary, telemetry only
	Prompt      string // prompt for the sub-agent
	System      string // optional system prompt override

	// Model overrides the parent's model when non-empty. "inherit"
	// is treated as empty by the spawner.
	Model string

	// MaxTurns caps the tool-loop budget; 0 = inherit parent.
	MaxTurns int

	// PermissionMode pins the permission mode for this run; empty =
	// inherit. Useful for read-only Explore agents that want to
	// force `plan` mode regardless of parent state.
	PermissionMode string

	// AllowedTools restricts the tool catalog. Empty = no restriction.
	AllowedTools []string

	// DisallowedTools subtracts from the catalog. Applied AFTER
	// AllowedTools (so tools in both are excluded).
	DisallowedTools []string

	// ParentToolUseID is the outer AgentTool tool_use_id this sub-agent
	// is being spawned to handle. The spawner stamps it onto the new
	// QueryEngine so every event the sub-agent emits carries the
	// parent's id — clients can then draw the call tree (outer
	// AgentTool → inner Read/Bash) without out-of-band correlation.
	// Empty = top-level / can't determine; the resulting events will
	// have empty ParentToolUseID(). Filled automatically by AgentTool /
	// AgentBackground from the runner's tool_use id.
	ParentToolUseID string
}

// AgentSpawnResult carries what the parent agent sees: the assistant's
// final text + stop reason + token usage.
type AgentSpawnResult struct {
	Output       string
	StopReason   string
	Elapsed      time.Duration
	InputTokens  int
	OutputTokens int
}

// ToolRegistry is the lookup surface. Engine grabs tools by name; it
// doesn't care whether they came from builtin code, MCP, or skill
// auto-load.
type ToolRegistry interface {
	Get(name string) (Tool, bool)
	List() []Tool
}

// SimpleRegistry is a stock map-backed implementation, sufficient for
// the engine's internal tests. Real bootstrap uses the same shape but
// with hooks for hot-reload (skill add / mcp re-list).
type SimpleRegistry struct {
	m map[string]Tool
}

func NewRegistry() *SimpleRegistry {
	return &SimpleRegistry{m: map[string]Tool{}}
}

func (r *SimpleRegistry) Register(t Tool) { r.m[t.Name()] = t }

func (r *SimpleRegistry) Get(name string) (Tool, bool) {
	t, ok := r.m[name]
	return t, ok
}

func (r *SimpleRegistry) List() []Tool {
	out := make([]Tool, 0, len(r.m))
	for _, t := range r.m {
		out = append(out, t)
	}
	return out
}
