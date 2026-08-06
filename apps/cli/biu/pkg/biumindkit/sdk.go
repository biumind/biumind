// Package biumindkit is the public Go SDK for embedding the biu
// agent loop into other applications — IDE integrations, custom CLIs,
// CI runners, internal tools.
//
// # API stability
//
// Everything exported from this package is part of the supported
// surface. We follow standard Go semver: minor versions add features
// without breaking existing callers; major versions are reserved for
// breaking changes and announced in CHANGELOG.md. Internal packages
// (anything under `internal/`) are off-limits — they may change
// between any two commits.
//
// # Two ways to drive the agent
//
//   - [Agent.Run]    — blocking convenience that returns the final
//     assistant text and stop reason. Right for CI jobs, scripts,
//     server endpoints that need a single answer.
//   - [Agent.Submit] — streaming channel that emits every event as
//     it happens. Right for IDE plugins, progress UIs, anything that
//     wants tool-call visibility.
//
// # Quick start
//
//   ag, err := biumindkit.New(biumindkit.Options{
//       APIKey: os.Getenv("ANTHROPIC_API_KEY"),
//       Model:  "claude-sonnet-4-6",
//       Cwd:    ".",
//   })
//   if err != nil { return err }
//   defer ag.Close()
//
//   text, _, err := ag.Run(ctx, "list every TODO in this repo")
//   if err != nil { return err }
//   fmt.Println(text)
//
// More patterns — streaming, custom tools, custom permission
// policies — live under `pkg/biumindkit/examples/` in the repo, and
// as `Example*` functions you can browse on pkg.go.dev.
//
// # Permissions
//
// By default the SDK denies every "ask"-tier tool call so headless
// runs never hang waiting on a non-existent TTY. Override with
// [PermissionAllow], [PermissionDeny], [PermissionAlways], or supply
// a custom [PermissionPolicyFn] that routes to your own UI / queue.

package biumindkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/adapters"
	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/planhint"
	"github.com/biumind/biumind/apps/cli/biu/internal/planverify"
	"github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/files"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/interactive"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
	webtools "github.com/biumind/biumind/apps/cli/biu/internal/tools/web"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
)

// Options configures a new Agent. APIKey + Model are required;
// everything else has sensible defaults.
type Options struct {
	// APIKey carries either:
	//   - the Anthropic API key (direct mode, default)
	//   - the BiuMind model-relay bearer token (when UseRelayAuth=true)
	APIKey string

	// AnthropicEndpoint overrides the default api.anthropic.com base
	// URL (direct mode), or specifies the BiuMind model-relay URL when
	// UseRelayAuth=true (e.g. "http://localhost:7001"). The /v1/messages
	// suffix is appended by the engine provider.
	AnthropicEndpoint string

	// UseRelayAuth flips the LLM provider from direct-to-Anthropic
	// (x-api-key) to BiuMind model-relay (Bearer auth on the same Anthropic
	// Messages API shape). Set this when the SDK runs inside a process
	// that should respect a user's model-relay identity / quota / billing
	// rather than burning a local Anthropic key.
	//
	// model-relay forwards /v1/messages verbatim to Anthropic so tool blocks,
	// streaming, and prompt-cache markers all work unchanged.
	UseRelayAuth bool

	// Provider, when non-nil, overrides the default Anthropic/relay
	// engine provider (e.g. client.NewOpenAIEngine for OpenAI-compat
	// upstreams, or any custom engine.Provider). When set,
	// APIKey/AnthropicEndpoint/UseRelayAuth/ExtraHeaders are ignored —
	// the caller owns provider configuration entirely.
	Provider engine.Provider

	// Model selects the LLM. Default: claude-sonnet-4-6.
	Model string

	// Cwd is the project root the agent considers its workspace.
	// File-based tools resolve relative paths against it. Defaults to
	// the process working directory.
	Cwd string

	// ExtraHeaders are stamped on every /v1/messages HTTP request
	// (after the standard content-type/auth headers). Daemon Agent
	// mode uses this to pass `X-Biumind-LLM-Key` / `X-Biumind-LLM-Base-Url`
	// so model-relay routes the user's per-thread BYOK provider
	// credentials. Empty in chat / direct CLI runs.
	ExtraHeaders map[string]string

	// System overrides / extends the default system prompt.
	System string

	// MaxToolTurns caps how many tool loops the agent runs before
	// giving up. 0 = engine default (25).
	MaxToolTurns int

	// MaxTokens forwarded to the provider per request. 0 = 4096.
	MaxTokens int

	// LoadProjectMemory toggles BIUMIND.md ingestion. Default: on.
	// Set to NoMemory to skip it (e.g. for headless tests).
	LoadProjectMemory MemoryMode

	// LoadProjectSettings toggles ~/.biumind + project settings.json
	// loading (permissions / hooks). Default: on.
	LoadProjectSettings SettingsMode

	// PermissionMode pins the active permission mode. Empty = use
	// settings.json defaultMode or fall back to "default".
	PermissionMode string

	// BypassPermissions disables the ask flow entirely. Equivalent to
	// PermissionMode="bypassPermissions".
	//
	// Ignored when ToolFloor != nil: a hard floor and a global bypass are
	// contradictory, and the bypass short-circuits Decide ahead of the
	// floor's deny rules (policy.go step 1). New() force-clears it then.
	BypassPermissions bool

	// AllowedRoots (Runtime v3 R6.3 / D7) pins the filesystem boundary
	// for this agent. When non-empty, roots[0] seeds the permission
	// Context's OriginalCwd and all roots are registered as working
	// directories — which *activates* the working-dir gate in
	// permissions.Decide (otherwise fail-open in headless/daemon runs,
	// letting a read-only Read of ~/.ssh slip through auto-allow). Paths
	// outside every root then resolve to Ask; the daemon's floorPolicy
	// wrapper upgrades that to a hard Deny. Empty = no path floor (today's
	// behavior). Remote-device daemon sets this from --allowed-roots.
	AllowedRoots []string

	// ToolFloor (Runtime v3 R6.3 / D7), when non-nil, is a hard
	// capability floor: the dangerous tools (file-mutating / shell /
	// sub-agent) NOT in its allowlist get Context deny rules, which win
	// in Decide ahead of acceptEdits auto-allow and read-only auto-allow.
	// Safe tools (read, search, plan, todo…) are unaffected — path access
	// is bounded by AllowedRoots instead. nil = no capability floor.
	ToolFloor *ToolFloor

	// ExtraTools adds caller-supplied tools to the agent's catalog
	// after the built-ins (last-write-wins on name conflict).
	// Built with NewTool / ToolDef; see tool.go.
	ExtraTools []Tool

	// ExecHost (Runtime v3 轴 B) 决定内置 Bash 工具往哪落地执行：nil →
	// 本机 local（今天的行为）；exechost.For("cloud"/"none") → 分流（cloud
	// 当前 stub，none 拒绝）。daemon 按 WorkPayload.RuntimeEnvMode 设置。
	ExecHost exechost.Host

	// PermissionPolicy decides what to do when the agent needs to
	// run a destructive tool and the rules don't pre-approve it.
	// Default (nil) auto-denies in headless / SDK contexts so the
	// turn fails loud rather than hanging waiting for a TTY user.
	//
	// Set to PermissionAllow / PermissionDeny / PermissionAlways for
	// the canned policies, or supply a custom function for stdin /
	// queue-based interaction.
	PermissionPolicy PermissionPolicyFn

	// PermissionPolicyExt is the suggestion-aware variant. Wins over
	// PermissionPolicy when both are configured. Supply this when
	// your callback wants to act on PermissionRequest.Suggestions
	// (e.g. headless GUI host that lets the user pick "Allow + add
	// dir to working dirs").
	PermissionPolicyExt PermissionPolicyExtFn

	// MCPRegistry, when non-nil, has its connected servers' tools +
	// resource / prompt wrappers registered into the agent's tool
	// catalog with the standard `mcp__<server>__<tool>` prefix.
	//
	// Type is the public *MCPRegistry wrapper (biumindkit.NewMCPRegistry)
	// — embedders don't import internal/mcp directly. Brain / CLI build
	// the inner registry via wiring.BootstrapMCP and pass it through
	// NewMCPRegistry before assigning here.
	//
	// Caller responsibilities:
	//   - bootstrap the registry BEFORE calling New (typical: use
	//     cmd/biu/wiring.BootstrapMCP) so health monitoring + server
	//     handshakes are already running by the time the engine
	//     starts.
	//   - close the registry when the last Agent that uses it has
	//     been Closed; biumindkit doesn't take ownership.
	MCPRegistry *MCPRegistry

	// PriorMessages seeds the engine's conversation history before
	// Submit runs. Each entry becomes a state.Message (user / assistant /
	// tool_result) so the LLM sees the full prior turn context.
	//
	// Brain S4 uses this to inject thread history (rebuilt from the
	// thread_blocks DB) so the model can answer follow-ups; CLI bridge
	// uses it to resume an existing session after process restart.
	//
	// Empty / nil = fresh session, no history.
	PriorMessages []Message

	// SessionID is stamped onto every Event the agent emits as
	// SessionID(). Embedders typically pass their thread / session uuid
	// here so SDK Protocol frames inherit a stable id without per-call
	// plumbing. Empty = events carry empty SessionID and the bridge
	// layer's caller-side fallback fills it.
	SessionID string

	// ParentToolUseID is stamped onto every Event as ParentToolUseID()
	// — non-empty only when this Agent was created as a sub-agent
	// inside another Agent (e.g. brain spawning a per-thread agent
	// from inside a BackgroundAgent runner). The engine spawner sets
	// this automatically for AgentTool sub-agents; embedders only need
	// to set it when wiring custom multi-agent topologies that don't
	// go through AgentTool.
	ParentToolUseID string
}

// floorDangerousTools is the bounded universe of side-effecting tools
// the capability floor gates: file-mutating + shell + sub-agent spawn.
// A preset that excludes any of these gets a Context deny rule for it.
// Safe tools (Read/Glob/Grep/Web*/plan/todo/ask) are never capability-
// gated — their reach is bounded by AllowedRoots (path floor) instead.
// Keep in sync with tool registration (internal/tools/{files,web,
// orchestration}/register.go) when adding a side-effecting tool.
var floorDangerousTools = []string{
	// file-mutating (canonical + lowercase aliases)
	"Edit", "edit", "Write", "write", "MultiEdit", "NotebookEdit",
	// shell
	"Bash", "BashOutput", "KillBash",
	// sub-agent spawn (can escape the parent's tool set)
	"Agent", "AgentBackground",
}

// IsFloorDangerousTool reports whether name is one of the side-effecting
// tools the capability floor gates. The daemon's floorPolicy wrapper uses
// this so it only capability-denies dangerous tools (safe tools like
// Read/WebSearch pass — their reach is bounded by AllowedRoots instead).
func IsFloorDangerousTool(name string) bool {
	for _, t := range floorDangerousTools {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// ToolFloor is a hard capability allowlist (Runtime v3 R6.3 / D7). Tools
// in floorDangerousTools that are NOT allowed become Context deny rules.
// Build it from a preset via the daemon's agentplane layer; biumindkit
// only enforces a resolved floor.
type ToolFloor struct {
	// AllowedTools is the set of permitted tool names (case-insensitive).
	// Only the dangerous tools matter; listing safe tools is harmless.
	AllowedTools map[string]struct{}
}

// Allows reports whether name is permitted (case-folded). A nil floor
// allows everything (caller should treat nil ToolFloor as "no floor").
func (f *ToolFloor) Allows(name string) bool {
	if f == nil {
		return true
	}
	for a := range f.AllowedTools {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// deniedDangerous returns the dangerous tools this floor forbids — the
// complement (within floorDangerousTools) of AllowedTools. These become
// Context deny rules in New.
func (f *ToolFloor) deniedDangerous() []string {
	if f == nil {
		return nil
	}
	var out []string
	for _, t := range floorDangerousTools {
		if !f.Allows(t) {
			out = append(out, t)
		}
	}
	return out
}

// PermissionDecision is what a PermissionPolicyFn returns.
type PermissionDecision int

const (
	// PermDeny soft-rejects the call. The model sees a tool_result
	// with `denied by user policy` and can adapt.
	PermDeny PermissionDecision = iota
	// PermAllow approves this single call.
	PermAllow
	// PermAlways approves and remembers the choice for the rest of
	// the session (mirrors REPL "shift+a").
	PermAlways
)

// PermissionPolicyFn receives the pending tool call and returns a
// decision. ctx is the agent's request context so policies can
// honour cancellation. Implementations must return promptly — any
// blocking call holds up the whole turn.
//
// For richer responses (selecting a suggestion that widens the
// runtime ctx, e.g. adding a working directory in one shot), use
// [PermissionPolicyExtFn] instead. PermissionPolicyExtFn always
// wins when both are configured on an Agent.
type PermissionPolicyFn func(ctx context.Context, req PermissionRequest) PermissionDecision

// PermissionPolicyExtFn is the richer variant — receives the same
// PermissionRequest plus its Suggestions, and returns a full
// PermissionResponse. Lets headless / GUI policies do the equivalent
// of the REPL's "[w] Allow + add dir to working dirs" hotkey:
// approve the call AND fold the suggestion's update into the ctx.
type PermissionPolicyExtFn func(ctx context.Context, req PermissionRequest) PermissionResponse

// PermissionRequest is what the policy callback sees.
//
// Suggestions, when non-empty, are pre-computed shortcuts the runner
// thinks would be useful (e.g. adding the parent directory of a
// rejected path to working dirs). The basic PermissionPolicyFn
// always ignores them; PermissionPolicyExtFn returns
// SelectedSuggestion to pick one. UI-driven policies (REPL,
// stdin-json) render Suggestions for the user to choose from.
type PermissionRequest struct {
	ToolUseID   string
	ToolName    string
	Input       map[string]any
	Reason      string // e.g. "destructive operation", "first use of <tool>"
	Suggestions []PermissionSuggestion
}

// PermissionSuggestion is one shortcut the policy may select. Mirrors
// engine.AskSuggestion but uses an opaque payload so callers don't
// need to import sdkproto. The agent reconstructs the original
// suggestion via index when applying the decision.
type PermissionSuggestion struct {
	Label  string
	HotKey string
	// Kind is a short tag the SDK exposes for policies that want to
	// filter by category (currently only "addDirectories" emitted).
	Kind string
}

// PermissionResponse is the rich return from PermissionPolicyExtFn.
//
//   - Decision: the basic Allow / Deny / Always outcome.
//   - SelectedSuggestion: index into PermissionRequest.Suggestions
//     (or -1 / out-of-range = none selected). Only honoured when
//     Decision is PermAllow.
type PermissionResponse struct {
	Decision           PermissionDecision
	SelectedSuggestion int
}

// PermissionAllow returns a policy that approves every prompt — used
// by `--permission-policy=allow`. Equivalent to BypassPermissions
// but goes through the same code path so audit logs see the call.
func PermissionAllow() PermissionPolicyFn {
	return func(_ context.Context, _ PermissionRequest) PermissionDecision {
		return PermAllow
	}
}

// PermissionDeny denies every prompt. Default for headless / SDK
// runs without a configured callback.
func PermissionDeny() PermissionPolicyFn {
	return func(_ context.Context, _ PermissionRequest) PermissionDecision {
		return PermDeny
	}
}

// PermissionAlways approves every prompt AND remembers the choice
// for the rest of the session. Mirrors the REPL "shift+a" path.
// Useful in CI sandboxes where you want one approval to cover the
// whole turn so subsequent identical calls don't burn a callback.
func PermissionAlways() PermissionPolicyFn {
	return func(_ context.Context, _ PermissionRequest) PermissionDecision {
		return PermAlways
	}
}

// MemoryMode toggles project-memory loading.
type MemoryMode int

const (
	// AutoMemory loads BIUMIND.md from user/project/local layers
	// (default).
	AutoMemory MemoryMode = iota
	// NoMemory skips memory loading entirely.
	NoMemory
)

// SettingsMode toggles settings.json loading.
type SettingsMode int

const (
	// AutoSettings loads ~/.biumind + project + local layers
	// (default).
	AutoSettings SettingsMode = iota
	// NoSettings skips file-based settings (BypassPermissions still
	// honoured).
	NoSettings
)

// Agent is the public handle on a running biu engine. Concurrency-
// safe: the underlying engine serialises Submit calls (subsequent
// calls return ErrConcurrent until the first finishes).
type Agent struct {
	eng       *engine.QueryEngine
	policy    PermissionPolicyFn
	policyExt PermissionPolicyExtFn

	// In-flight Submit cancel. Set by Submit before kicking the engine,
	// cleared after the channel closes. Interrupt() reads it under
	// cancelMu to fire ctx-cancellation on the running turn.
	//
	// CancelCauseFunc (not CancelFunc) so Interrupt() can attach the
	// engine.ErrInterrupted sentinel — the engine inspects
	// context.Cause(ctx) to distinguish deliberate Interrupt() from
	// parent timeout/cancel.
	cancelMu sync.Mutex
	cancel   context.CancelCauseFunc
}

// ErrConcurrent is returned via the event channel when Submit is
// called while another Submit is in flight. Wraps engine's internal
// sentinel for SDK callers.
var ErrConcurrent = errors.New("biumindkit: another Submit is in progress")

// ErrInterrupted is the cancel cause to attach when stopping an
// in-flight Submit deliberately. Equivalent to calling Agent.Interrupt(),
// but lets callers wire interruption through the parent ctx without
// holding an *Agent reference.
//
// Usage:
//
//	subCtx, cancel := context.WithCancelCause(ctx, nil)
//	go agent.Submit(subCtx, prompt)
//	// Later, from anywhere:
//	cancel(biumindkit.ErrInterrupted)
//
// The engine emits DoneEvent{StopReason:"interrupted"} (not Error) and
// well-formed synthetic tool_results so the session history stays
// replayable. Plain ctx cancel without this cause keeps the legacy
// timeout / parent-cancel ErrorEvent behaviour.
var ErrInterrupted = engine.ErrInterrupted

// New builds an Agent with the supplied Options.
func New(opt Options) (*Agent, error) {
	if opt.APIKey == "" && opt.Provider == nil {
		return nil, errors.New("biumindkit: APIKey is required (or supply Options.Provider)")
	}
	if opt.Model == "" {
		opt.Model = "claude-sonnet-4-6"
	}

	var prov engine.Provider
	switch {
	case opt.Provider != nil:
		prov = opt.Provider // caller owns config (headers etc)
	case opt.UseRelayAuth:
		p := client.NewRelayEngine(opt.AnthropicEndpoint, opt.APIKey)
		if len(opt.ExtraHeaders) > 0 {
			p.ExtraHeaders = opt.ExtraHeaders
		}
		prov = p
	default:
		p := client.NewAnthropicEngine(opt.APIKey, opt.AnthropicEndpoint)
		if len(opt.ExtraHeaders) > 0 {
			p.ExtraHeaders = opt.ExtraHeaders
		}
		prov = p
	}
	st := state.New()

	permCtx := permissions.NewContext()
	hookReg := hooks.NewRegistry()
	if opt.LoadProjectSettings != NoSettings {
		if l, err := settings.Load(opt.Cwd); err == nil {
			l.ApplyToContext(permCtx)
			registerLayer(hookReg, "user", l.User)
			registerLayer(hookReg, "project", l.Project)
			registerLayer(hookReg, "local", l.Local)
		}
	}
	if opt.PermissionMode != "" {
		permCtx.SetMode(permissions.ModeFromString(opt.PermissionMode))
	}

	// R6.3 / D7 — remote-device hard floor. Enforced in the permission
	// Context so it survives any policy-fn refactor and wins ahead of
	// acceptEdits / read-only auto-allow inside permissions.Decide.
	if len(opt.AllowedRoots) > 0 {
		// Path floor: anchor the working-dir gate (otherwise fail-open in
		// daemon runs — see permissions.pathOutsideWorkingDirs). roots[0]
		// is the cwd anchor; every root is an allowed directory.
		permCtx.SetOriginalCwd(opt.AllowedRoots[0])
		permCtx.AddDirectories(permissions.SrcCLIArg, opt.AllowedRoots)
	}
	if opt.ToolFloor != nil {
		// Capability floor: deny the dangerous tools this preset forbids.
		// Deny rules are Decide step 4 — ahead of acceptEdits (8) and
		// read-only (7) auto-allow.
		if deny := opt.ToolFloor.deniedDangerous(); len(deny) > 0 {
			permCtx.AddRules(permissions.SrcCLIArg, permissions.BehaviorDeny, deny)
		}
		// A global bypass would short-circuit the floor (Decide step 1),
		// so a floor and bypass are mutually exclusive — floor wins.
		if opt.BypassPermissions {
			opt.BypassPermissions = false
		}
		if m := permCtx.Mode(); m == permissions.ModeBypass || m == permissions.ModeFullAccess {
			permCtx.SetMode(permissions.ModeDefault)
		}
	}

	system := opt.System
	if opt.LoadProjectMemory != NoMemory {
		// Honour claudeMdExcludes from the merged settings layer when
		// it's enabled — keeps embedders' memory loads consistent
		// with the CLI behaviour.
		memOpt := memory.Options{}
		if opt.LoadProjectSettings != NoSettings {
			if l, err := settings.Load(opt.Cwd); err == nil {
				memOpt.Excludes = l.MergedClaudeMdExcludes()
			}
		}
		mem := memory.LoadWithOptions(opt.Cwd, memOpt)
		if memSys := mem.SystemPrompt(); memSys != "" {
			if system == "" {
				system = memSys
			} else {
				system = system + "\n\n" + memSys
			}
		}
		// Auto-memory primer (~/.biumind/memory). See cmd/biu/main.go
		// for the rationale — same wiring, same skip-on-no-HOME path.
		if home, err := os.UserHomeDir(); err == nil {
			auto := memory.LoadAuto(home)
			if autoSys := auto.SystemPrompt(); autoSys != "" {
				if system == "" {
					system = autoSys
				} else {
					system = system + "\n\n" + autoSys
				}
			}
		}
	}

	// Path-matched skills auto-attach: their bodies fold into the
	// system prompt before the engine starts so the model sees them
	// without any explicit /skill invocation. Honours the same
	// LoadProjectMemory toggle — embedders that opt out of memory
	// usually want a clean slate altogether.
	skillReg, _ := skills.Load(opt.Cwd)
	if opt.LoadProjectMemory != NoMemory && skillReg != nil {
		if attached := skillReg.AutoAttachPrompt(opt.Cwd); attached != "" {
			if system == "" {
				system = attached
			} else {
				system = system + "\n\n" + attached
			}
		}
	}

	reg := tools.Defaults().EngineRegistrySimple()
	files.Register(reg) // native file tools beat legacy adapters
	// File-loaded sub-agent registry — wired into AgentTool so
	// `subagent_type` overrides honour the parent's
	// ~/.biumind/agents/<name>.md definitions.
	agentReg, _ := agents.Load(opt.Cwd)
	orchestration.Register(reg, orchestration.Options{Agents: agentReg})
	// Background-task store: same instance flows to the Bash tool
	// (so Bash{run_in_background:true} can park work) AND to the
	// engine notifier (so completions auto-surface in the next
	// turn without polling).
	bgStore := bgtask.NewStore()
	webOpt := webtools.Options{BgTasks: bgStore, ExecHost: opt.ExecHost}
	// Pipe the merged settings.sandbox block into BashTool's FS rules
	// so embedders inherit the same allow/deny list semantics the CLI
	// already gets via cmd/biu/wiring/wiring.go. Without this the SDK
	// would silently drop user-supplied sandbox config — and Layer H
	// of the integration test plan can't verify FS rules without it.
	if opt.LoadProjectSettings != NoSettings {
		if l, err := settings.Load(opt.Cwd); err == nil {
			if sc := l.MergedSandboxConfig(opt.Cwd); sc != nil {
				webOpt.SandboxFSReadDeny = sc.FSReadDeny
				webOpt.SandboxFSReadAllowWithinDeny = sc.FSReadAllowWithinDeny
				webOpt.SandboxFSWriteAllowExtra = sc.FSWriteAllowExtra
				webOpt.SandboxFSWriteDenyWithinAllow = sc.FSWriteDenyWithinAllow
			}
		}
	}
	webtools.Register(reg, webOpt)
	// MCP servers (P20.47x): register every connected upstream's tools
	// + the resource / prompt wrappers under their `mcp__<server>__*`
	// namespace. Caller is responsible for bootstrapping the registry
	// before New runs and closing it after the last agent.Close.
	if opt.MCPRegistry != nil && opt.MCPRegistry.inner != nil {
		opt.MCPRegistry.inner.RegisterEngineTools(reg)
	}
	// ExtraTools come last so callers can override built-ins by name.
	for _, t := range opt.ExtraTools {
		reg.Register(&engineToolBridge{inner: t})
	}
	tracker := cost.NewTracker(opt.Model)
	// Default to the shared ~/.biu/usage.jsonl logger so embedders
	// get cross-session cost history out of the box. Failures are
	// non-fatal — the agent still runs without persistence.
	usageLog, _ := cost.NewLogger("")

	// Plan-drift verifier — wired to permissions.Context so
	// ExitPlanMode automatically refreshes the plan body without us
	// having to touch every call site.
	verifier := planverify.New()
	permCtx.SetPlanObserver(verifier.SetPlan)

	// Plan-mode auto-suggest. SDK embedders inherit the same
	// keyword heuristic as the CLI; default-on, override via
	// the embedding application's config plumbing.
	hinter := adapters.PlanHint(planhint.New(true, nil))

	// Seed prior conversation history so the engine's first turn sees
	// the full context (typical brain use case: rebuild thread history
	// from DB, hand it to a per-message agent). Skip silently when the
	// caller provides no PriorMessages.
	for _, m := range opt.PriorMessages {
		st.AppendMessage(state.Message{
			Role:       state.MessageRole(m.Role),
			Content:    m.Content,
			StopReason: m.StopReason,
			Model:      m.Model,
			ToolUseID:  m.ToolUseID,
			IsError:    m.IsError,
			CreatedAt:  time.Now(),
		})
	}

	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov,
		Model: opt.Model, System: system, Cwd: opt.Cwd,
		MaxToolTurns:      opt.MaxToolTurns,
		MaxTokens:         opt.MaxTokens,
		Permissions:       permCtx,
		Hooks:             hookReg,
		Cost:              tracker,
		UsageLogger:       usageLog,
		BypassPermissions: opt.BypassPermissions,
		PlanVerifier:      verifier,
		PlanHinter:        hinter,
		BgTaskNotifier:    adapters.BgTask(bgStore),
		AgentID:           opt.SessionID,
		ParentToolUseID:   opt.ParentToolUseID,
	})
	if err != nil {
		return nil, err
	}
	// Interactive tools register after engine.New so they can take
	// live engine handles (cwd switcher, mode toggle).
	interactive.Register(reg, interactive.Options{
		Perms:       permCtx,
		CwdSwitcher: eng,
		Notifier:    interactive.SystemNotifier("biu"),
		Plans:       interactive.NewDiskPlanStore(""),
	})
	policy := opt.PermissionPolicy
	if policy == nil && opt.PermissionPolicyExt == nil {
		// Default: deny so the turn fails loudly instead of hanging
		// on a non-existent TTY user. Headless callers should set
		// PermissionAllow / a stdin-driven policy explicitly.
		policy = PermissionDeny()
	}
	// Fire SessionStart now that the agent is fully wired. Idempotent
	// inside the engine — a future tweak that builds + discards an
	// Agent in tests doesn't double-fire.
	eng.FireSessionStart()
	return &Agent{eng: eng, policy: policy, policyExt: opt.PermissionPolicyExt}, nil
}

// Submit fires a user prompt. The returned channel emits SDK-level
// events and closes when the turn finishes. Callers drain the channel
// to completion.
//
// Permission asks are intercepted before they reach the SDK channel —
// the configured PermissionPolicy decides allow / deny / always so
// headless / SDK consumers never hang waiting on a TTY user.
//
// 等价于 SubmitContent(ctx, prompt, nil) — 多模态附件(图片等)走 SubmitContent。
func (a *Agent) Submit(ctx context.Context, prompt string) <-chan Event {
	return a.SubmitContent(ctx, prompt, nil)
}

// SubmitContent 跟 Submit 一样跑一次 turn,但允许在 user message 里附加
// 文本之外的 ContentBlock(目前只用 ContentImage,未来可扩 file/audio)。
// attachments 顺序保留;turn.go 在写 user message 时拼到 prompt text 之后。
//
// 视觉模型(claude-3-vision / glm-4.5v / qwen-vl-* / gpt-4o)消费图片块;
// 非视觉模型把 image content block 喂上去多数会 400 — 调用方应当先按
// model capabilities 过滤(brain 端 ChatRunner.resolveCreds 之后看到模型不
// 支持 vision 时 attachments 应当被剔除或拒绝)。
func (a *Agent) SubmitContent(
	ctx context.Context,
	prompt string,
	attachments []ContentBlock,
) <-chan Event {
	out := make(chan Event, 32)

	// Derive a cancel-cause ctx so Interrupt() can break the in-flight
	// turn from another goroutine and the engine can tell the deliberate-
	// user-stop case apart from a parent timeout/cancel via context.Cause.
	// Stored under cancelMu — replaced on each Submit, cleared after
	// channel close.
	subCtx, cancel := context.WithCancelCause(ctx)
	a.cancelMu.Lock()
	a.cancel = cancel
	a.cancelMu.Unlock()

	go func() {
		defer close(out)
		defer func() {
			a.cancelMu.Lock()
			a.cancel = nil
			a.cancelMu.Unlock()
			cancel(nil)
		}()
		// Panic isolation. A tool's Call, the provider stream parser, or a
		// hook can panic deep inside the engine. Without this, the panic
		// unwinds THIS goroutine uncaught and kills the whole process — for
		// the agent-plane daemon that means heartbeat + control + poll loops
		// (which share the process) all die from one bad work item. Recover,
		// surface a terminal Error event (mapped to SDKResultError downstream)
		// so embedders see a clean failure, then let the deferred close(out)
		// above end the turn. Registered AFTER close(out) → runs BEFORE it
		// (LIFO), so the channel is still open for this final send.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("biumindkit: recovered panic in Submit",
					"panic", r, "stack", string(debug.Stack()))
				select {
				case out <- Error{Err: fmt.Errorf("biumindkit: internal panic: %v", r)}:
				case <-ctx.Done():
				}
			}
		}()
		// Forward gating uses the user's parent ctx, NOT subCtx. When
		// Interrupt() fires, subCtx.Done() closes but ctx.Done() does
		// not — we want to keep forwarding the engine's final
		// DoneEvent{StopReason:"interrupted"} so embedders see a clean
		// stop. We also keep draining until the engine closes its
		// channel; bailing out early would leak the engine goroutine
		// stuck on a blocking send to a no-longer-read channel.
		fwdDone := ctx.Done()
		for raw := range a.eng.SubmitContent(subCtx, prompt, attachments) {
			// Intercept the engine's PermissionAskEvent — apply the
			// configured policy and reply on Decision so the runner
			// can proceed. This event is INTERNAL; it never reaches
			// the public SDK channel.
			if ask, ok := raw.(*engine.PermissionAskEvent); ok {
				go a.replyToPermission(subCtx, ask)
				continue
			}
			// AssistantMessage → AssistantBlock fan-out before the
			// summary AssistantText. Embedders that consume blocks
			// (brain S4 JsonEmitter) get per-block events; embedders
			// that just want text still get AssistantText.
			if msg, ok := raw.(*engine.AssistantMessageEvent); ok {
				base := BaseEvent{
					EventSessionID:       msg.SessionID(),
					EventParentToolUseID: msg.ParentToolUseID(),
				}
				for i, b := range msg.Message.Content {
					select {
					case out <- AssistantBlock{BaseEvent: base, Block: b, Index: i, StopReason: msg.StopReason}:
					case <-fwdDone:
						// parent ctx canceled — receiver gone; drop
						// but keep draining so engine can finish.
					}
				}
			}
			ev := translate(raw)
			if ev == nil {
				continue
			}
			select {
			case out <- ev:
			case <-fwdDone:
			}
		}
	}()
	return out
}

// Interrupt cancels the in-flight Submit. Returns nil immediately if no
// turn is running. Safe to call from any goroutine and idempotent —
// repeated calls after the first are no-ops.
//
// Maps to SDK Protocol Interrupt ControlRequest — brain receives the
// frame from a WS client, calls Agent.Interrupt(), the engine stops at
// the next safe yield point and emits a Done with StopReason=interrupted.
//
// The cancel cause is engine.ErrInterrupted so the engine's runSubmit
// can distinguish a deliberate user stop from a parent timeout / cancel
// and emit DoneEvent{StopReason:"interrupted"} instead of an ErrorEvent.
// Synthetic tool_result entries are also emitted for any tool_use block
// the model produced but the runner didn't reach, keeping history
// well-formed for replay.
func (a *Agent) Interrupt() error {
	a.cancelMu.Lock()
	cancel := a.cancel
	a.cancelMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel(engine.ErrInterrupted)
	return nil
}

// replyToPermission consults the configured policy and writes the
// decision back to the engine. Runs on its own goroutine so a slow
// stdin-based policy doesn't block the event drain.
//
// The Ext policy wins when both are configured (a callsite may want
// to keep its decision-only fallback alongside an Ext path that
// handles suggestion picks). Without either, defaults to Deny.
func (a *Agent) replyToPermission(ctx context.Context, ask *engine.PermissionAskEvent) {
	req := PermissionRequest{
		ToolUseID:   ask.ToolUseID,
		ToolName:    ask.ToolName,
		Input:       ask.Input,
		Reason:      ask.Reason,
		Suggestions: askSuggestionsToSDK(ask.Suggestions),
	}

	answer := engine.PermissionAnswer{Decision: engine.PermDeny}

	switch {
	case a.policyExt != nil:
		resp := a.policyExt(ctx, req)
		answer = sdkResponseToAnswer(resp, ask)
	case a.policy != nil:
		d := a.policy(ctx, req)
		answer = sdkDecisionToAnswer(d)
	default:
		// Neither configured → fail closed.
	}
	select {
	case ask.Decision <- answer:
	case <-ctx.Done():
	}
}

// askSuggestionsToSDK projects engine-level suggestions into the
// SDK's opaque shape. Kind is derived from the underlying sdkproto
// variant (currently always "addDirectories"; future kinds get more
// branches). Errors are swallowed — a malformed suggestion is
// dropped so the policy still sees the others.
func askSuggestionsToSDK(s []engine.AskSuggestion) []PermissionSuggestion {
	if len(s) == 0 {
		return nil
	}
	out := make([]PermissionSuggestion, 0, len(s))
	for _, sg := range s {
		if sg.Update == nil {
			continue
		}
		out = append(out, PermissionSuggestion{
			Label:  sg.Label,
			HotKey: sg.HotKey,
			Kind:   sg.Update.PermissionUpdateType(),
		})
	}
	return out
}

// sdkDecisionToAnswer maps the simple Allow/Always/Deny enum onto
// the engine's PermissionAnswer. No suggestion attachment.
func sdkDecisionToAnswer(d PermissionDecision) engine.PermissionAnswer {
	switch d {
	case PermAllow:
		return engine.PermissionAnswer{Decision: engine.PermAllow}
	case PermAlways:
		return engine.PermissionAnswer{Decision: engine.PermAllow, Remember: true}
	}
	return engine.PermissionAnswer{Decision: engine.PermDeny}
}

// sdkResponseToAnswer maps the rich PermissionResponse onto the
// engine answer, attaching the picked suggestion's update when
// SelectedSuggestion is in range and Decision is Allow.
func sdkResponseToAnswer(resp PermissionResponse, ask *engine.PermissionAskEvent) engine.PermissionAnswer {
	ans := sdkDecisionToAnswer(resp.Decision)
	if ans.Decision != engine.PermAllow {
		return ans
	}
	if resp.SelectedSuggestion < 0 || resp.SelectedSuggestion >= len(ask.Suggestions) {
		return ans
	}
	pick := ask.Suggestions[resp.SelectedSuggestion]
	if pick.Update != nil {
		ans.AppliedUpdates = append(ans.AppliedUpdates, pick.Update)
	}
	return ans
}

// Run is a blocking convenience: submit + drain + return the
// concatenated assistant text. Returns the final stop reason.
func (a *Agent) Run(ctx context.Context, prompt string) (string, string, error) {
	var assistant string
	var stop string
	for ev := range a.Submit(ctx, prompt) {
		switch e := ev.(type) {
		case AssistantText:
			assistant = e.Text
		case Done:
			stop = e.StopReason
		case Error:
			return assistant, stop, e.Err
		}
	}
	return assistant, stop, nil
}

// Cost returns the running token + USD usage.
func (a *Agent) Cost() Cost {
	s := a.eng.Cost().Snapshot()
	return Cost{
		Model: s.Model, USD: s.USD,
		InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
		CacheReadTokens: s.CacheReadTokens, CacheWriteTokens: s.CacheWriteTokens,
	}
}

// CostByTool returns the per-tool slice of session usage. Empty map
// when no tool has been invoked yet (fresh agent / pure-text turn).
//
// Each ToolCost entry is independent of LLM token cost — see ToolCost
// docs for the rationale. Useful for:
//
//   - "/cost --by-tool" style reports
//   - dashboards that show "this thread spent 80% of its tool time
//     in Bash" so users can spot wasteful patterns
//   - per-tool rate-limit decisions (block a runaway loop on Read
//     before it exhausts the LLM budget)
//
// Concurrency-safe — returns a fresh map, callers can mutate it
// without affecting the running agent's tracker.
func (a *Agent) CostByTool() map[string]ToolCost {
	raw := a.eng.Cost().SnapshotByTool()
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]ToolCost, len(raw))
	for name, u := range raw {
		out[name] = ToolCost{
			Calls:       u.Calls,
			ElapsedMs:   u.ElapsedMs,
			OutputBytes: u.OutputBytes,
			Errors:      u.Errors,
		}
	}
	return out
}

// Compact runs a manual macro-compact. Useful before forking a
// session or attaching an IDE that can't afford to re-receive the
// full history.
func (a *Agent) Compact(ctx context.Context) error {
	out := make(chan engine.Event, 16)
	done := make(chan error, 1)
	go func() { done <- a.eng.Compact(ctx, out); close(out) }()
	for range out {
	}
	return <-done
}

// Close releases SDK resources and fires the SessionEnd hook chain.
// Idempotent — engine.Close wraps a sync.Once so deferred Close() in
// callers won't double-fire user hooks. Callers should always defer
// Close after New so user-configured "log when SDK done" hooks
// always run.
func (a *Agent) Close() error {
	if a.eng == nil {
		return nil
	}
	return a.eng.Close()
}

// ─── Public event types ─────────────────────────────

// Event is the common interface for everything Submit emits.
//
// All events also expose routing metadata via SessionID() /
// ParentToolUseID() — the engine session this event came from and the
// outer AgentTool tool_use_id (when running inside a sub-agent). These
// are populated automatically by the engine's stamping forwarder so
// embedders never see zero metadata on a real event.
//
// Why public methods on Event itself: brain / Flutter daemon adapters
// translate biumindkit events to SDK Protocol frames where every
// frame carries `session_id` and `parent_tool_use_id` per the SDK
// Protocol spec. Putting the accessors on the interface lets the adapter
// pull both fields with a single type assertion regardless of event
// kind, instead of switching on every concrete type.
type Event interface {
	sdkEvent()
	SessionID() string
	ParentToolUseID() string
}

// BaseEvent is embedded into every concrete Event type. It exposes the
// SessionID / ParentToolUseID accessors required by the Event
// interface. The actual values are filled by the engine layer (engine.
// QueryEngine.Submit's stamping forwarder) and copied across the
// translate boundary in this file. Embedders writing tests can
// construct Event values with explicit BaseEvent{...} for routing
// assertions; production code never needs to set the fields by hand.
type BaseEvent struct {
	EventSessionID       string
	EventParentToolUseID string
}

// SessionID returns the engine session/agent id this event came from.
// Empty only for events constructed in tests outside the engine.
func (b BaseEvent) SessionID() string { return b.EventSessionID }

// ParentToolUseID returns the outer AgentTool tool_use_id this engine
// was spawned under (sub-agent case). Empty for top-level engines —
// the user-facing root agent.
func (b BaseEvent) ParentToolUseID() string { return b.EventParentToolUseID }

// AssistantText is the assembled assistant turn — fired once per LLM
// response. Coarse-grained: every text block in the message gets joined
// with `\n` so embedders that just want "what did the model say" can
// ignore the per-block events.
type AssistantText struct {
	BaseEvent
	Text       string
	StopReason string
}

func (AssistantText) sdkEvent() {}

// AssistantBlock fires once per content block in an assistant turn,
// BEFORE the AssistantText summary. Streaming UIs use this for fine-
// grained per-block render (text vs tool_use vs image side-by-side).
//
// Index is the block's position in Message.Content (0-based). Embedders
// can rely on stable ordering — the engine emits blocks in the order
// the LLM produced them.
type AssistantBlock struct {
	BaseEvent
	Block ContentBlock
	Index int
	// StopReason is the turn's stop reason; same value will appear on
	// the AssistantText that follows. Surfaced here so an emitter that
	// only consumes blocks (not the assembled text) can still finalize
	// turn state.
	StopReason string
}

func (AssistantBlock) sdkEvent() {}

// StreamingText carries one chunk of assistant text as it streams from
// the LLM. Multiple StreamingText events may fire before the assembled
// AssistantText that closes the turn. Embedders that want a typing-
// style UI consume these; embedders that just want the final text can
// ignore them and rely on AssistantText.
//
// Wraps engine.StreamTokenEvent.
type StreamingText struct {
	BaseEvent
	Text string
}

func (StreamingText) sdkEvent() {}

// ToolStart fires when a tool is about to run.
type ToolStart struct {
	BaseEvent
	ID    string
	Name  string
	Input map[string]any
}

func (ToolStart) sdkEvent() {}

// ToolResult fires when a tool finishes.
type ToolResult struct {
	BaseEvent
	ID      string
	Name    string
	Output  string
	IsError bool
	Elapsed time.Duration
}

func (ToolResult) sdkEvent() {}

// CompactStarted / CompactFinished fire on auto / manual compact.
type CompactStarted struct {
	BaseEvent
	Reason       string
	TokensBefore int
}

func (CompactStarted) sdkEvent() {}

// CompactFinished is paired with CompactStarted.
type CompactFinished struct {
	BaseEvent
	TokensBefore int
	TokensAfter  int
	TokensSaved  int
}

func (CompactFinished) sdkEvent() {}

// Error is a fatal or recoverable error emitted by the engine.
type Error struct {
	BaseEvent
	Err         error
	Recoverable bool
}

func (Error) sdkEvent() {}

// Done is the very last event before the channel closes. Mirrors the
// engine's DoneEvent.
type Done struct {
	BaseEvent
	StopReason       string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Elapsed          time.Duration
}

func (Done) sdkEvent() {}

// Cost is the snapshot returned by Agent.Cost.
type Cost struct {
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	USD              float64
}

// ToolCost is the per-tool slice of usage returned by
// Agent.CostByTool. We intentionally do NOT split LLM tokens to a
// tool — input tokens are context length (LLM-side, not tool-side)
// and output tokens are the model's prose / tool_use blocks (also
// LLM-side). What we do track per tool, because it cleanly attributes:
//
//   - Calls         total invocations
//   - ElapsedMs     wall time the tool's Call ran (matches the
//     runner's Elapsed in ToolResult events)
//   - OutputBytes   byte length of result text content
//   - Errors        invocations that came back IsError=true
//
// UI rendering: a small leaderboard ("Read 12× / 0.8s / 4KB",
// "Bash 3× / 12.4s / 30KB"). Useful for sniffing wasteful tools and
// tightening rate-limits; not for billing.
type ToolCost struct {
	Calls       int
	ElapsedMs   int64
	OutputBytes int64
	Errors      int
}

// translate maps an internal engine.Event to a public SDK event.
// Unknown / internal-only events return nil and are dropped.
//
// SessionID + ParentToolUseID hop across the boundary by reading from
// the engine.Event interface methods. The engine's stamping forwarder
// guarantees they're populated before raw arrives here.
func translate(raw engine.Event) Event {
	base := BaseEvent{
		EventSessionID:       raw.SessionID(),
		EventParentToolUseID: raw.ParentToolUseID(),
	}
	switch e := raw.(type) {
	case *engine.StreamTokenEvent:
		return StreamingText{BaseEvent: base, Text: e.Text}
	case *engine.AssistantMessageEvent:
		text := ""
		for _, b := range e.Message.Content {
			if b.Type == state.ContentText {
				if text != "" {
					text += "\n"
				}
				text += b.Text
			}
		}
		return AssistantText{BaseEvent: base, Text: text, StopReason: e.StopReason}
	case *engine.ToolUseStartEvent:
		return ToolStart{BaseEvent: base, ID: e.ID, Name: e.Name, Input: e.Input}
	case *engine.ToolUseResultEvent:
		body := ""
		for _, b := range e.Result.Content {
			body += b.Text
		}
		return ToolResult{
			BaseEvent: base,
			ID:        e.ID, Name: e.Name, Output: body,
			IsError: e.Result.IsError, Elapsed: e.Elapsed,
		}
	case *engine.CompactStartEvent:
		return CompactStarted{BaseEvent: base, Reason: e.Reason, TokensBefore: e.TokensBefore}
	case *engine.CompactDoneEvent:
		return CompactFinished{
			BaseEvent:    base,
			TokensBefore: e.TokensBefore,
			TokensAfter:  e.TokensAfter,
			TokensSaved:  e.TokensSaved,
		}
	case *engine.ErrorEvent:
		return Error{BaseEvent: base, Err: e.Err, Recoverable: e.Recoverable}
	case *engine.DoneEvent:
		return Done{
			BaseEvent:        base,
			StopReason:       e.StopReason,
			InputTokens:      e.InputTokens,
			OutputTokens:     e.OutputTokens,
			CacheReadTokens:  e.CacheReadTokens,
			CacheWriteTokens: e.CacheWriteTokens,
			Elapsed:          e.Elapsed,
		}
	}
	return nil
}

// registerLayer is a thin lift wrapper kept private so consumers
// don't need to know about settings.Settings shape.
func registerLayer(reg *hooks.Registry, source string, s *settings.Settings) {
	if reg == nil || s == nil || len(s.Hooks) == 0 {
		return
	}
	out := make(map[string][]json.RawMessage, len(s.Hooks))
	for evt, raw := range s.Hooks {
		if len(raw) == 0 {
			continue
		}
		out[evt] = []json.RawMessage{raw}
	}
	reg.Add(source, out)
}
