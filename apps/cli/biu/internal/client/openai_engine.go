// openai_engine.go — adapter from internal/engine.Provider to any
// OpenAI-compatible /v1/chat/completions upstream.
//
// Same shape works for OpenAI, Azure OpenAI, DeepSeek, vLLM, llama.cpp,
// OpenRouter, Together, SiliconFlow, GLM, new-api, Google-via-OpenAI-
// compat-proxy, etc. — point BaseURL at the host.
//
// Distinct from AnthropicEngineProvider because the wire shapes diverge:
//   * No top-level `system` field — system folds into a leading
//     role=system message.
//   * tool_use ↔ tool_calls (assistant), tool_result ↔ role=tool.
//   * Streaming has no explicit block boundaries — we synthesise the
//     Anthropic content_block_{start,delta,stop} lifecycle from the
//     flat OpenAI delta stream (see drainOpenAISSE state machine).
//
// engine.ParseStream (stream.go:127) is vendor-agnostic and consumes
// Anthropic-shape engine.StreamFrame, so this adapter's job is to
// normalise OpenAI SSE into that shape. state.go:120-122 documents
// this boundary; stream.go:20-24 explicitly reserves this file.
//
// Blueprint (translation logic, NOT imported — different module):
// services/model-relay/internal/relay/provider/openai/chat.go

package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

const openAIDefaultBaseURL = "https://api.openai.com"

// OpenAIEngineProvider implements engine.Provider against any
// OpenAI-compatible /v1/chat/completions upstream.
type OpenAIEngineProvider struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client

	// ExtraHeaders stamped after standard auth/content-type headers.
	ExtraHeaders map[string]string
}

// NewOpenAIEngine wires an HTTP client with no read timeout (streaming
// can be long). baseURL may be bare ("https://host") or include /v1 —
// normalizeOpenAIBaseURL strips the suffix so we don't double it.
func NewOpenAIEngine(apiKey, baseURL string) *OpenAIEngineProvider {
	return &OpenAIEngineProvider{
		APIKey:  apiKey,
		BaseURL: normalizeOpenAIBaseURL(baseURL),
		HTTP:    &http.Client{Timeout: 0},
	}
}

// normalizeOpenAIBaseURL mirrors model-relay's provider.NormalizeBaseURL
// so users who paste "https://host/v1" don't get /v1/v1/chat/completions.
func normalizeOpenAIBaseURL(s string) string {
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/v1")
	return s
}

// Compile-time check that we satisfy the engine.Provider contract.
var _ engine.Provider = (*OpenAIEngineProvider)(nil)

// ─── Outbound request shape ───────────────────────────────────────────

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // always "function"
	Function openAIToolCallFunc `json:"function"`
}

type openAIToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string per OpenAI spec
}

type openAITool struct {
	Type     string             `json:"type"` // "function"
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Stream        bool                 `json:"stream,omitempty"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Tools         []openAITool         `json:"tools,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// translateMessagesOpenAI converts state.Message into OpenAI
// chat.completions messages. Rules (driven by engine/batch.go:146-160
// which stores tool_result as a RoleUser message of ContentToolResult
// blocks, NOT a separate role):
//   - RoleSystem → skipped (folded into leading system message by caller
//     via systemTextFromMessages).
//   - A user message with ContentToolResult blocks → one role=tool
//     message PER result block (OpenAI requirement). Non-tool_result
//     blocks (text/image) in the same message go out as a separate
//     preceding user message to preserve order.
//   - Assistant message with ContentToolUse blocks → role=assistant
//     with tool_calls[]; content gets the GLM-5.1 " " sentinel when
//     empty (empty content + tool_calls triggers upstream 400 code
//     1213 on the next tool_result round).
//   - Image blocks → content-parts array [{type:text},{type:image_url}].
//   - Otherwise → plain string content.
func translateMessagesOpenAI(in []state.Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(in)+1)
	for _, m := range in {
		if m.Role == state.RoleSystem {
			continue
		}
		if hasToolResult(m.Content) {
			appendNonToolResultBlocks(&out, m.Content, "user")
			for _, b := range m.Content {
				if b.Type != state.ContentToolResult {
					continue
				}
				out = append(out, openAIMessage{
					Role:       "tool",
					ToolCallID: b.ToolResultID,
					Content:    toolResultContentToString(b),
				})
			}
			continue
		}
		role := string(m.Role)
		if role == string(state.RoleToolResult) {
			role = "user" // defensive; batch.go uses RoleUser
		}
		toolUses := toolUseBlocks(m.Content)
		if role == "assistant" && len(toolUses) > 0 {
			content := joinTextBlocks(m.Content)
			if content == "" {
				content = " " // GLM-5.1 sentinel
			}
			msg := openAIMessage{Role: "assistant", Content: content}
			for _, tu := range toolUses {
				args, _ := json.Marshal(tu.ToolUseInput)
				if tu.ToolUseInput == nil || string(args) == "null" {
					args = []byte("{}")
				}
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					ID:   tu.ToolUseID,
					Type: "function",
					Function: openAIToolCallFunc{
						Name:      tu.ToolUseName,
						Arguments: string(args),
					},
				})
			}
			out = append(out, msg)
			continue
		}
		if hasImage(m.Content) {
			if parts := contentPartsOpenAI(m.Content); parts != nil {
				out = append(out, openAIMessage{Role: role, Content: parts})
				continue
			}
		}
		out = append(out, openAIMessage{Role: role, Content: joinTextBlocks(m.Content)})
	}
	return out
}

func hasToolResult(blocks []state.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == state.ContentToolResult {
			return true
		}
	}
	return false
}

func hasImage(blocks []state.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == state.ContentImage {
			return true
		}
	}
	return false
}

func toolUseBlocks(blocks []state.ContentBlock) []state.ContentBlock {
	var out []state.ContentBlock
	for _, b := range blocks {
		if b.Type == state.ContentToolUse {
			out = append(out, b)
		}
	}
	return out
}

func joinTextBlocks(blocks []state.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == state.ContentText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolResultContentToString flattens a tool_result's nested content
// blocks into a single string for the OpenAI role=tool message body.
// IsError with empty content → "[tool error]" (mirrors model-relay
// openai/chat.go:157-159).
func toolResultContentToString(b state.ContentBlock) string {
	if len(b.ToolResultContent) == 0 {
		if b.ToolResultIsError {
			return "[tool error]"
		}
		return ""
	}
	var parts []string
	for _, inner := range b.ToolResultContent {
		if inner.Type == state.ContentText && inner.Text != "" {
			parts = append(parts, inner.Text)
		}
	}
	s := strings.Join(parts, "\n")
	if s == "" && b.ToolResultIsError {
		return "[tool error]"
	}
	return s
}

// appendNonToolResultBlocks emits any text/image blocks (non tool_result)
// as a single message of the given role; no-op when none. Preserves
// order when a tool-result turn also carries text/image.
func appendNonToolResultBlocks(out *[]openAIMessage, blocks []state.ContentBlock, role string) {
	var nonTool []state.ContentBlock
	for _, b := range blocks {
		if b.Type != state.ContentToolResult {
			nonTool = append(nonTool, b)
		}
	}
	if len(nonTool) == 0 {
		return
	}
	if hasImage(nonTool) {
		if parts := contentPartsOpenAI(nonTool); parts != nil {
			*out = append(*out, openAIMessage{Role: role, Content: parts})
			return
		}
	}
	*out = append(*out, openAIMessage{Role: role, Content: joinTextBlocks(nonTool)})
}

// contentPartsOpenAI emits OpenAI multimodal content parts
// [{type:text,...},{type:image_url,...}] from canonical blocks.
// Returns nil if empty.
func contentPartsOpenAI(blocks []state.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case state.ContentText:
			if b.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": b.Text})
			}
		case state.ContentImage:
			media := b.ImageMimeType
			if media == "" {
				media = "image/png"
			}
			out = append(out, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + media + ";base64," + b.ImageData},
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// translateToolsOpenAI maps engine.ToolSpec → OpenAI function schema.
// input_schema (Anthropic) and parameters (OpenAI) are both JSON-Schema
// → 1:1. Sorted by name for wire stability (matches the
// anthropic_engine.go cache-prefix rationale).
func translateToolsOpenAI(tools []engine.ToolSpec) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(tools))
	for _, t := range tools {
		params := t.InputSchema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Function.Name < out[j].Function.Name
	})
	return out
}

// ─── Stream method ────────────────────────────────────────────────────

func (p *OpenAIEngineProvider) Stream(
	ctx context.Context, req engine.StreamRequest,
) (<-chan engine.StreamFrame, error) {
	if p.APIKey == "" {
		return nil, errors.New("openai-engine: missing api_key")
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	// ContextManagement (engine.go:71) is Anthropic-only — ignored here.

	sysText := systemTextFromMessages(req) // anthropic_engine.go:243, same package
	messages := translateMessagesOpenAI(req.Messages)
	if sysText != "" {
		messages = append([]openAIMessage{{Role: "system", Content: sysText}}, messages...)
	}

	body, err := json.Marshal(openAIRequest{
		Model:         req.Model,
		Messages:      messages,
		Tools:         translateToolsOpenAI(req.Tools),
		MaxTokens:     maxTokens,
		Stream:        true,
		StreamOptions: &openAIStreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("openai-engine: marshal: %w", err)
	}

	base := p.BaseURL
	if base == "" {
		base = openAIDefaultBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range p.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}

	httpCl := p.HTTP
	if httpCl == nil {
		httpCl = http.DefaultClient
	}
	resp, err := httpCl.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai-engine: http: %w", err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		return nil, fmt.Errorf("openai-engine: %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	out := make(chan engine.StreamFrame, 64)
	go drainOpenAISSE(ctx, resp.Body, out)
	return out, nil
}

// ─── Inbound: OpenAI SSE → engine.StreamFrame ─────────────────────────

// openAIStreamChunk is one SSE `data: {...}` payload from
// chat/completions. choices[].delta carries incremental content /
// reasoning / tool_calls; the terminal chunk carries finish_reason and
// (with stream_options.include_usage) a non-nil usage.
type openAIStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			// ReasoningContent — LiteLLM / new-api / 智谱 GLM / DeepSeek-R1 /
			// Qwen-r1 emit reasoning under this field in OpenAI-compat mode.
			ReasoningContent string `json:"reasoning_content,omitempty"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// openAIStreamState tracks the Anthropic block lifecycle synthesised
// from the flat OpenAI delta stream. Invariants:
//   - At most one of textOpen/thinkOpen is true (sequential, like Anthropic).
//   - Tool blocks live in their own index space, opened only after
//     closeContent() runs (text/thinking sealed first).
//   - nextIndex monotonically assigns Anthropic content-block indices.
type openAIStreamState struct {
	started   bool
	nextIndex int
	textOpen  bool
	textIdx   int
	thinkOpen bool
	thinkIdx  int
	tools     map[int]*openAIToolBlock
}

type openAIToolBlock struct {
	anthropicIdx int
	id           string
	name         string
}

func newOpenAIStreamState() *openAIStreamState {
	return &openAIStreamState{tools: map[int]*openAIToolBlock{}}
}

// closeContent seals whichever of text/thinking is open. Returns the
// stop frames to emit.
func (s *openAIStreamState) closeContent() []engine.StreamFrame {
	var f []engine.StreamFrame
	if s.textOpen {
		f = append(f, engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: s.textIdx})
		s.textOpen = false
	}
	if s.thinkOpen {
		f = append(f, engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: s.thinkIdx})
		s.thinkOpen = false
	}
	return f
}

func (s *openAIStreamState) ensureText() []engine.StreamFrame {
	if s.textOpen {
		return nil
	}
	var f []engine.StreamFrame
	// switching text←thinking: seal thinking first
	if s.thinkOpen {
		f = append(f, engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: s.thinkIdx})
		s.thinkOpen = false
	}
	s.textIdx = s.nextIndex
	s.nextIndex++
	f = append(f, engine.StreamFrame{
		Type:         engine.FrameContentBlockStart,
		Index:        s.textIdx,
		ContentBlock: &engine.StreamBlockHead{Type: "text"},
	})
	s.textOpen = true
	return f
}

func (s *openAIStreamState) ensureThinking() []engine.StreamFrame {
	if s.thinkOpen {
		return nil
	}
	var f []engine.StreamFrame
	if s.textOpen { // text→thinking transition (rare)
		f = append(f, engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: s.textIdx})
		s.textOpen = false
	}
	s.thinkIdx = s.nextIndex
	s.nextIndex++
	f = append(f, engine.StreamFrame{
		Type:         engine.FrameContentBlockStart,
		Index:        s.thinkIdx,
		ContentBlock: &engine.StreamBlockHead{Type: "thinking"},
	})
	s.thinkOpen = true
	return f
}

// mapFinishReasonOpenAI maps OpenAI finish_reason → Anthropic stop_reason.
// engine loop (engine.go:13-22) keys off end_turn / tool_use / max_tokens.
func mapFinishReasonOpenAI(fr string) string {
	switch fr {
	case "stop":
		return "end_turn"
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	default: // content_filter, unknown → safe terminate
		return "end_turn"
	}
}

// drainOpenAISSE reads OpenAI SSE `data:` lines, decodes each chunk,
// and pushes engine.StreamFrame values into out. Closes out on EOF /
// [DONE] / context cancellation / decode error.
//
// message_stop is emitted exactly once, at stream end ([DONE] or
// connection close), after any synthesised close + stop_reason. This
// keeps ParseStream's terminal branch (stream.go:263) firing correctly
// even when a provider omits finish_reason or usage.
func drainOpenAISSE(ctx context.Context, body io.ReadCloser, out chan<- engine.StreamFrame) {
	defer body.Close()
	defer close(out)

	send := func(frames ...engine.StreamFrame) bool {
		for _, f := range frames {
			select {
			case out <- f:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}

	st := newOpenAIStreamState()
	sawFinish := false

	// finish emits the close-then-stop_reason prefix; called from both
	// finish_reason and end-of-stream paths.
	finish := func(reason string) bool {
		if !send(st.closeContent()...) {
			return false
		}
		for idx, tb := range st.tools {
			if !send(engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: tb.anthropicIdx}) {
				return false
			}
			delete(st.tools, idx)
		}
		return send(engine.StreamFrame{
			Type:  engine.FrameMessageDelta,
			Delta: &engine.StreamDelta{StopReason: reason},
		})
	}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			if !sawFinish {
				if !finish("end_turn") {
					return
				}
				sawFinish = true
			}
			if st.started {
				_ = send(engine.StreamFrame{Type: engine.FrameMessageStop})
			}
			return
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			_ = send(engine.StreamFrame{
				Type:  engine.FrameError,
				Error: &engine.StreamError{Type: "decode_failure", Message: err.Error()},
			})
			return
		}

		if !st.started {
			st.started = true
			if !send(engine.StreamFrame{
				Type:    engine.FrameMessageStart,
				Message: &engine.StreamMessageHead{ID: chunk.ID, Model: chunk.Model},
			}) {
				return
			}
		}

		for _, c := range chunk.Choices {
			d := c.Delta
			if d.ReasoningContent != "" {
				if !send(st.ensureThinking()...) {
					return
				}
				if !send(engine.StreamFrame{
					Type:  engine.FrameContentBlockDelta,
					Index: st.thinkIdx,
					Delta: &engine.StreamDelta{Type: "thinking_delta", Thinking: d.ReasoningContent},
				}) {
					return
				}
			}
			if d.Content != "" {
				if !send(st.ensureText()...) {
					return
				}
				if !send(engine.StreamFrame{
					Type:  engine.FrameContentBlockDelta,
					Index: st.textIdx,
					Delta: &engine.StreamDelta{Type: "text_delta", Text: d.Content},
				}) {
					return
				}
			}
			for _, tc := range d.ToolCalls {
				tb := st.tools[tc.Index]
				if tb == nil {
					// entering tool_use — close any open text/thinking
					if !send(st.closeContent()...) {
						return
					}
					id := tc.ID
					if id == "" {
						id = fmt.Sprintf("call_%d", tc.Index)
					}
					tb = &openAIToolBlock{
						anthropicIdx: st.nextIndex,
						id:           id,
						name:         tc.Function.Name,
					}
					st.nextIndex++
					st.tools[tc.Index] = tb
					if !send(engine.StreamFrame{
						Type:         engine.FrameContentBlockStart,
						Index:        tb.anthropicIdx,
						ContentBlock: &engine.StreamBlockHead{Type: "tool_use", ID: tb.id, Name: tb.name, Input: map[string]any{}},
					}) {
						return
					}
				}
				if tc.Function.Arguments != "" {
					if !send(engine.StreamFrame{
						Type:  engine.FrameContentBlockDelta,
						Index: tb.anthropicIdx,
						Delta: &engine.StreamDelta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
					}) {
						return
					}
				}
			}
			if c.FinishReason != "" {
				if !sawFinish {
					if !finish(mapFinishReasonOpenAI(c.FinishReason)) {
						return
					}
					sawFinish = true
				}
			}
		}

		if chunk.Usage != nil {
			if !send(engine.StreamFrame{
				Type: engine.FrameMessageDelta,
				Usage: &engine.StreamUsage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}) {
				return
			}
		}
	}
	if err := sc.Err(); err != nil {
		_ = send(engine.StreamFrame{
			Type:  engine.FrameError,
			Error: &engine.StreamError{Type: "read_failure", Message: err.Error()},
		})
		return
	}
	// Server closed without [DONE] — graceful close.
	if !sawFinish {
		_ = finish("end_turn")
		sawFinish = true
	}
	if st.started {
		_ = send(engine.StreamFrame{Type: engine.FrameMessageStop})
	}
}
