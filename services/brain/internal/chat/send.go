// Cloud-mode streaming send: Brain → model-relay → LLM, with everything
// persisted along the way.
//
// Flow:
//
//	1. Insert user message (status=success)
//	2. Insert assistant message (status=streaming, content="")
//	3. SSE: emit user_message + assistant_message events to client
//	4. Build context (recent messages + thread.summary if any) under
//	   the model's token budget
//	5. POST /v1/messages stream=true to model-relay, accumulate content
//	6. Forward each delta as SSE to client; persist nothing
//	   intermediate (DB write per delta = QPS death)
//	7. On stop:
//	   - UPDATE assistant message: content, status=success, tokens
//	   - SSE: emit done {usage}
//	8. On error / cancel:
//	   - UPDATE assistant: status=error/paused + error message
//	   - SSE: emit error / done
//
// Cancellation:
//   - Client close → forward to model-relay (still consume to drain), but
//     state stays streaming so other devices can pick up.
//   - Explicit POST .../cancel → cancel model-relay stream + status=paused.

package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// HTTPSender is the production Sender implementation. It POSTs to a
// model-relay /v1/messages endpoint and forwards SSE frames to the client
// while persisting the accumulated content.
type HTTPSender struct {
	Store *Store
	// model-relay endpoint base URL (no trailing slash).
	RelayURL string
	// Bearer used for model-relay calls. Either:
	//   - a service-to-service virtual key (cloud mode), OR
	//   - the user's JWT forwarded as-is (lets model-relay apply per-user
	//     plan / quota gates exactly like a direct call would)
	// We default to passthrough (the client's Authorization header)
	// because that gives correct billing attribution.
	PassThroughAuth bool
	// Static bearer used when PassThroughAuth=false. Optional.
	StaticBearer string
	// Token budget for assembling context (defaults to 100k).
	ContextBudgetTokens int
	// Number of recent messages to include before the budget kicks in.
	MinHistory int

	HTTP *http.Client

	// Tools is the cloud-side tool catalog the AgentLoop consults.
	// nil → no tools registered, agent loop degenerates to a single
	// LLM turn and behaves identically to the legacy text-only path.
	Tools *tools.Registry

	// In-flight cancellation registry. Key = assistant message id.
	mu      sync.Mutex
	cancels map[uuid.UUID]context.CancelFunc
}

func NewHTTPSender(store *Store, relayURL string) *HTTPSender {
	return &HTTPSender{
		Store:               store,
		RelayURL:              strings.TrimRight(relayURL, "/"),
		PassThroughAuth:     true,
		ContextBudgetTokens: 100_000,
		MinHistory:          10,
		HTTP:                &http.Client{Timeout: 0}, // SSE: no timeout
		cancels:             map[uuid.UUID]context.CancelFunc{},
	}
}

// WithTools wires a tool registry for the cloud-side AgentLoop. nil
// is a valid argument and disables tool-use entirely.
func (h *HTTPSender) WithTools(reg *tools.Registry) *HTTPSender {
	h.Tools = reg
	return h
}

type sendReq struct {
	ClientID string          `json:"client_id"`
	Parts    json.RawMessage `json:"parts"`     // multimodal content array
	Content  string          `json:"content"`   // fallback if parts empty
	Model    *string         `json:"model"`     // optional override
	System   *string         `json:"system"`    // optional override
	// Per-send override of thread.metadata.model_params. Any field
	// set here wins over the thread default. Client-side UI
	// typically writes to thread.metadata so the override stays
	// stable across sends; this path is for ad-hoc / debugging
	// callers (curl, biu CLI).
	MaxTokens     int      `json:"max_tokens"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
}

// modelParams parses thread.metadata for a `model_params` object
// holding chat-time overrides. Returns the zero-value struct when the
// key is absent, malformed, or the metadata is empty — callers treat
// nil-pointer fields as "use provider default" so missing → safe.
type modelParams struct {
	MaxTokens     int      `json:"max_tokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
}

// hasAny reports whether any field is set; cheaper than reflecting
// over the struct and avoids the "struct containing []string cannot
// be compared" issue.
func (m modelParams) hasAny() bool {
	return m.MaxTokens != 0 ||
		m.Temperature != nil ||
		m.TopP != nil ||
		len(m.StopSequences) > 0
}

func parseThreadModelParams(metadata []byte) modelParams {
	if len(metadata) == 0 {
		return modelParams{}
	}
	var meta struct {
		ModelParams *modelParams `json:"model_params"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return modelParams{}
	}
	if meta.ModelParams == nil {
		return modelParams{}
	}
	return *meta.ModelParams
}

// mergedModelParams folds per-send request overrides onto the thread
// defaults. Per-send wins where set; thread defaults fill the rest.
func mergedModelParams(req sendReq, thread modelParams) modelParams {
	out := thread
	if req.MaxTokens > 0 {
		out.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	if req.TopP != nil {
		out.TopP = req.TopP
	}
	if len(req.StopSequences) > 0 {
		out.StopSequences = req.StopSequences
	}
	return out
}

func (h *HTTPSender) HandleSend(w http.ResponseWriter, r *http.Request,
	threadID, userID uuid.UUID,
) {
	// Verify thread + ownership.
	thread, err := h.Store.GetThread(r.Context(), userID, threadID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}

	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// Compose content text from parts when caller only supplied parts.
	contentText := req.Content
	if contentText == "" && len(req.Parts) > 0 {
		contentText = textFromParts(req.Parts)
	}
	if contentText == "" {
		writeErr(w, http.StatusBadRequest, "empty_message", "no content or parts")
		return
	}

	model := strFallback(req.Model, thread.Model)
	if model == "" {
		writeErr(w, http.StatusBadRequest, "no_model",
			"thread has no model configured and request didn't override")
		return
	}
	system := strFallback(req.System, thread.SystemPrompt)

	// 1) Insert user message.
	userMsg, err := h.Store.CreateMessage(r.Context(), CreateMessageInput{
		ThreadID: threadID,
		UserID:   userID,
		Role:     RoleUser,
		Content:  contentText,
		Parts:    []byte(req.Parts),
		Status:   StatusSuccess,
		ClientID: nilIfEmpty(req.ClientID),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "user_msg_failed", err.Error())
		return
	}

	// 2) Insert assistant placeholder, parented to the user message
	// so the regenerate flow (P1.2) can find siblings via parent_id.
	assistantMsg, err := h.Store.CreateMessage(r.Context(), CreateMessageInput{
		ThreadID: threadID,
		UserID:   userID,
		Role:     RoleAssistant,
		Content:  "",
		Status:   StatusStreaming,
		Model:    &model,
		ParentID: &userMsg.ID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError,
			"assistant_msg_failed", err.Error())
		return
	}

	h.streamAssistant(w, r, thread, &userMsg.ID, assistantMsg,
		model, system, req)
}

// streamAssistant runs the SSE+agent-loop sequence shared by
// HandleSend (new message) and HandleRegenerate (sibling re-roll).
// `userMsgID` may be nil for legacy callers without parent linkage,
// though everything in production sets it.
func (h *HTTPSender) streamAssistant(
	w http.ResponseWriter,
	r *http.Request,
	thread *Thread,
	userMsgID *uuid.UUID,
	assistantMsg *Message,
	model, system string,
	req sendReq,
) {
	threadID := thread.ID
	userID := thread.UserID

	// 3) Open SSE connection back to client.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError,
			"streaming_unsupported", "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// ChunkType v2 protocol — see docs §3.2. The emitter dual-publishes
	// new (block.*/tool.*) and legacy (delta/stop/done/error) events so
	// older clients keep working while the rollout window is open.
	be := NewBlockEmitter(w, flusher, assistantMsg.ID)
	emit := func(event string, payload any) {
		// Backwards-compat shim for the few non-protocol events
		// (user_message / assistant_message / stop / error). Goes
		// through the emitter so we keep one writer + lock.
		be.EmitRaw(event, payload)
	}
	// HandleRegenerate doesn't have a fresh user message to announce
	// — the placeholder is what's new. HandleSend always supplies
	// userMsgID so this branch handles both paths uniformly.
	if userMsgID != nil {
		// Re-fetch the user message so the wire shape matches the
		// HandleSend leg (which used to emit the freshly-created row).
		// Cheap — primary-key lookup, owner-scoped.
		if userMsg, err := h.Store.GetMessage(r.Context(), userID,
			*userMsgID); err == nil {
			emit("user_message", messageOut(userMsg))
		}
	}
	emit("assistant_message", messageOut(assistantMsg))

	// 4) Build context.
	history, err := h.Store.ListMessages(r.Context(), ListMessagesInput{
		ThreadID:       threadID,
		UserID:         userID,
		BeforePosition: &assistantMsg.Position,
		Limit:          200,
	})
	if err != nil {
		h.failV2(assistantMsg.ID, userID,
			fmt.Sprintf("history load: %v", err), be)
		return
	}
	// Drop superseded sibling assistants — when a user has multiple
	// regenerations of the same prompt, only the latest sibling
	// counts as "history". Without this the model would see itself
	// answering the same question twice.
	history = pickLatestSiblings(history)
	// Regenerate path: trim history to end at the parent user
	// message. Without this trim the prior assistant sibling
	// (still in DB as the most-recent reply for that parent)
	// becomes the last message we ship, and Anthropic 400s with
	// "conversation must end with a user message".
	if assistantMsg.ParentID != nil {
		history = trimAfterParent(history, *assistantMsg.ParentID)
	}
	hubMessages := buildHubMessages(history, h.MinHistory, h.ContextBudgetTokens)

	// 5) Stream from model-relay. The hubCtx is intentionally detached from
	// the request context so that a client disconnect doesn't kill
	// the in-flight model-relay stream — we keep draining + persisting. We
	// do tag it with the caller's user id so any tool the agent loop
	// invokes can scope its data access (wiki.search uses this for
	// owner_id, memory.recall same idea).
	hubCtx, cancel := context.WithCancel(tools.WithUserID(
		context.Background(), userID))
	h.mu.Lock()
	h.cancels[assistantMsg.ID] = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.cancels, assistantMsg.ID)
		h.mu.Unlock()
		cancel()
	}()

	bearer := h.StaticBearer
	if h.PassThroughAuth {
		bearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}

	// 6) Drive the cloud agent loop. With zero registered tools the
	// loop runs exactly one model-relay turn and behaves identically to the
	// legacy text-only path. With tools, it round-trips
	// LLM↔tool↔LLM until the model emits a non-tool stop reason.
	loop := NewAgentLoop(h, h.Tools)
	// Q1: chat-mode tool whitelist (default-deny). HandleSend is always a
	// chat-mode send, so the gate always applies here. See tools/chatmode.go.
	loop.ChatToolAllowlist = tools.DefaultChatToolAllowlist
	// Runtime v3 D9: thread.execution_mode 已删。chat loop 恒在 brain（cloud
	// 侧），工具集恒走 cloud-runtime 过滤。工具执行环境（轴 B）由
	// agent_sessions.runtime_env_mode 表达，不在 chat send 路径。
	mode := tools.ExecutionCloud
	// Sampling params: thread defaults, per-send overrides on top.
	threadParams := parseThreadModelParams(thread.Metadata)
	mp := mergedModelParams(req, threadParams)
	runResult, runErr := loop.Run(hubCtx, AgentRunInput{
		Bearer:        bearer,
		Model:         model,
		System:        system,
		Mode:          mode,
		History:       hubMessages,
		MaxTokens:     mp.MaxTokens,
		Temperature:   mp.Temperature,
		TopP:          mp.TopP,
		StopSequences: mp.StopSequences,
		Emitter:       be,
	})
	if runErr != nil {
		h.failV2(assistantMsg.ID, userID,
			fmt.Sprintf("agent: %v", runErr), be)
		return
	}
	var usagePT, usageCT *int
	if runResult.PromptTokens > 0 {
		v := runResult.PromptTokens
		usagePT = &v
	}
	if runResult.CompletionTokens > 0 {
		v := runResult.CompletionTokens
		usageCT = &v
	}
	// Drain client-disconnect signal so we still keep persistence
	// going while the SSE writer is silently no-op'd by the closed
	// connection.
	ctxClosed := false
	select {
	case <-r.Context().Done():
		ctxClosed = true
	default:
	}
	if !ctxClosed {
		// Legacy `stop` for v1 clients; v2 clients ignore it (they
		// use block.complete + message.done instead).
		emit(EventLegacyStop, map[string]any{
			"reason": runResult.StopReason,
			"usage": map[string]any{
				"prompt_tokens":     valOrZero(usagePT),
				"completion_tokens": valOrZero(usageCT),
			},
		})
	}

	// Make sure the final text block is closed before we persist —
	// emitter must have written all parts into its buffer.
	be.CloseActiveText()

	// 7) Persist final state. Use background context — if the client
	// went away mid-stream, we still want the row updated.
	finalContent := be.AccumulatedText()
	finalParts := be.PartsJSON()
	updated, err := h.Store.UpdateMessage(context.Background(),
		UpdateMessageInput{
			UserID:           userID,
			MessageID:        assistantMsg.ID,
			Content:          &finalContent,
			Parts:            finalParts,
			Status:           pStr(StatusSuccess),
			PromptTokens:     usagePT,
			CompletionTokens: usageCT,
		})
	if err != nil {
		h.Store.UpdateMessage(context.Background(), UpdateMessageInput{
			UserID:    userID,
			MessageID: assistantMsg.ID,
			Status:    pStr(StatusError),
			ErrorMsg:  pStr(fmt.Sprintf("persist: %v", err)),
		})
		if !ctxClosed {
			be.MessageError("persist_failed", err.Error())
		}
		return
	}

	if !ctxClosed {
		be.MessageDone(map[string]any{
			"assistant_message_id": updated.ID.String(),
			"thread_updated_at":    updated.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
}

// trimAfterParent truncates history at (and including) the user
// message identified by parentID. Used by the regenerate path so
// the prompt we ship to the LLM ends at the user's question and
// doesn't trail into prior assistant replies — required by
// Anthropic which insists `messages.last.role == 'user'`.
//
// Returns history unchanged if parentID isn't found (defensive:
// new HandleSend path doesn't need the trim because the user msg
// is already the last item, but calling this is a safe no-op).
func trimAfterParent(history []*Message, parentID uuid.UUID) []*Message {
	for i, m := range history {
		if m.ID == parentID {
			return history[:i+1]
		}
	}
	return history
}

// pickLatestSiblings drops superseded assistant messages when the
// user has regenerated a reply more than once. Group key: parent_id
// (the user message). Within a group, only the highest-position
// assistant survives — the others were earlier rerolls the user has
// since moved past.
//
// Messages without parent_id (legacy or non-assistant) pass through
// unchanged. Output preserves the original ordering so the agent
// loop's history walk stays linear.
func pickLatestSiblings(history []*Message) []*Message {
	// First pass: per parent_id, find the latest assistant position.
	latestByParent := map[uuid.UUID]int64{}
	for _, m := range history {
		if m.Role != RoleAssistant || m.ParentID == nil {
			continue
		}
		if cur, ok := latestByParent[*m.ParentID]; !ok || m.Position > cur {
			latestByParent[*m.ParentID] = m.Position
		}
	}
	// Second pass: emit the survivors.
	out := make([]*Message, 0, len(history))
	for _, m := range history {
		if m.Role == RoleAssistant && m.ParentID != nil {
			if latest := latestByParent[*m.ParentID]; m.Position != latest {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// HandleRegenerate creates a fresh assistant placeholder for an
// existing user message and runs the streaming loop on it. The new
// placeholder is parented to the same user message as any prior
// assistant — making it a sibling. UI surfaces siblings via the
// 1/N ◄ ► chip; default render is the latest position.
func (h *HTTPSender) HandleRegenerate(w http.ResponseWriter,
	r *http.Request, threadID, userMsgID, userID uuid.UUID,
) {
	thread, err := h.Store.GetThread(r.Context(), userID, threadID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	userMsg, err := h.Store.GetMessage(r.Context(), userID, userMsgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if userMsg.ThreadID != threadID {
		writeErr(w, http.StatusBadRequest, "wrong_thread", "")
		return
	}
	if userMsg.Role != RoleUser {
		writeErr(w, http.StatusBadRequest, "not_user_msg",
			"can only regenerate replies to user messages")
		return
	}

	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil &&
		err != io.EOF {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	model := strFallback(req.Model, thread.Model)
	if model == "" {
		writeErr(w, http.StatusBadRequest, "no_model",
			"thread has no model configured and request didn't override")
		return
	}
	system := strFallback(req.System, thread.SystemPrompt)

	// Create the sibling placeholder. Same parent_id as any prior
	// assistant for this user message.
	assistantMsg, err := h.Store.CreateMessage(r.Context(), CreateMessageInput{
		ThreadID: threadID,
		UserID:   userID,
		Role:     RoleAssistant,
		Content:  "",
		Status:   StatusStreaming,
		Model:    &model,
		ParentID: &userMsgID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError,
			"assistant_msg_failed", err.Error())
		return
	}

	h.streamAssistant(w, r, thread, &userMsgID, assistantMsg,
		model, system, req)
}

// failV2 is the BlockEmitter-aware failure path: persists the error
// state + emits both new (block.error) and legacy (error) events so
// v1 and v2 clients both see the failure.
func (h *HTTPSender) failV2(msgID, userID uuid.UUID, reason string,
	be *BlockEmitter,
) {
	_, _ = h.Store.UpdateMessage(context.Background(), UpdateMessageInput{
		UserID:    userID,
		MessageID: msgID,
		Status:    pStr(StatusError),
		ErrorMsg:  &reason,
	})
	be.MessageError("stream_failed", reason)
}

func (h *HTTPSender) HandleCancel(w http.ResponseWriter, r *http.Request,
	msgID, userID uuid.UUID,
) {
	// Cancel in-flight model-relay stream if we own it.
	h.mu.Lock()
	cancel, ok := h.cancels[msgID]
	h.mu.Unlock()
	if ok {
		cancel()
	}
	// Mark paused regardless (covers stale messages from prior process).
	if _, err := h.Store.UpdateMessage(r.Context(), UpdateMessageInput{
		UserID:    userID,
		MessageID: msgID,
		Status:    pStr(StatusPaused),
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError,
			"cancel_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// fail is retained for non-SSE callers that pass a custom emit func.
// In-tree HandleSend uses failV2 directly.
func (h *HTTPSender) fail(ctx context.Context, msgID, userID uuid.UUID,
	reason string, emit func(string, any),
) {
	_ = ctx
	_, _ = h.Store.UpdateMessage(context.Background(), UpdateMessageInput{
		UserID:    userID,
		MessageID: msgID,
		Status:    pStr(StatusError),
		ErrorMsg:  &reason,
	})
	emit("error", map[string]any{"message": reason})
}

// ─── model-relay SSE client ─────────────────────────────────────────────
//
// Mirrors services/runtime/internal/relayclient — Brain Chat now needs
// tool_call frames too once the agent loop (chat/agent.go) is wired.
// Kept inline rather than imported because hubclient lives under the
// runtime module's `internal/` and is intentionally not exported.

type hubReq struct {
	Model         string       `json:"model"`
	System        string       `json:"system,omitempty"`
	Messages      []hubMessage `json:"messages"`
	Tools         []hubTool    `json:"tools,omitempty"`
	Stream        bool         `json:"stream"`
	MaxTokens     int          `json:"max_tokens,omitempty"`
	Temperature   *float64     `json:"temperature,omitempty"`
	TopP          *float64     `json:"top_p,omitempty"`
	StopSequences []string     `json:"stop_sequences,omitempty"`
}

// hubTool is the tool definition forwarded to the model-relay → Provider.
type hubTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// hubToolCall represents one assistant-emitted tool_use block, ready
// to send back as part of an assistant turn in the next request.
type hubToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type hubMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	// Parts: optional multimodal content forwarded to model-relay →
	// adaptor as Anthropic-shape content blocks (text + image).
	// When set, the adaptor uses these instead of Content.
	Parts      json.RawMessage `json:"parts,omitempty"`
	ToolCalls  []hubToolCall   `json:"tool_calls,omitempty"`
	// ToolCallID set on role=tool messages to pair with a prior
	// tool_use block.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type hubFrameKind int

const (
	frameDelta hubFrameKind = iota
	frameToolCallStart
	frameToolCallArgs
	frameToolCallEnd
	frameThinking
	frameStop
	frameEnd
	frameErr
)

type hubFrame struct {
	Kind             hubFrameKind
	Text             string
	Stop             string
	Err              error
	PromptTokens     int
	CompletionTokens int
	// Tool-call frames:
	ToolID    string
	ToolName  string // KindToolCallStart only
	ArgsDelta string // KindToolCallArgs partial JSON
}

func (h *HTTPSender) callHubStream(ctx context.Context, body hubReq, bearer string,
) (<-chan hubFrame, error) {
	body.Stream = true
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.RelayURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("model-relay %d: %s", resp.StatusCode, string(buf))
	}

	out := make(chan hubFrame, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		// model-relay SSE protocol — same shape as runtime/hubclient: lines of
		// "event: <name>" / "data: <json>" separated by blank lines.
		// Events: delta / tool_call_start / tool_call_args /
		// tool_call_end / stop / end / error.
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		var event, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if event == "" {
					continue
				}
				h.dispatchHubFrame(event, data, out)
				event, data = "", ""
			}
		}
		if err := scanner.Err(); err != nil {
			out <- hubFrame{Kind: frameErr, Err: err}
		}
	}()
	return out, nil
}

func (h *HTTPSender) dispatchHubFrame(event, data string, out chan<- hubFrame) {
	switch event {
	case "delta":
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{Kind: frameDelta, Text: p.Text}
	case "tool_call_start":
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{
			Kind:     frameToolCallStart,
			ToolID:   p.ID,
			ToolName: p.Name,
		}
	case "tool_call_args":
		var p struct {
			ID    string `json:"id"`
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{
			Kind:      frameToolCallArgs,
			ToolID:    p.ID,
			ArgsDelta: p.Delta,
		}
	case "tool_call_end":
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{Kind: frameToolCallEnd, ToolID: p.ID}
	case "thinking":
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{Kind: frameThinking, Text: p.Text}
	case "stop":
		var p struct {
			Reason           string `json:"reason"`
			Usage            struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		}
		_ = json.Unmarshal([]byte(data), &p)
		f := hubFrame{Kind: frameStop, Stop: p.Reason}
		if p.Usage.PromptTokens > 0 {
			f.PromptTokens = p.Usage.PromptTokens
			f.CompletionTokens = p.Usage.CompletionTokens
		} else {
			f.PromptTokens = p.PromptTokens
			f.CompletionTokens = p.CompletionTokens
		}
		out <- f
	case "end":
		out <- hubFrame{Kind: frameEnd}
	case "error":
		var p struct{ Message string `json:"message"` }
		_ = json.Unmarshal([]byte(data), &p)
		out <- hubFrame{Kind: frameErr, Err: errors.New(p.Message)}
	}
}

// ─── Helpers ────────────────────────────────────────

// buildHubMessages assembles the model input from history. v0.1 is
// dumb: take the last `min(MinHistory, len)` messages, keep going
// while we're under budget. Real summarization (§3.7) is Phase 3.
func buildHubMessages(history []*Message, minN, budget int) []hubMessage {
	out := make([]hubMessage, 0, len(history))
	// Crude approximation: 4 chars ≈ 1 token. Good enough for budget
	// gating; true counting requires per-model tokenizer.
	used := 0
	// Iterate oldest → newest so order is preserved.
	for _, m := range history {
		if m.Role == RoleSystem {
			continue
		}
		if m.Status != StatusSuccess {
			// Skip incomplete / errored messages
			continue
		}
		approx := len(m.Content) / 4
		if used+approx > budget && len(out) >= minN {
			break
		}
		hm := hubMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		// Forward multimodal parts when present and non-empty. model-relay
		// anthropic adapter prefers Parts over Content. We only
		// forward parts that already contain non-text blocks
		// (image/file) — pure text parts are redundant with
		// `Content` and adding them just inflates payload size.
		if len(m.Parts) > 0 && partsHaveMultimodal(m.Parts) {
			hm.Parts = json.RawMessage(m.Parts)
		}
		out = append(out, hm)
		used += approx
	}
	return out
}

// partsHaveMultimodal reports whether the JSON parts array contains
// any non-text block (image, file, etc.). Pure text-only parts are
// redundant with hubMessage.Content and skipped to keep payloads
// small.
func partsHaveMultimodal(parts []byte) bool {
	var arr []map[string]any
	if err := json.Unmarshal(parts, &arr); err != nil {
		return false
	}
	for _, p := range arr {
		if t, _ := p["type"].(string); t != "" && t != "text" {
			return true
		}
	}
	return false
}

// textFromParts extracts the concatenated text from a parts JSON array.
// Used as fallback when the request supplied only parts (no flat content).
func textFromParts(parts []byte) string {
	var arr []map[string]any
	if err := json.Unmarshal(parts, &arr); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range arr {
		if t, ok := p["type"].(string); ok && t == "text" {
			if s, ok := p["text"].(string); ok {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

func strFallback(primary *string, fallback *string) string {
	if primary != nil && *primary != "" {
		return *primary
	}
	if fallback != nil {
		return *fallback
	}
	return ""
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func valOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
