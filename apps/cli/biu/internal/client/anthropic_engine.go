// AnthropicEngineProvider — adapter from internal/engine.Provider to
// the Anthropic Messages API (https://api.anthropic.com/v1/messages).
//
// Distinct from the existing Anthropic chat adapter (internal/client/
// direct/anthropic.go) because the engine path needs:
//
//   1. Tool catalog in the request body (`tools` field, with
//      JSON-Schema input_schema each).
//   2. Multi-content-block messages — tool_use, tool_result, image —
//      not just plain text.
//   3. Native engine.StreamFrame output so the engine.ParseStream
//      consumer doesn't have to translate.
//
// We keep the legacy adapter so the existing chat REPL keeps working
// while the engine path is developed in parallel.
//
// We deliberately omit cache_control + thinking + betas in this first
// cut — those are P1 polish.

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
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

const (
	anthropicEngineDefaultURL = "https://api.anthropic.com"
	anthropicEngineVersion    = "2023-06-01"
)

// AnthropicEngineProvider implements engine.Provider.
type AnthropicEngineProvider struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client

	// AuthScheme controls how credentials reach the upstream.
	//   "x-api-key" — Anthropic platform default (header `x-api-key`)
	//   "bearer"    — BiuMind model-relay / OpenAI-compatible proxies that
	//                 want `Authorization: Bearer <token>`
	// Empty string defaults to "x-api-key" so existing callers keep
	// working unchanged.
	AuthScheme string

	// ExtraHeaders are stamped on every outbound /v1/messages request
	// after the standard auth/content-type/version headers. Used by
	// daemon Agent mode to attach `X-Biumind-LLM-Key` / `X-Biumind-LLM-Base-Url`
	// so model-relay's BYOK fast-path picks the user's per-thread provider
	// credentials instead of the platform pool.
	//
	// Direct mode (UseRelayAuth=false) typically leaves this empty —
	// nothing in api.anthropic.com reads custom Biumind headers.
	ExtraHeaders map[string]string
}

// NewAnthropicEngine wires an HTTP client with a generous read timeout
// (streaming responses can be long). Caller supplies APIKey + optional
// BaseURL override (e.g. internal proxies).
func NewAnthropicEngine(apiKey, baseURL string) *AnthropicEngineProvider {
	return &AnthropicEngineProvider{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 0},
	}
}

// NewRelayEngine targets the BiuMind model-relay's `/v1/messages` endpoint with
// Bearer auth. Wire-compatible with the Anthropic Messages API on the
// request side because model-relay forwards verbatim, so we reuse the
// AnthropicEngineProvider with a different auth scheme.
func NewRelayEngine(endpoint, bearerToken string) *AnthropicEngineProvider {
	return &AnthropicEngineProvider{
		APIKey:     bearerToken,
		BaseURL:    strings.TrimRight(endpoint, "/"),
		HTTP:       &http.Client{Timeout: 0},
		AuthScheme: "bearer",
	}
}

// ─── Outbound request shape ────────────────────────────
//
// Prompt cache strategy (see promptcache.go):
//
//   * `system` is sent as []anthropicSystemBlock so we can put a
//     cache_control marker on stable segments without losing the
//     dynamic tail.
//   * `tools` carry NO cache_control. They cache implicitly via the
//     Anthropic API's prefix matching, anchored by markers further
//     down. Stability comes from sorted-by-name registration.
//   * `messages` get exactly ONE message-level cache_control marker,
//     placed on the LAST message's last content block. The marker
//     rotates each turn so the previous turn's KV cache pages get
//     freed cleanly.

type anthropicReq struct {
	Model     string                 `json:"model"`
	System    []anthropicSystemBlock `json:"system,omitempty"`
	Messages  []anthropicReqMessage  `json:"messages"`
	Tools     []anthropicReqTool     `json:"tools,omitempty"`
	MaxTokens int                    `json:"max_tokens"`
	Stream    bool                   `json:"stream"`
}

// anthropicSystemBlock is one entry in the multi-block system array.
// Each block is plain text; only the stable (cacheable) ones get a
// cache_control marker.
type anthropicSystemBlock struct {
	Type         string        `json:"type"` // always "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`          // always "ephemeral" — TTL via separate field
	TTL  string `json:"ttl,omitempty"` // "5m" (default) or "1h" — opt-in
}

type anthropicReqMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content any    `json:"content"` // string OR []block
}

type anthropicReqTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// translateMessages converts state.Message into the Anthropic wire
// shape. Most assistant turns end up as plain string content; only
// when the message has tool_use or tool_result blocks (or is a
// tool-result-bearing user turn) do we emit the block array form.
func translateMessages(in []state.Message) []anthropicReqMessage {
	out := make([]anthropicReqMessage, 0, len(in))
	for _, m := range in {
		// Skip system messages — they go in the top-level `system`
		// field, not the messages array.
		if m.Role == state.RoleSystem {
			continue
		}
		role := string(m.Role)
		// Anthropic recognises only "user" and "assistant"; tool_result
		// rides inside a "user" message per the spec.
		if role == string(state.RoleToolResult) {
			role = "user"
		}

		// Decide flat-string vs block-array form.
		needsBlocks := false
		for _, b := range m.Content {
			if b.Type != state.ContentText {
				needsBlocks = true
				break
			}
		}
		if !needsBlocks && len(m.Content) == 1 {
			// Flat string fast-path keeps wire compact + matches what
			// older Anthropic-compatible servers expect.
			out = append(out, anthropicReqMessage{
				Role: role, Content: m.Content[0].Text,
			})
			continue
		}
		blocks := make([]map[string]any, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, contentBlockToWire(b))
		}
		out = append(out, anthropicReqMessage{Role: role, Content: blocks})
	}
	return out
}

// contentBlockToWire mirrors the per-block shape Anthropic expects.
func contentBlockToWire(b state.ContentBlock) map[string]any {
	switch b.Type {
	case state.ContentText:
		return map[string]any{"type": "text", "text": b.Text}
	case state.ContentToolUse:
		return map[string]any{
			"type":  "tool_use",
			"id":    b.ToolUseID,
			"name":  b.ToolUseName,
			"input": b.ToolUseInput,
		}
	case state.ContentToolResult:
		// Per claude.ts:1894-1902 — when is_error is true, content must
		// be text-only. The engine's softError() already produces a
		// single text block, so we don't need to sanitize again here.
		var content any = b.ToolResultContent
		if len(b.ToolResultContent) == 1 &&
			b.ToolResultContent[0].Type == state.ContentText {
			// Compact form: string content for single-text results.
			content = b.ToolResultContent[0].Text
		} else {
			// Block-array form (multi-block or non-text results).
			arr := make([]map[string]any, 0, len(b.ToolResultContent))
			for _, inner := range b.ToolResultContent {
				arr = append(arr, contentBlockToWire(inner))
			}
			content = arr
		}
		out := map[string]any{
			"type":        "tool_result",
			"tool_use_id": b.ToolResultID,
			"content":     content,
		}
		if b.ToolResultIsError {
			out["is_error"] = true
		}
		return out
	case state.ContentImage:
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": b.ImageMimeType,
				"data":       b.ImageData,
			},
		}
	}
	return map[string]any{"type": "text", "text": ""}
}

// systemTextFromMessages pulls any state.RoleSystem messages out and
// joins them. The engine doesn't currently inject system messages
// into state, but compact / memory layers eventually will, so we
// honor it.
func systemTextFromMessages(req engine.StreamRequest) string {
	out := req.System
	for _, m := range req.Messages {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText {
				if out != "" {
					out += "\n\n"
				}
				out += b.Text
			}
		}
	}
	return out
}

// SystemDynamicBoundary is a sentinel string callers can embed in the
// system prompt to mark the static/dynamic split. Everything BEFORE
// the boundary is cached; everything AFTER is per-turn variable
// (cwd, timestamp, current branch, …).
//
// Keep verbatim — changing this string busts caches in any session
// that pinned the previous value.
const SystemDynamicBoundary = "<!-- biu:system-dynamic-boundary -->"

// splitSystem turns a flat system string into the multi-block array
// that goes on the wire. Each block decides for itself whether it
// gets a cache_control marker.
//
// Strategy:
//
//   - No boundary present → one block, marked cacheable. The whole
//     prompt is treated as static. The most common case for biu
//     today (CLI prefix + BIUMIND.md is all static).
//   - Boundary present → two blocks: static prefix marked cacheable,
//     dynamic suffix unmarked. Caller can use this to pin per-turn
//     metadata without busting the cache for the rest of the prompt.
//
// Empty input returns nil so we don't waste a block on `system: [""]`.
func splitSystem(text string) []anthropicSystemBlock {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	idx := strings.Index(text, SystemDynamicBoundary)
	if idx < 0 {
		return []anthropicSystemBlock{{
			Type: "text", Text: text,
			CacheControl: &cacheControl{Type: "ephemeral"},
		}}
	}
	staticPart := strings.TrimSpace(text[:idx])
	dynamicPart := strings.TrimSpace(text[idx+len(SystemDynamicBoundary):])
	out := make([]anthropicSystemBlock, 0, 2)
	if staticPart != "" {
		out = append(out, anthropicSystemBlock{
			Type: "text", Text: staticPart,
			CacheControl: &cacheControl{Type: "ephemeral"},
		})
	}
	if dynamicPart != "" {
		// No cache_control on the dynamic tail.
		out = append(out, anthropicSystemBlock{
			Type: "text", Text: dynamicPart,
		})
	}
	return out
}

// markLastMessageForCache adds exactly one message-level cache_control
// marker to the very last block of the very last message. If the last
// message uses the flat-string content form, we promote it to a
// block-array first so we have a place to attach the marker.
//
// A single marker is placed on the rotating "last message" position
// because:
//
//  1. The Anthropic API only allows a small fixed number of
//     message-level markers per request — wasting them on internal
//     positions starves the rotation.
//  2. With one marker the previous turn's KV cache pages are freed
//     promptly; with two markers an internal position gets pinned and
//     bloats memory without ever being resumed from.
//
// `messages` is mutated in place.
func markLastMessageForCache(messages []anthropicReqMessage) {
	if len(messages) == 0 {
		return
	}
	last := &messages[len(messages)-1]
	switch c := last.Content.(type) {
	case string:
		// Promote to block array so we can attach cache_control.
		last.Content = []map[string]any{{
			"type": "text", "text": c,
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
	case []map[string]any:
		if len(c) == 0 {
			return
		}
		c[len(c)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
	}
}

// translateTools is a 1:1 copy of the engine.ToolSpec into the API
// shape. NO cache_control marker is attached — tools cache
// implicitly via prefix matching anchored by the
// system/messages markers. The contract for that to work is byte-for-
// byte stability of the tools array, which we enforce here by
// sorting by name.
func translateTools(tools []engine.ToolSpec) []anthropicReqTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicReqTool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		out = append(out, anthropicReqTool{
			Name: t.Name, Description: t.Description, InputSchema: schema,
		})
	}
	// Stable order is critical for cache hits — registry.List() uses
	// map iteration which Go intentionally randomises. Sort by name.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ─── Stream method ─────────────────────────────────────

// Stream is the engine.Provider entry point. It POSTs to /v1/messages
// with stream=true, reads the SSE response, and emits typed
// engine.StreamFrame values into a channel.
//
// Cancellation: ctx cancellation tears down the HTTP request so the
// goroutine can exit cleanly. The returned channel always closes.
//
// First-token errors (4xx / network) are surfaced as a normal Go
// error; the caller never gets a channel for them. Mid-stream errors
// (server emits an `event: error` SSE) are pushed into the channel as
// FrameError so the parser can decide whether to retry.
func (a *AnthropicEngineProvider) Stream(
	ctx context.Context,
	req engine.StreamRequest,
) (<-chan engine.StreamFrame, error) {
	if a.APIKey == "" {
		return nil, errors.New("anthropic-engine: missing api_key")
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	messages := translateMessages(req.Messages)
	// Single rotating cache_control marker on the last message —
	// this pins the prefix without polluting interior
	// positions. Skipped when there are no messages (initial probe).
	markLastMessageForCache(messages)

	body, err := json.Marshal(anthropicReq{
		Model:     req.Model,
		System:    splitSystem(systemTextFromMessages(req)),
		Messages:  messages,
		Tools:     translateTools(req.Tools),
		MaxTokens: maxTokens,
		Stream:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic-engine: marshal: %w", err)
	}

	base := a.BaseURL
	if base == "" {
		base = anthropicEngineDefaultURL
	}
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicEngineVersion)
	switch a.AuthScheme {
	case "bearer":
		httpReq.Header.Set("authorization", "Bearer "+a.APIKey)
		// 走 BiuMind model-relay /v1/messages 时，model-relay 默认输出
		// unified frame SSE（兼容 brain / hub.go RelayProvider）。这里
		// 显式声明客户端要 Anthropic 原生 SSE 格式（biumindkit 这层 SSE
		// parser 是 Anthropic 协议的），不然 stop_reason 永远空。
		// 直连 api.anthropic.com 时 server 不识别这个 header 会忽略，
		// 不影响兼容。
		httpReq.Header.Set("X-Stream-Format", "anthropic")
	default: // "" or "x-api-key"
		httpReq.Header.Set("x-api-key", a.APIKey)
	}
	httpReq.Header.Set("accept", "text/event-stream")
	for k, v := range a.ExtraHeaders {
		// 用户传的额外头(如 BYOK 路由用的 X-Biumind-LLM-Key)。在标准
		// header 之后 stamp,允许覆盖 — 调用方有责任不传 reserved name
		// (content-type / authorization 等) 否则等于自废武功。
		httpReq.Header.Set(k, v)
	}

	httpCl := a.HTTP
	if httpCl == nil {
		httpCl = http.DefaultClient
	}
	resp, err := httpCl.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic-engine: http: %w", err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic-engine: %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	// Spawn a goroutine to drain SSE → frames. The channel is
	// buffered enough to absorb a fast burst (token deltas can flood
	// in milliseconds during a busy generation).
	out := make(chan engine.StreamFrame, 64)
	go drainSSE(ctx, resp.Body, out)
	return out, nil
}

// drainSSE reads SSE event/data lines, decodes the JSON payload by the
// event name, and pushes engine.StreamFrame values into out. Closes
// out on EOF / context cancellation / decode error.
func drainSSE(ctx context.Context, body io.ReadCloser, out chan<- engine.StreamFrame) {
	defer body.Close()
	defer close(out)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var event, data string

	flush := func() {
		if event == "" {
			return
		}
		frame := decodeAnthropicEvent(event, data)
		if frame == nil {
			event, data = "", ""
			return
		}
		select {
		case out <- *frame:
		case <-ctx.Done():
		}
		event, data = "", ""
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
		// Lines starting with ":" are SSE comments / heartbeats — ignore.
		if ctx.Err() != nil {
			return
		}
	}
	// In case the server closes without a trailing blank line.
	flush()
}

// decodeAnthropicEvent maps a single (event, data) pair to an
// engine.StreamFrame. Returns nil for unknown / harmless events
// (ping, comments).
func decodeAnthropicEvent(event, data string) *engine.StreamFrame {
	// Some servers emit the SSE event name + a JSON object that
	// already contains a "type" matching the event name. We trust
	// the JSON body for the discriminator and use the SSE name as a
	// fallback for sanity.
	type wireBlock struct {
		Type  string         `json:"type"`
		ID    string         `json:"id,omitempty"`
		Name  string         `json:"name,omitempty"`
		Input map[string]any `json:"input,omitempty"`
		Text  string         `json:"text,omitempty"`
	}
	type wireDelta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		// Thinking — Anthropic 原生 thinking_delta 内容载体。开源推理
		// 模型 (glm-thinking / deepseek-r1 / qwen-r1) 经 model-relay
		// anthropic_stream 翻译成同样形态的 content_block_delta。
		// 没有 Thinking 字段时这些内容会被静默丢弃 → 客户端看不到。
		Thinking     string `json:"thinking,omitempty"`
		StopReason   string `json:"stop_reason,omitempty"`
		StopSequence string `json:"stop_sequence,omitempty"`
	}
	type wireUsage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	}
	type wireMessage struct {
		ID    string     `json:"id"`
		Model string     `json:"model"`
		Usage *wireUsage `json:"usage,omitempty"`
	}
	type wirePayload struct {
		Type         string       `json:"type"`
		Index        int          `json:"index"`
		Message      *wireMessage `json:"message,omitempty"`
		ContentBlock *wireBlock   `json:"content_block,omitempty"`
		Delta        *wireDelta   `json:"delta,omitempty"`
		Usage        *wireUsage   `json:"usage,omitempty"`
		Error        *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if data == "" {
		return nil
	}
	var p wirePayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		// Malformed line; surface as engine error frame.
		return &engine.StreamFrame{
			Type: engine.FrameError,
			Error: &engine.StreamError{
				Type: "decode_failure", Message: err.Error()},
		}
	}
	t := p.Type
	if t == "" {
		t = event
	}

	mapUsage := func(u *wireUsage) *engine.StreamUsage {
		if u == nil {
			return nil
		}
		return &engine.StreamUsage{
			InputTokens:              u.InputTokens,
			OutputTokens:             u.OutputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
		}
	}

	switch t {
	case "message_start":
		f := engine.StreamFrame{Type: engine.FrameMessageStart}
		if p.Message != nil {
			f.Message = &engine.StreamMessageHead{
				ID: p.Message.ID, Model: p.Message.Model,
				Usage: mapUsage(p.Message.Usage),
			}
		}
		return &f
	case "content_block_start":
		f := engine.StreamFrame{Type: engine.FrameContentBlockStart, Index: p.Index}
		if p.ContentBlock != nil {
			f.ContentBlock = &engine.StreamBlockHead{
				Type:  p.ContentBlock.Type,
				ID:    p.ContentBlock.ID,
				Name:  p.ContentBlock.Name,
				Input: p.ContentBlock.Input,
			}
		}
		return &f
	case "content_block_delta":
		f := engine.StreamFrame{Type: engine.FrameContentBlockDelta, Index: p.Index}
		if p.Delta != nil {
			f.Delta = &engine.StreamDelta{
				Type:        p.Delta.Type,
				Text:        p.Delta.Text,
				PartialJSON: p.Delta.PartialJSON,
				Thinking:    p.Delta.Thinking,
			}
		}
		return &f
	case "content_block_stop":
		return &engine.StreamFrame{Type: engine.FrameContentBlockStop, Index: p.Index}
	case "message_delta":
		f := engine.StreamFrame{Type: engine.FrameMessageDelta}
		if p.Delta != nil {
			f.Delta = &engine.StreamDelta{
				StopReason:   p.Delta.StopReason,
				StopSequence: p.Delta.StopSequence,
			}
		}
		f.Usage = mapUsage(p.Usage)
		return &f
	case "message_stop":
		return &engine.StreamFrame{Type: engine.FrameMessageStop}
	case "ping":
		return &engine.StreamFrame{Type: engine.FramePing}
	case "error":
		f := engine.StreamFrame{Type: engine.FrameError}
		if p.Error != nil {
			f.Error = &engine.StreamError{
				Type: p.Error.Type, Message: p.Error.Message,
			}
		}
		return &f
	}
	return nil
}

// Compile-time check that we satisfy the engine.Provider contract.
var _ engine.Provider = (*AnthropicEngineProvider)(nil)

// silence unused warning during early development if any field stays
// unreferenced. Removed once cache_control wiring lands.
var _ = time.Time{}
