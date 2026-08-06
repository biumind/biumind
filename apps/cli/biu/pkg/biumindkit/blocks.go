package biumindkit

// Per-block content surface — re-exports of internal/state types so
// embedders can read assistant turns at the block granularity (one
// text block, one tool_use block, etc.) without importing internal
// packages.
//
// Brain S4 uses these to translate biumindkit Submit events into the
// SDK Protocol StdoutMessage frames the WS client consumes (S4-6
// JsonEmitter). Without per-block visibility, the only public hook is
// AssistantText which collapses all text into one string — too coarse
// for streaming UIs.

import "github.com/biumind/biumind/apps/cli/biu/internal/state"

// ContentBlock mirrors Anthropic's Messages API block shape (text /
// tool_use / tool_result / image), discriminated by Type.
type ContentBlock = state.ContentBlock

// ContentType discriminates ContentBlock.Type.
type ContentType = state.ContentType

const (
	// ContentText —— a chunk of assistant text.
	ContentText = state.ContentText
	// ContentToolUse —— assistant requested a tool call.
	ContentToolUse = state.ContentToolUse
	// ContentToolResult —— response we feed back to the LLM.
	ContentToolResult = state.ContentToolResult
	// ContentImage —— inline image (base64).
	ContentImage = state.ContentImage
)

// Message is one turn in the conversation. Used by Options.PriorMessages
// to seed history before Submit runs. Role is one of "user", "assistant",
// "tool_result", "system".
type Message struct {
	Role    string
	Content []ContentBlock

	// Optional — assistant turn metadata (mirrors state.Message). Empty
	// for user / tool_result entries.
	StopReason string
	Model      string

	// Optional — tool_result-only metadata.
	ToolUseID string
	IsError   bool
}
