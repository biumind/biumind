// Package engine implements the QueryEngine — biu's agent loop.
//
// The public surface is a single Submit() call returning a channel of
// Event values; everything else (turn loop, tool dispatch, compact
// boundary, hook fan-out) happens behind that channel.
//
// This file defines the Event taxonomy. The actual loop lives in
// engine.go; the LLM stream parser lives in stream.go.

package engine

import (
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// Event is the discriminated union pushed through the channel
// returned by QueryEngine.Submit. Implementations are concrete struct
// types whose pointer satisfies the kind() method.
//
// We intentionally don't use string-typed kind constants:
// type-switching gives compile-time exhaustiveness for free.
//
// Every Event also exposes routing metadata via SessionID() /
// ParentToolUseID(). SessionID is the engine's session id (= agentID
// for sub-agents); ParentToolUseID is the outer AgentTool tool_use_id
// the engine was spawned under (empty string for top-level engines).
// Embedders use these to:
//
//   - tag SDK Protocol frames (sdkproto requires session_id on every
//     frame; tool_progress / assistant carry parent_tool_use_id)
//   - reconstruct sub-agent call trees in UI (ToolStart inside a
//     sub-agent points back to the AgentTool that spawned it)
type Event interface {
	eventKind() string
	SessionID() string
	ParentToolUseID() string
}

// baseEvent is embedded into every concrete Event struct. It provides
// the SessionID / ParentToolUseID accessors so each event auto-implements
// the Event interface without per-type boilerplate. The engine fills
// these in via fillBase() right before SafeSend so callers never see
// zero-valued metadata on a real event.
//
// EventSessionID and EventParentToolUseID are exported (Go embedded-
// field accessor rules require it) but **callers should use the
// interface methods** SessionID() / ParentToolUseID(). The fields are
// only public so test code in this package can construct events with
// preset metadata.
type baseEvent struct {
	EventSessionID       string
	EventParentToolUseID string
}

// SessionID returns the engine's session/agent id this event came from.
// Returns "" only for events constructed outside the engine (e.g. by
// hand in tests).
func (b baseEvent) SessionID() string { return b.EventSessionID }

// ParentToolUseID returns the outer AgentTool tool_use_id the engine
// was spawned under. Empty string for top-level engines (the user-
// facing root agent).
func (b baseEvent) ParentToolUseID() string { return b.EventParentToolUseID }

// ─── Turn lifecycle ─────────────────────────────────────────────

// RequestStartEvent is emitted right before the LLM call goes out.
// UI uses it to flip the status indicator to "thinking", record a
// trace span, etc.
type RequestStartEvent struct {
	baseEvent
	TurnID    string
	Model     string
	Timestamp time.Time
	// Why kicked off this request. "user" = fresh user message; the
	// other reasons drive UI hints ("continuing tools", "compacting…").
	Reason RequestReason
}

type RequestReason string

const (
	ReasonUserPrompt   RequestReason = "user"
	ReasonAfterTool    RequestReason = "after_tool"
	ReasonAfterCompact RequestReason = "after_compact"
)

func (*RequestStartEvent) eventKind() string { return "request_start" }

// ─── LLM streaming ──────────────────────────────────────────────

// StreamTokenEvent carries a chunk of streamed assistant text. Multiple
// of these arrive between RequestStartEvent and AssistantMessageEvent.
type StreamTokenEvent struct {
	baseEvent
	Text string
}

func (*StreamTokenEvent) eventKind() string { return "stream_token" }

// StreamUsageEvent reports incremental token counts. model-relay / Anthropic
// emits this once at the end (input_tokens), then again with
// output_tokens. UI accumulates these into the cost bar.
type StreamUsageEvent struct {
	baseEvent
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
}

func (*StreamUsageEvent) eventKind() string { return "stream_usage" }

// AssistantMessageEvent is the assembled assistant turn — fired once
// per LLM response, AFTER all StreamTokenEvents and any embedded
// tool_use blocks have been parsed out.
type AssistantMessageEvent struct {
	baseEvent
	Message    state.Message
	StopReason string // end_turn | tool_use | max_tokens | stop_sequence | error
}

func (*AssistantMessageEvent) eventKind() string { return "assistant_message" }

// ─── Tool execution ─────────────────────────────────────────────

// ToolUseStartEvent fires immediately after the engine looks up a
// tool by name and BEFORE the permission / hook checks run. UI shows
// the ⏺ row at this point so the user sees what the LLM intended;
// a subsequent ToolUseResultEvent carries the actual outcome (real
// run, denied, hook-blocked, soft-error). Subscribers must NOT treat
// "ToolUseStartEvent fired" as proof the tool actually executed.
type ToolUseStartEvent struct {
	baseEvent
	ID    string         // tool_use id from the LLM (matches AssistantMessage block)
	Name  string         // tool name
	Input map[string]any // tool args
}

func (*ToolUseStartEvent) eventKind() string { return "tool_use_start" }

// ToolUseProgressEvent carries streaming progress mid-execution
// (e.g. bash stdout lines, file scan progress). Tools opt into this
// via ToolEnv.OnProgress.
type ToolUseProgressEvent struct {
	baseEvent
	ID   string
	Data ProgressData
}

// ProgressData is whatever the tool wants to surface to the UI. Common
// shapes: { kind: "stdout", line: "..."} for bash, { kind: "file",
// path: "..." } for Glob, etc.
type ProgressData map[string]any

func (*ToolUseProgressEvent) eventKind() string { return "tool_use_progress" }

// ToolUseResultEvent fires when a tool finishes (success, error, or
// canceled). The Result has the same shape as what gets fed back to
// the LLM, so UI can render the same content blocks.
type ToolUseResultEvent struct {
	baseEvent
	ID      string
	Name    string
	Result  ToolResultPayload
	Elapsed time.Duration
}

// ToolResultPayload is the wire shape of a tool result. Defined here
// (not in tools/) to break a potential import cycle: events flow from
// engine to UI without UI needing to import tools/.
type ToolResultPayload struct {
	Content   []state.ContentBlock // typically a single text block
	IsError   bool
	SoftError string // user-readable error LLM can recover from
}

func (*ToolUseResultEvent) eventKind() string { return "tool_use_result" }

// ─── Permissions ────────────────────────────────────────────────

// PermissionAskEvent fires when permissions.Decide returns "ask".
// UI is expected to show a dialog and write the answer back through
// Decision channel. Engine blocks the affected tool call until the
// channel receives a value.
//
// Suggestions are actionable shortcuts the runner pre-computes for
// the UI to render alongside the bare allow/deny choices. Picking
// one (via REPL hotkey, SDK callback, or AG-UI client) attaches its
// PermissionUpdate to the response — the engine applies it to the
// runtime ctx (and persists when the destination supports it) before
// continuing as Allow. Option + suggestion generation is rolled into
// a single channel-shaped contract.
//
// Empty Suggestions = no shortcuts; UI shows the bare 3-choice flow.
type PermissionAskEvent struct {
	baseEvent
	ToolUseID   string
	ToolName    string
	Input       map[string]any
	Reason      string // e.g. "destructive bash command", "first use"
	Decision    chan PermissionAnswer
	Suggestions []AskSuggestion
}

// AskSuggestion is one pre-computed "Allow + side-effect" option in
// a permission dialog. Label is what the UI renders; HotKey is a
// single character the REPL listens for; Update is the
// sdkproto-format permission change to apply on selection.
//
// Example (path outside working dir):
//
//	Label:  "Allow + add scratch/ to working dirs (this session)"
//	HotKey: "w"
//	Update: &sdkproto.AddDirectories{
//	          Directories: []string{"/abs/scratch"},
//	          Destination: "session",
//	        }
type AskSuggestion struct {
	Label  string
	HotKey string
	Update sdkproto.PermissionUpdate
}

// PermissionAnswer is what UI writes back. UpdatedInput allows the
// user to tweak args (e.g. correct a path) before approval.
//
// AppliedUpdates carries any AskSuggestion.Update the user picked
// (or the SDK callback selected). The runner applies each entry via
// permissions.ApplyPermissionUpdate before returning Allow, so the
// next tool call sees the widened ctx.
type PermissionAnswer struct {
	Decision       PermissionDecision
	UpdatedInput   map[string]any
	Remember       bool // store in alwaysAllow when true
	AppliedUpdates []sdkproto.PermissionUpdate
}

type PermissionDecision string

const (
	PermAllow PermissionDecision = "allow"
	PermDeny  PermissionDecision = "deny"
)

func (*PermissionAskEvent) eventKind() string { return "permission_ask" }

// UserQuestionAskEvent fires when a tool (typically AskUserQuestion)
// needs the user to pick from a multi-choice list. The REPL renders
// a panel and writes the selected indices back through Decision.
type UserQuestionAskEvent struct {
	baseEvent
	ToolUseID string
	Question  UserQuestion
	Decision  chan UserAnswer
}

func (*UserQuestionAskEvent) eventKind() string { return "user_question_ask" }

// ─── Compact pipeline ──────────────────────────────────────────

// CompactStartEvent fires when auto-compact triggers. UI shows a
// banner ("compacting context…").
type CompactStartEvent struct {
	baseEvent
	Reason       string // "tokens_above_threshold" | "manual" | "max_tokens_recovery"
	TokensBefore int
}

func (*CompactStartEvent) eventKind() string { return "compact_start" }

// CompactDoneEvent fires after compact completes. TokensSaved is
// negative for the rare case where the summary itself is bigger than
// the original (we still apply, since context usage is what matters).
type CompactDoneEvent struct {
	baseEvent
	TokensBefore int
	TokensAfter  int
	TokensSaved  int
}

func (*CompactDoneEvent) eventKind() string { return "compact_done" }

// CompactWarningEvent fires when context usage crosses an early /
// urgent threshold but the auto-compact firing line hasn't been
// crossed yet. Lets the REPL surface a heads-up so the user can
// /compact manually with control over the summary, or tighten their
// inputs before automatic compact fires.
//
// Each level fires at most once per compact cycle; a successful
// compact resets the warning state (handled by Engine — this event
// is purely informational).
type CompactWarningEvent struct {
	baseEvent
	Level       string // "info" | "urgent" — see compact.WarningLevel
	UsedTokens  int
	MaxTokens   int
	Ratio       float64 // 0.0..1.0
	NextActions string  // human-readable hint, e.g. "/compact, /clear, or keep going"
}

func (*CompactWarningEvent) eventKind() string { return "compact_warning" }

// ─── Errors + completion ───────────────────────────────────────

// ErrorEvent fires when the engine hit a recoverable or fatal error.
// Recoverable=true means the loop will retry (e.g. transient HTTP
// 503). Recoverable=false ends the turn.
type ErrorEvent struct {
	baseEvent
	Err         error
	Recoverable bool
	Source      ErrorSource
}

type ErrorSource string

const (
	ErrSrcLLM      ErrorSource = "llm"  // LLM API call failed
	ErrSrcTool     ErrorSource = "tool" // tool execution panicked
	ErrSrcHook     ErrorSource = "hook" // user hook returned error
	ErrSrcCompact  ErrorSource = "compact"
	ErrSrcInternal ErrorSource = "internal" // engine bug
)

func (*ErrorEvent) eventKind() string { return "error" }

// DoneEvent is the very last event before the channel closes. Stop
// reason is the LLM's last stop_reason; TotalCostUSD is this turn's
// (not the entire session's) cost.
type DoneEvent struct {
	baseEvent
	StopReason   string
	TurnCostUSD  float64
	InputTokens  int
	OutputTokens int
	// CacheReadTokens / CacheWriteTokens populate when the upstream
	// reports prompt-cache hits / writes. With biu's cache markers a
	// fresh process should see CacheWrite > 0 on the first turn and
	// CacheRead > 0 on every subsequent turn.
	CacheReadTokens  int
	CacheWriteTokens int
	Elapsed          time.Duration
}

func (*DoneEvent) eventKind() string { return "done" }

// ─── Helpers ───────────────────────────────────────────────────

// SafeSend writes ev to ch, dropping silently if the receiver is gone
// (channel buffer full + ctx canceled). Used by the engine internals
// where a blocked send would deadlock the goroutine that's also
// supposed to detect cancellation.
func SafeSend(ch chan<- Event, ev Event, done <-chan struct{}) {
	select {
	case ch <- ev:
	case <-done:
		// Receiver gone; drop the event. Backpressure on a closed
		// session is acceptable because the user isn't watching.
	}
}

// baseAccessor is satisfied by every concrete event in this file
// because they all embed baseEvent. We use it to set metadata on an
// event right before SafeSend without paying a type-switch cost per
// event kind. *baseEvent's pointer receiver methods are auto-promoted
// when the parent struct embeds baseEvent by value.
type baseAccessor interface {
	setBase(sessionID, parentToolUseID string)
}

// setBase fills the metadata fields. Pointer receiver because we need
// to mutate the embedded struct on the live event pointer (callers
// always hold *XxxEvent).
func (b *baseEvent) setBase(sessionID, parentToolUseID string) {
	b.EventSessionID = sessionID
	b.EventParentToolUseID = parentToolUseID
}

// fillBase stamps SessionID + ParentToolUseID onto the event before it
// flies. The engine calls this from a single chokepoint right before
// every SafeSend so per-call sites stay free of metadata bookkeeping.
// No-op on events that somehow don't embed baseEvent (impossible at
// compile time given the constructor private to this package, but
// belt-and-suspenders against future Event implementers).
func fillBase(ev Event, sessionID, parentToolUseID string) Event {
	if a, ok := ev.(baseAccessor); ok {
		a.setBase(sessionID, parentToolUseID)
	}
	return ev
}
