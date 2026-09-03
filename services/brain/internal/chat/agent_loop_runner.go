package chat

// agent_loop_runner.go — one-shot agent loop for non-chat callers.
// (S3 P0-1 wiki autonomous-maintenance agent run.)
//
// chat.HandleSend → streamAssistant drives a thread-scoped loop that
// persists user + assistant messages. The wiki agent loop is task-scoped:
// the LLM reads sources / mutates pages / maintains backlinks in one run,
// and the *result* shows up as page.created / page.updated events on the
// page stream (store emits them inside each tool's tx) — not as a chat
// message. So RunAgentLoop is streamAssistant minus the thread/msg DB.
//
// The caller (wiki/api handleWikiAgentRun) owns:
//   - auth + project ownership (ownsProject)
//   - userID injection into ctx (tools.WithUserID — tool Invokers read it)
//   - system prompt + instruction text + mode→budget mapping
// and passes them here. This method owns the SSE + emitter + loop wiring.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// AgentLoopRunInput collects the business parameters for a one-shot agent
// run. None of the chat-thread plumbing (history load, message rows,
// sampling from thread metadata) applies — the caller supplies everything.
type AgentLoopRunInput struct {
	// System is the LLM system prompt. For wiki maintenance this carries
	// the project context + "read sources → mutate pages → maintain
	// backlinks → flag contradictions" directives.
	System string
	// UserText is the single user-turn instruction (e.g. "整理这个项目的
	// 知识，补全缺失页，合并重复"). There is no multi-turn history — the
	// agent loop's tool round-trips ARE the turns.
	UserText string
	// Model is the model id (resolved by model-relay). Empty → caller
	// should have filled it; we pass it through and let relay validate.
	Model string
	// Allowlist is the tool whitelist (tools.WikiAgentToolAllowlist for
	// wiki). nil = no restriction (advertise every cloud tool).
	Allowlist map[string]struct{}
	// MaxTurns caps the loop. 0 → AgentLoop default (8). Wiki maps
	// mode Fast/Standard/Deep to 4/8/12 (see wiki/api wikiAgentMaxTurns).
	MaxTurns int
	// RetrievalBudget (P2 #19) caps retrieval-class tool calls per run,
	// independent of MaxTurns. 0 → no retrieval budget. Wiki maps
	// mode Fast/Standard/Deep to 2/4/6 (wiki/api wikiAgentRetrievalBudget).
	RetrievalBudget int
}

// ErrStreamingUnsupported is returned by RunAgentLoop when the ResponseWriter
// can't flush (the caller should map it to a 500 before any SSE headers go out).
var ErrStreamingUnsupported = errors.New("chat: streaming unsupported (ResponseWriter is not a Flusher)")

// RunAgentLoop drives a one-shot agent loop and streams ChunkType v2 SSE
// events to w. ctx MUST already carry the caller's user id
// (tools.WithUserID) so tool Invokers can owner-scope their writes; r is
// used only to forward the bearer when PassThroughAuth is on. No thread or
// message rows are touched — persistence is the caller's concern (for wiki,
// there is none: the tool calls themselves emit page.* events).
//
// SSE is opened (200 + headers) unconditionally on entry, so on error the
// caller MUST NOT writeErr afterwards — the failure is surfaced as a
// block.error / MessageError event on the stream instead. Returns the run
// summary (token usage, stop reason) and a non-nil error only if the loop
// itself failed (already surfaced on the stream before return).
func (h *HTTPSender) RunAgentLoop(ctx context.Context, w http.ResponseWriter,
	r *http.Request, in AgentLoopRunInput,
) (*AgentRunResult, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrStreamingUnsupported
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Synthetic message id — only for SSE event correlation (the client
	// keys block.*/message.done off it). No DB row, so uuid.New is fine.
	msgID := uuid.New()
	be := NewBlockEmitter(w, flusher, msgID)

	bearer := h.StaticBearer
	if h.PassThroughAuth {
		bearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return h.runAgentLoop(ctx, be, msgID, bearer, in)
}

// RunAgentLoopBuffered is the in-process variant of RunAgentLoop for
// callers that need the run's final text instead of a live SSE stream
// (MCP wiki.chat). The loop, tool wiring, budgets and model-relay path
// are identical; the ChunkType v2 frames go to an in-memory buffer and
// the returned emitter exposes AccumulatedText() / PartsJSON() so the
// caller can project the outcome into its own protocol.
//
// bearer is the caller's user token forwarded to model-relay (same
// PassThrough semantics as RunAgentLoop); empty falls back to
// StaticBearer, which covers stdio/dev deployments that run with
// PassThroughAuth=false.
func (h *HTTPSender) RunAgentLoopBuffered(ctx context.Context, bearer string,
	in AgentLoopRunInput,
) (*AgentRunResult, *BlockEmitter, error) {
	bw := newBufferResponseWriter()
	msgID := uuid.New()
	be := NewBlockEmitter(bw, bw, msgID)
	if bearer == "" {
		bearer = h.StaticBearer
	}
	res, err := h.runAgentLoop(ctx, be, msgID, bearer, in)
	if err != nil {
		return nil, be, err
	}
	return res, be, nil
}

// runAgentLoop is the shared core of RunAgentLoop / RunAgentLoopBuffered:
// build the single-user-turn history, wire the loop (allowlist, turn +
// retrieval budgets), run it against model-relay, and finish the emitter
// (message.done on success, block.error on failure).
func (h *HTTPSender) runAgentLoop(ctx context.Context, be *BlockEmitter,
	msgID uuid.UUID, bearer string, in AgentLoopRunInput,
) (*AgentRunResult, error) {
	history := []hubMessage{{Role: "user", Content: in.UserText}}

	loop := NewAgentLoop(h, h.Tools)
	loop.ChatToolAllowlist = in.Allowlist
	loop.MaxTurns = in.MaxTurns
	loop.RetrievalBudget = in.RetrievalBudget

	runResult, runErr := loop.Run(ctx, AgentRunInput{
		Bearer:  bearer,
		Model:   in.Model,
		System:  in.System,
		Mode:    tools.ExecutionCloud,
		History: history,
		Emitter: be,
	})
	if runErr != nil {
		be.MessageError("agent_failed", runErr.Error())
		return nil, runErr
	}

	be.CloseActiveText()
	be.MessageDone(map[string]any{
		"assistant_message_id": msgID.String(),
		"stop_reason":          runResult.StopReason,
	})
	return runResult, nil
}

// bufferResponseWriter is an in-memory http.ResponseWriter + Flusher for
// RunAgentLoopBuffered. The SSE bytes are discarded — the caller reads
// the emitter's structured state (AccumulatedText / PartsJSON) instead.
type bufferResponseWriter struct {
	buf    bytes.Buffer
	header http.Header
}

func newBufferResponseWriter() *bufferResponseWriter {
	return &bufferResponseWriter{header: http.Header{}}
}

func (b *bufferResponseWriter) Header() http.Header         { return b.header }
func (b *bufferResponseWriter) WriteHeader(int)             {}
func (b *bufferResponseWriter) Flush()                      {}
func (b *bufferResponseWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
