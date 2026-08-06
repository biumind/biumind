package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// BlockEmitter wraps an SSE writer with the ChunkType v2 protocol
// from docs/BiuMind-Chat-Optimization-Design.md §3.2.
//
// Goals:
//   - Emit typed lifecycle events (block.create / block.delta /
//     block.complete / tool.created / tool.completed / message.done /
//     block.error) so the client can route streaming chunks into the
//     right MessageBlock channel (text / thinking / tool / citation /
//     image / file / error).
//   - Maintain backward compat: every text delta is also published
//     via the legacy `delta` event so older clients keep working.
//   - Build the canonical `parts` array as a side effect so it can be
//     persisted into chat.messages.parts at message.done.
//
// The emitter is goroutine-safe; concurrent calls serialize on the
// HTTP write lock. The model-relay-frame consumer is single-threaded today,
// but defensive locking lets future tool dispatch interleave events
// without races.
//
// W2 only wires the text path. Tool events have first-class methods
// here for W6 to call into; nothing in send.go uses them yet.
type BlockEmitter struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	flusher   http.Flusher
	messageID uuid.UUID
	nextIdx   int
	// Active text block — the one that legacy `delta` events stream
	// into. Created lazily on first text chunk so a turn that begins
	// with thinking / tool calls doesn't get an empty leading block.
	activeText *blockState
	// Active thinking block — separate from the text block so they
	// can interleave (Anthropic emits both in alternation when
	// extended-thinking is on). Lazily created on first thinking
	// chunk and closed at the next non-thinking event.
	activeThinking *blockState
	// Built-up parts list, ready to persist at message.done.
	parts []map[string]any
}

type blockState struct {
	ID    string
	Index int
	Type  string
	// For text-like blocks: accumulated content.
	Buf string
	// Status to record on close.
	Status string
}

func NewBlockEmitter(w http.ResponseWriter, flusher http.Flusher,
	messageID uuid.UUID,
) *BlockEmitter {
	return &BlockEmitter{
		w:         w,
		flusher:   flusher,
		messageID: messageID,
		parts:     []map[string]any{},
	}
}

// SSE event names. Centralized so cmd/brain doesn't grep for strings.
const (
	EventBlockCreate   = "block.create"
	EventBlockDelta    = "block.delta"
	EventBlockComplete = "block.complete"
	EventBlockError    = "block.error"
	EventToolCreated   = "tool.created"
	EventToolCompleted = "tool.completed"
	EventMessageDone   = "message.done"

	// Legacy events kept for backwards compat. The cloud send loop
	// emits both the legacy and new events for text streaming so
	// existing clients keep working through the rollout window.
	EventLegacyDelta = "delta"
	EventLegacyStop  = "stop"
	EventLegacyDone  = "done"
)

// Block type strings; mirror the Flutter MessageBlock variants.
const (
	BlockTypeText     = "text"
	BlockTypeThinking = "thinking"
	BlockTypeToolUse  = "tool_use"
	BlockTypeCitation = "citation"
	BlockTypeImage    = "image"
	BlockTypeFile     = "file"
	BlockTypeError    = "error"
)

// emit serializes the SSE write under the lock.
func (e *BlockEmitter) emit(event string, payload any) {
	body, _ := json.Marshal(payload)
	fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", event, body)
	e.flusher.Flush()
}

// EmitRaw lets callers piggyback non-protocol events (user_message /
// assistant_message bootstraps; legacy stop/done) through the same
// writer + lock. Keeps the locking discipline single-source.
func (e *BlockEmitter) EmitRaw(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.emit(event, payload)
}

func (e *BlockEmitter) startBlock(blockType string) *blockState {
	id := uuid.NewString()
	bs := &blockState{
		ID:     id,
		Index:  e.nextIdx,
		Type:   blockType,
		Status: "streaming",
	}
	e.nextIdx++
	e.emit(EventBlockCreate, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   id,
		"type":       blockType,
		"index":      bs.Index,
	})
	return bs
}

// TextDelta appends to the active text block, lazily creating one on
// first call. Also emits the legacy `delta` event for backwards
// compat with v1 clients. A text chunk closes any in-flight thinking
// block — Anthropic interleaves the two and the renderer assumes
// thinking precedes its associated text.
func (e *BlockEmitter) TextDelta(text string) {
	if text == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeActiveThinkingLocked()
	if e.activeText == nil {
		e.activeText = e.startBlock(BlockTypeText)
	}
	e.activeText.Buf += text
	e.emit(EventBlockDelta, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   e.activeText.ID,
		"delta":      text,
	})
	// Legacy compat: pre-v2 clients only know `delta`.
	e.emit(EventLegacyDelta, map[string]any{"text": text})
}

// ThinkingDelta streams reasoning text into a thinking block. Mirrors
// TextDelta but routes to a separate channel so the UI can fold the
// reasoning behind a "Thought for Xs" disclosure rather than mixing
// it inline. Does NOT emit a legacy delta event — pre-v2 clients
// would render the reasoning as if it were the answer.
func (e *BlockEmitter) ThinkingDelta(text string) {
	if text == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Conversely: a thinking chunk closes any in-flight text — the
	// model is reflecting between sentences.
	e.closeActiveTextLocked()
	if e.activeThinking == nil {
		e.activeThinking = e.startBlock(BlockTypeThinking)
	}
	e.activeThinking.Buf += text
	e.emit(EventBlockDelta, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   e.activeThinking.ID,
		"delta":      text,
	})
}

// CloseActiveThinking finalizes the in-flight thinking block, if any.
// Called by the loop at turn boundaries so partsJson reflects the
// closed state when persisted. Idempotent.
func (e *BlockEmitter) CloseActiveThinking() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeActiveThinkingLocked()
}

func (e *BlockEmitter) closeActiveThinkingLocked() {
	if e.activeThinking == nil {
		return
	}
	bs := e.activeThinking
	bs.Status = "complete"
	e.parts = append(e.parts, map[string]any{
		"id":   bs.ID,
		"type": BlockTypeThinking,
		"text": bs.Buf,
	})
	e.emit(EventBlockComplete, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   bs.ID,
	})
	e.activeThinking = nil
}

// CloseActiveText finalizes the running text block (if any) and
// records it in `parts`. Idempotent.
func (e *BlockEmitter) CloseActiveText() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeActiveTextLocked()
}

func (e *BlockEmitter) closeActiveTextLocked() {
	if e.activeText == nil {
		return
	}
	bs := e.activeText
	bs.Status = "complete"
	e.parts = append(e.parts, map[string]any{
		"id":   bs.ID,
		"type": BlockTypeText,
		"text": bs.Buf,
	})
	e.emit(EventBlockComplete, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   bs.ID,
	})
	e.activeText = nil
}

// ─── Tool events (W6 will use these; defined now so the protocol is
// stable) ─────────────────────────────────────────────────────────

// ToolStarted emits tool.created and adds a tool_use part. Returns the
// generated block id so the caller can pair it with ToolCompleted.
func (e *BlockEmitter) ToolStarted(name string, input any) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	// A tool call interrupts any in-flight text or thinking.
	e.closeActiveTextLocked()
	e.closeActiveThinkingLocked()
	id := uuid.NewString()
	idx := e.nextIdx
	e.nextIdx++
	e.parts = append(e.parts, map[string]any{
		"id":     id,
		"type":   BlockTypeToolUse,
		"name":   name,
		"input":  input,
		"phase":  "running",
		"status": "streaming",
	})
	e.emit(EventToolCreated, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   id,
		"index":      idx,
		"name":       name,
		"input":      input,
	})
	return id
}

// ToolCompleted emits tool.completed and patches the prior tool_use
// part with the result. ms is wall-clock duration of the tool call.
func (e *BlockEmitter) ToolCompleted(blockID string, result any, ms int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.parts {
		if p["id"] == blockID {
			p["result"] = result
			p["phase"] = "success"
			p["status"] = "complete"
			p["duration_ms"] = ms
			break
		}
	}
	e.emit(EventToolCompleted, map[string]any{
		"message_id":  e.messageID.String(),
		"block_id":    blockID,
		"result":      result,
		"duration_ms": ms,
	})
}

// ToolFailed marks the prior tool_use part as failed.
func (e *BlockEmitter) ToolFailed(blockID, errMsg string, ms int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.parts {
		if p["id"] == blockID {
			p["error"] = errMsg
			p["phase"] = "error"
			p["status"] = "error"
			p["duration_ms"] = ms
			break
		}
	}
	e.emit(EventBlockError, map[string]any{
		"message_id": e.messageID.String(),
		"block_id":   blockID,
		"code":       "tool_failed",
		"message":    errMsg,
	})
}

// MessageDone emits message.done and the legacy `done` event. The
// caller is responsible for persisting parts to chat.messages.parts
// before invoking this so reconnect-resume sees the final state.
func (e *BlockEmitter) MessageDone(payload map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Make sure any tail-end blocks are closed before signalling done.
	e.closeActiveTextLocked()
	e.closeActiveThinkingLocked()
	withID := map[string]any{}
	for k, v := range payload {
		withID[k] = v
	}
	withID["message_id"] = e.messageID.String()
	withID["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	e.emit(EventMessageDone, withID)
	// Legacy `done` for v1 clients.
	e.emit(EventLegacyDone, payload)
}

// MessageError emits a top-level error event for failures that don't
// belong to any specific block (network drop, model-relay error, persist
// failure). Does not auto-close the active text block — callers can
// decide whether to discard or finalize the partial.
func (e *BlockEmitter) MessageError(code, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.emit(EventBlockError, map[string]any{
		"message_id": e.messageID.String(),
		"code":       code,
		"message":    msg,
	})
	// Legacy `error` for v1 clients.
	e.emit("error", map[string]any{"message": msg})
}

// PartsJSON returns the marshaled parts array, ready for persistence
// into chat.messages.parts. Returns "[]" when nothing was emitted.
func (e *BlockEmitter) PartsJSON() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.parts) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(e.parts)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// AccumulatedText returns the concatenated text from all closed text
// blocks plus the in-flight one. Used by send.go to keep
// chat.messages.content in sync with the parts so legacy clients (or
// search/preview consumers) still see a usable plain-text rendering.
func (e *BlockEmitter) AccumulatedText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var s string
	for _, p := range e.parts {
		if p["type"] == BlockTypeText {
			if t, ok := p["text"].(string); ok {
				s += t
			}
		}
	}
	if e.activeText != nil {
		s += e.activeText.Buf
	}
	return s
}
