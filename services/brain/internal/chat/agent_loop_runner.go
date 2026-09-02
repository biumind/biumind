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

	history := []hubMessage{{Role: "user", Content: in.UserText}}

	loop := NewAgentLoop(h, h.Tools)
	loop.ChatToolAllowlist = in.Allowlist
	loop.MaxTurns = in.MaxTurns
	loop.RetrievalBudget = in.RetrievalBudget

	bearer := h.StaticBearer
	if h.PassThroughAuth {
		bearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}

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
