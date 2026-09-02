package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// AgentLoop drives a multi-turn LLM conversation with tool-call
// round-trips. It's the cloud-side counterpart to the Flutter agent
// loop that W7 will add for client-mode threads.
//
// 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6 (cloud
// agent loop) and §3.2 (ChunkType protocol).
//
// Loop:
//
//	build messages → model-relay stream
//	  ├── text deltas       → emitter.TextDelta
//	  ├── tool_use blocks   → emitter.ToolStarted / Invoke / ToolCompleted
//	  └── stop_reason
//	if stop_reason == tool_use → append tool_use + tool_result, loop
//	else                       → done
//
// When the registry has zero cloud-runtime tools the loop degenerates
// to a single LLM turn and Run reduces to the legacy "stream text and
// persist" path. So flipping the agent on costs nothing for threads
// that haven't opted into tools yet.
type AgentLoop struct {
	Relay    *HTTPSender // Reuses HTTPSender.callHubStream + bearer/byok plumbing.
	Registry *tools.Registry
	MaxTurns int // safety cap; 0 → 8

	// RetrievalBudget (P2 #19) caps retrieval-class tool calls
	// (tools.Tool.Retrieval) per run, independently of MaxTurns. 0 →
	// no retrieval budget (default; plain chat keeps prior behaviour).
	// The wiki agent run wires mode tiers fast=2/standard=4/deep=6 —
	// see wiki/api wikiAgentRetrievalBudget. When active, the loop also
	// rejects duplicate retrieval signatures and early-stops after
	// NoYieldStreakLimit consecutive empty results (retrieval_guard.go).
	RetrievalBudget int
	// NoYieldStreakLimit is the consecutive-empty-results threshold for
	// early stop. 0 → 3 (only meaningful when RetrievalBudget > 0).
	NoYieldStreakLimit int

	// ChatToolAllowlist is the chat-mode tool whitelist (Q1, Runtime v3
	// §4). When non-nil it default-denies: both kernels (Run / RunV2)
	// advertise only tools whose name is in the set. nil = no restriction
	// (kernel-mechanics tests). Production sets it to
	// tools.DefaultChatToolAllowlist — this is a chat kernel, so the gate
	// always applies in prod. See tools/chatmode.go.
	ChatToolAllowlist map[string]struct{}
}

// NewAgentLoop wires defaults. Registry may be nil — that disables
// tool-use entirely while still streaming text via the same code path.
func NewAgentLoop(relay *HTTPSender, reg *tools.Registry) *AgentLoop {
	return &AgentLoop{Relay: relay, Registry: reg, MaxTurns: 8}
}

// AgentRunInput collects everything the loop needs for one user turn.
type AgentRunInput struct {
	Bearer    string
	Model     string
	System    string
	Mode      tools.ExecutionMode
	History   []hubMessage // user/assistant/tool history including current user msg
	MaxTokens int
	// Sampling params — nil pointers leave the upstream provider's
	// own default (chat-side UI tends to leave temperature/top_p
	// unset for everyday convos and only override when needed).
	Temperature   *float64
	TopP          *float64
	StopSequences []string

	// Emitter receives ChunkType v2 events as the loop progresses.
	Emitter *BlockEmitter
}

// AgentRunResult summarises the run for persistence.
type AgentRunResult struct {
	StopReason       string
	PromptTokens     int
	CompletionTokens int
}

// Run executes the loop until the model emits a non-tool stop reason
// or MaxTurns is reached. Stream events flow into the supplied
// BlockEmitter; this function does not touch the database — the
// caller (HandleSend) owns persistence at the end.
func (a *AgentLoop) Run(ctx context.Context, in AgentRunInput) (*AgentRunResult, error) {
	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}

	// Tool catalog filtered by execution mode. Empty when registry is
	// nil or no tools registered.
	var hubTools []hubTool
	if a.Registry != nil {
		// Chat-mode whitelist (default-deny when ChatToolAllowlist set).
		for _, d := range tools.FilterChatAllowed(a.Registry.Available(in.Mode), a.ChatToolAllowlist) {
			ht := hubTool{
				Name:        d.Name,
				Description: d.Description,
			}
			if len(d.InputSchema) > 0 {
				var params map[string]any
				if err := json.Unmarshal(d.InputSchema, &params); err == nil {
					ht.Parameters = params
				}
			}
			hubTools = append(hubTools, ht)
		}
	}

	history := append([]hubMessage(nil), in.History...)
	result := &AgentRunResult{}

	// P2 #19: per-run retrieval budget guard. Active only when the caller
	// opted in via RetrievalBudget; nil otherwise (invoke skips it).
	var guard *retrievalGuard
	if a.RetrievalBudget > 0 && a.Registry != nil {
		guard = newRetrievalGuard(a.RetrievalBudget, a.NoYieldStreakLimit)
	}

	for turn := 0; turn < maxTurns; turn++ {
		req := hubReq{
			Model:         in.Model,
			System:        in.System,
			Messages:      history,
			Tools:         hubTools,
			MaxTokens:     in.MaxTokens,
			Temperature:   in.Temperature,
			TopP:          in.TopP,
			StopSequences: in.StopSequences,
		}

		stream, err := a.Relay.callHubStream(ctx, req, in.Bearer)
		if err != nil {
			return result, fmt.Errorf("agent: model-relay call: %w", err)
		}

		assembled, calls, stop, ptok, ctok, streamErr := a.consumeStream(ctx, stream, in.Emitter)
		if streamErr != nil {
			return result, streamErr
		}
		result.StopReason = stop
		if ptok > 0 {
			result.PromptTokens = ptok
		}
		if ctok > 0 {
			result.CompletionTokens = ctok
		}

		// Always record this assistant turn — even tool-use turns —
		// so the next request gives the model the same view it just
		// produced. Without this, the LLM gets confused about whose
		// tool_use_id we're answering.
		assistant := hubMessage{Role: "assistant", Content: assembled}
		assistant.ToolCalls = calls
		history = append(history, assistant)

		// Done when the model didn't call any tool.
		if len(calls) == 0 || stop != "tool_use" {
			return result, nil
		}

		// Execute every tool the model requested, in order, append a
		// tool_result message each time, and let the loop continue.
		for _, c := range calls {
			toolResult, toolErr := a.invoke(ctx, in.Emitter, c, in.Mode, guard)
			history = append(history, hubMessage{
				Role:       "tool",
				ToolCallID: c.ID,
				Content:    toolResult,
			})
			_ = toolErr // already surfaced through Emitter.ToolFailed
		}
	}

	// Hit the turn cap — surface as an explicit stop reason so the
	// caller can choose to flag the message rather than silently
	// truncate.
	result.StopReason = "max_turns"
	return result, nil
}

// consumeStream drains one model-relay turn, emitting text deltas through the
// BlockEmitter and bookkeeping any tool calls the model issues. The
// returned `calls` slice has assembled JSON inputs ready for invoke.
func (a *AgentLoop) consumeStream(ctx context.Context, stream <-chan hubFrame,
	emitter *BlockEmitter,
) (text string, calls []hubToolCall, stop string,
	prompt, completion int, err error,
) {
	var assembled strings.Builder
	// argsBuf indexed by tool call id; each tool_call_args frame
	// appends partial JSON.
	argsBuf := map[string]*strings.Builder{}
	callOrder := []string{}
	callsByID := map[string]*hubToolCall{}

	for f := range stream {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		default:
		}

		switch f.Kind {
		case frameDelta:
			assembled.WriteString(f.Text)
			emitter.TextDelta(f.Text)
		case frameThinking:
			// Reasoning text — separate channel so the UI can fold it.
			// Not appended to `assembled` because the assistant turn
			// stored in history is the answer, not the reasoning.
			emitter.ThinkingDelta(f.Text)
		case frameToolCallStart:
			callsByID[f.ToolID] = &hubToolCall{ID: f.ToolID, Name: f.ToolName}
			callOrder = append(callOrder, f.ToolID)
			argsBuf[f.ToolID] = &strings.Builder{}
			// Close any in-flight text block — model is switching
			// channels. The emitter handles idempotency.
			emitter.CloseActiveText()
		case frameToolCallArgs:
			if buf, ok := argsBuf[f.ToolID]; ok {
				buf.WriteString(f.ArgsDelta)
			}
		case frameToolCallEnd:
			if tc, ok := callsByID[f.ToolID]; ok {
				if buf := argsBuf[f.ToolID]; buf != nil {
					tc.Input = json.RawMessage(buf.String())
				}
			}
		case frameStop:
			stop = f.Stop
			if f.PromptTokens > 0 {
				prompt = f.PromptTokens
				completion = f.CompletionTokens
			}
		case frameEnd:
			// natural end of stream
		case frameErr:
			err = f.Err
			return
		}
	}

	text = assembled.String()
	calls = make([]hubToolCall, 0, len(callOrder))
	for _, id := range callOrder {
		if tc, ok := callsByID[id]; ok {
			// Defensive: empty input JSON becomes "{}" so downstream
			// json.Unmarshal in the tool doesn't choke.
			if len(tc.Input) == 0 {
				tc.Input = json.RawMessage("{}")
			}
			calls = append(calls, *tc)
		}
	}
	return
}

// invoke runs one tool call through the registry, emits ToolStarted +
// ToolCompleted/ToolFailed events, and returns the JSON-encoded
// tool_result payload to feed back to the model. Errors are folded
// into the result string so the model can recover (e.g. by trying a
// different tool) rather than aborting the loop.
//
// guard (P2 #19, may be nil) gates retrieval-class calls: duplicate /
// over-budget / no-yield rejections are surfaced as ToolFailed steps
// (visible in the client's tool-step UI) and fed back as error text so
// the model wraps up instead of looping on search.
func (a *AgentLoop) invoke(ctx context.Context, emitter *BlockEmitter,
	c hubToolCall, mode tools.ExecutionMode, guard *retrievalGuard,
) (string, error) {
	// Surface to the user before we run, so a slow tool shows
	// "calling…" immediately rather than waiting on the result.
	var inputView any
	_ = json.Unmarshal(c.Input, &inputView) // best-effort; show raw if it fails
	if inputView == nil {
		inputView = string(c.Input)
	}
	blockID := emitter.ToolStarted(c.Name, inputView)

	start := time.Now()
	if a.Registry == nil {
		msg := fmt.Sprintf("tool %q unavailable: registry not configured", c.Name)
		emitter.ToolFailed(blockID, msg, time.Since(start).Milliseconds())
		return msg, errors.New(msg)
	}

	// Defense-in-depth: even if the model emits a tool it was never
	// advertised, reject anything outside the chat whitelist. nil
	// allowlist = no restriction (mechanics tests / non-chat callers).
	if a.ChatToolAllowlist != nil {
		if _, ok := a.ChatToolAllowlist[c.Name]; !ok {
			msg := fmt.Sprintf("tool %q not allowed in chat mode", c.Name)
			emitter.ToolFailed(blockID, msg, time.Since(start).Milliseconds())
			return msg, errors.New(msg)
		}
	}

	isRetrieval := guard != nil && a.Registry.IsRetrieval(c.Name)
	if isRetrieval {
		if msg := guard.check(c.Name, c.Input); msg != "" {
			emitter.ToolFailed(blockID, msg, time.Since(start).Milliseconds())
			return fmt.Sprintf("error: %s", msg), errors.New(msg)
		}
	}

	result, err := a.Registry.Invoke(ctx, mode, c.Name, c.Input)
	dur := time.Since(start).Milliseconds()
	if isRetrieval {
		guard.record(c.Name, c.Input, result, err)
	}
	if err != nil {
		emitter.ToolFailed(blockID, err.Error(), dur)
		// Feed the error back to the model — that's how it learns
		// to recover (try different args, fall back to text, etc).
		return fmt.Sprintf("error: %s", err.Error()), err
	}

	// Marshal whatever the tool returned to JSON for the LLM. Strings
	// pass through verbatim so simple tools stay simple.
	var resultStr string
	switch v := result.(type) {
	case nil:
		resultStr = ""
	case string:
		resultStr = v
	default:
		b, mErr := json.Marshal(v)
		if mErr != nil {
			resultStr = fmt.Sprintf("%v", v)
		} else {
			resultStr = string(b)
		}
	}
	emitter.ToolCompleted(blockID, result, dur)
	return resultStr, nil
}
