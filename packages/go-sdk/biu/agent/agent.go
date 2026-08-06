// Package agent — heterogeneous code-agent runner abstraction.
//
// The BiuMind workbench drives more than one coding agent (Claude Code,
// Codex CLI, our own `biu` Runner) with the same UX. This package gives
// every one of them a uniform shape:
//
//	type Runner interface {
//	    Run(ctx, Request) (<-chan Event, error)
//	}
//
// Each adapter shells out to its CLI in `--json` / `stream-json` mode,
// parses the JSONL output, and produces a normalized [Event] stream. UI
// code (Flutter, web, future TUI) only ever sees the canonical shape;
// swapping which agent is in use is a one-line change.
//
// Replay: any `<-chan Event` can be teed through a [Recorder] to disk as
// JSONL. The same JSONL is played back by [Replay], producing an
// identical channel — so live and historical sessions share a render path.
package agent

import (
	"context"
	"time"
)

// EventType — canonical kinds the workbench renders. Adapters MUST map
// their native vocabulary onto these. Unknown native types should be
// surfaced as kind="raw" with the original payload preserved so we never
// lose information just because we haven't taught the adapter about a new
// frame yet.
const (
	EventSystem     = "system"      // session start, model info, env summary
	EventText       = "text"        // assistant prose chunk
	EventThinking   = "thinking"    // assistant interior monologue (Claude / o1-style)
	EventToolUse    = "tool_use"    // tool invocation start
	EventToolResult = "tool_result" // tool invocation result
	EventCommand    = "command"     // shell command being run by the agent
	EventError      = "error"       // adapter or agent error
	EventDone       = "done"        // session ended normally
	EventRaw        = "raw"         // adapter could not classify; payload kept
)

// Runner is what every adapter implements. Run consumes the request,
// shells out (or otherwise drives the underlying agent), and emits Events
// on the returned channel. The channel closes when the session ends —
// either after EventDone (success) or EventError (failure). The caller
// MUST drain the channel until close, otherwise the adapter goroutine
// leaks.
type Runner interface {
	Name() string
	Run(ctx context.Context, req Request) (<-chan Event, error)
}

// Request describes one agent invocation.
type Request struct {
	// Workdir the agent runs in. Defaults to the current process CWD.
	Workdir string

	// Prompt is the user's instruction. Required.
	Prompt string

	// SystemPrompt overrides the runner's default system prompt.
	// Empty = adapter default.
	SystemPrompt string

	// Model — adapter-specific identifier. Empty = adapter default.
	// Examples: "sonnet", "opus", "gpt-5", "claude-opus-4-7".
	Model string

	// Continue=true reuses the most recent session in the workdir
	// (Claude Code: `--continue`; biu: `--session=…`).
	Continue bool

	// SessionID — explicit session to resume. Mutually exclusive with
	// Continue=true.
	SessionID string

	// Env to merge on top of the caller's. Useful for ANTHROPIC_BASE_URL
	// overrides etc. (Runtime v3 A2 注入点.)
	Env map[string]string

	// ClearEnv lists env keys to DELETE from the inherited environment
	// before spawning the child (after inheriting os.Environ, before
	// merging Env). Runtime v3 A1: 清掉平台的 ANTHROPIC_API_KEY 等,让
	// Claude Code CLI 回落到用户自己的 ~/.claude 订阅(不计 biumind 额度)。
	ClearEnv []string

	// Timeout — hard kill after this many seconds. 0 = no limit.
	TimeoutSec int
}

// Event is the canonical wire shape downstream code renders. Payload is
// adapter-flavoured but always JSON-serializable.
type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"ts"`
	SessionID string    `json:"session_id,omitempty"`
	// Content for EventText / EventThinking / EventCommand. Convenience
	// shortcut so renderers don't always have to dig into Payload.
	Content string `json:"content,omitempty"`
	// Tool — name + input/output for EventToolUse / EventToolResult.
	Tool *ToolEvent `json:"tool,omitempty"`
	// Payload — the original adapter frame, kept verbatim so callers
	// never lose information.
	Payload map[string]any `json:"payload,omitempty"`
}

type ToolEvent struct {
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	Output string         `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
}
