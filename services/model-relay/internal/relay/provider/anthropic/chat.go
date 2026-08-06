// Package anthropic implements provider.Adaptor for Anthropic Messages API.
//
// Supports:
//   - Plain text round-trip (Phase 1)
//   - Streaming text deltas
//   - Tool use (Phase 2.1.5): tools in request, tool_use blocks in response,
//     tool_result blocks in subsequent user messages.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/model-relay/internal/relay/files"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
)

// Adaptor — Files resolver is optional. nil = no file_id support
// (parts containing source.type=file 会原样转发, Anthropic 会 400)。
// 接入路径在 model-relay main 里, 通过 NewWithResolver 注入。
type Adaptor struct {
	files files.Resolver
}

func New() *Adaptor { return &Adaptor{} }

// NewWithResolver — 注入 file resolver 后, parts 里的 source.type=file
// 会被替换成 source.type=url (presigned, 15 min); 拿不到 URL 时降级
// 为 base64 inline。
func NewWithResolver(r files.Resolver) *Adaptor {
	return &Adaptor{files: r}
}

func (a *Adaptor) Name() string { return "anthropic" }

// Capabilities — v0.3 全模态 ChatAdaptor 接口要求. anthropic 只 chat
// (没有 embed/speech/image 端点); 不会扩.
func (a *Adaptor) Capabilities() []string { return []string{"chat"} }

// ─── Request translation ──────────────────────────────────────────────

// anthropicMessage uses interface{} for Content because Anthropic accepts
// either a plain string or an array of content blocks.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// anthropicTool is the tool definition Anthropic expects.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicRequest struct {
	Model string `json:"model"`
	// System 透传：上游 Anthropic API 接受 string 或 [{type:text,text}] 数组。
	// 用 RawMessage 保 raw bytes，不强行转字符串（biumindkit 用块数组带
	// cache_control，转字符串会丢 cache 提示）。
	System        json.RawMessage    `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Tools         []anthropicTool    `json:"tools,omitempty"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

func (a *Adaptor) TranslateRequest(ctx context.Context, req *provider.Request, creds *provider.Credentials) (*http.Request, error) {
	upstream := anthropicRequest{
		Model:         req.Model,
		System:        req.System,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.StopSequences,
		Stream:        req.Stream,
	}
	if upstream.MaxTokens == 0 {
		upstream.MaxTokens = 4096
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if len(upstream.System) == 0 {
				// 老路径：role=system 拼到 system 字段。m.Content 是 RawMessage，
				// 直接透传 raw bytes（Anthropic 接受 string 或块数组两种）
				upstream.System = m.Content
			}
			continue
		case "tool":
			// canonical: role=tool ToolCallID=<id> Content=<result text 或块数组>
			// Anthropic: role=user content=[{type:tool_result, tool_use_id, content}]
			// content 字段 Anthropic 也接受字符串或子内容块；m.Content 走
			// ContentAsString 兜底拿文本。
			upstream.Messages = append(upstream.Messages, anthropicMessage{
				Role: "user",
				Content: []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.ContentAsString(),
				}},
			})
			continue
		case "assistant":
			if len(m.ToolCalls) > 0 {
				blocks := []map[string]any{}
				if txt := m.ContentAsString(); txt != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": txt})
				}
				for _, tc := range m.ToolCalls {
					var input map[string]any
					_ = json.Unmarshal(tc.Input, &input)
					if input == nil {
						input = map[string]any{}
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": input,
					})
				}
				upstream.Messages = append(upstream.Messages, anthropicMessage{
					Role: "assistant", Content: blocks,
				})
				continue
			}
		}
		// Multimodal path: caller supplied an Anthropic-shape content
		// array (text + image blocks). source.type=file blocks need
		// resolving against MinIO before forwarding (Anthropic doesn't
		// know our file_id); other shapes (base64/url) pass through.
		if len(m.Parts) > 0 {
			var blocks []map[string]any
			if err := json.Unmarshal(m.Parts, &blocks); err == nil &&
				len(blocks) > 0 {
				resolved := a.resolveBlocks(ctx, blocks)
				upstream.Messages = append(upstream.Messages,
					anthropicMessage{Role: m.Role, Content: resolved})
				continue
			}
			// Bad parts JSON — fall through to plain content rather
			// than 400ing the whole request.
		}
		// Default plain text path. m.Content 是 RawMessage（json.RawMessage
		// 实现 json.Marshaler，序列化时输出原始 bytes），anthropicMessage.Content
		// 是 any，直接赋值即可。
		upstream.Messages = append(upstream.Messages, anthropicMessage{
			Role: m.Role, Content: m.Content,
		})
	}

	for _, t := range req.Tools {
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		upstream.Tools = append(upstream.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	// 去掉尾部 / 和 /v1, 同 openai adaptor (避免 /v1/v1/messages).
	base = provider.NormalizeBaseURL(base)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("x-api-key", creds.APIKey)
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	return httpReq, nil
}

// resolveBlocks — walk the canonical Anthropic-shape content blocks and
// rewrite any source.type=file image into a shape Anthropic can consume.
// Order of attempts:
//
//  1. presigned URL (cheap, ~few hundred bytes; LLM fetches MinIO directly)
//  2. fetch + base64 inline (bandwidth-heavy fallback for unreachable
//     MinIO / TTL race / disabled URL support)
//  3. drop the block (better than 400ing the whole request)
//
// Other source types (base64 / url) and non-image blocks pass through.
//
// 当 Adaptor 没有 resolver (nil) 时, file blocks 会被丢弃 — 这种配置
// 不该在生产出现, 但也不能把 file shape 原样发给 Anthropic 让它 400。
func (a *Adaptor) resolveBlocks(ctx context.Context, blocks []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		if b["type"] != "image" {
			out = append(out, b)
			continue
		}
		src, _ := b["source"].(map[string]any)
		if src == nil {
			out = append(out, b)
			continue
		}
		srcType, _ := src["type"].(string)
		if srcType != "file" {
			// base64 / url — pass through verbatim.
			out = append(out, b)
			continue
		}
		fid, _ := src["file_id"].(string)
		if fid == "" || a.files == nil {
			// no resolver / no file_id → drop block (defensive).
			continue
		}
		if rewritten := a.rewriteFileBlock(ctx, b, fid); rewritten != nil {
			out = append(out, rewritten)
		}
	}
	return out
}

// rewriteFileBlock — try presign URL first, fallback to base64 inline.
// Returns nil to indicate "skip this block".
func (a *Adaptor) rewriteFileBlock(ctx context.Context, b map[string]any, fileID string) map[string]any {
	url, _, err := a.files.PresignURL(ctx, fileID)
	if err == nil && url != "" {
		// Anthropic 现在支持 source.type=url 直接给公网 https URL。
		// media_type 让 Anthropic 自己识别 (它支持 detection)。
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": url},
		}
	}
	// fallback — fetch + inline base64.
	bytesData, mediaType, ferr := a.files.Fetch(ctx, fileID)
	if ferr != nil || len(bytesData) == 0 {
		return nil
	}
	if mediaType == "" {
		// 退回 source.media_type 或 PNG 默认值
		if src, ok := b["source"].(map[string]any); ok {
			if mt, _ := src["media_type"].(string); mt != "" {
				mediaType = mt
			}
		}
		if mediaType == "" {
			mediaType = "image/png"
		}
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       base64.StdEncoding.EncodeToString(bytesData),
		},
	}
}

// ─── Non-streaming response ─────────────────────────────────────────────

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	} `json:"usage"`
}

func (a *Adaptor) ParseResponse(body []byte) (*provider.Response, error) {
	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("anthropic: parse: %w", err)
	}
	var text strings.Builder
	for _, c := range ar.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return &provider.Response{
		ID:    ar.ID,
		Model: ar.Model,
		Choices: []provider.Choice{{
			Index:   0,
			Message: provider.Message{Role: ar.Role, Content: provider.JSONString(text.String())},
		}},
		Usage: provider.Usage{
			PromptTokens:     ar.Usage.InputTokens,
			CompletionTokens: ar.Usage.OutputTokens,
			CacheReadTokens:  ar.Usage.CacheReadInputTokens,
			CacheWriteTokens: ar.Usage.CacheCreationInputTokens,
		},
		StopReason: ar.StopReason,
	}, nil
}

// ─── Streaming ──────────────────────────────────────────────────────────
//
// Anthropic stream of events for tool use (simplified):
//
//	event: message_start
//	event: content_block_start  data:{"index":0,"content_block":{"type":"tool_use","id":"toolu_X","name":"read","input":{}}}
//	event: content_block_delta  data:{"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path"}}
//	event: content_block_delta  data:{"index":0,"delta":{"type":"input_json_delta","partial_json":"\":\"x.txt\"}"}}
//	event: content_block_stop   data:{"index":0}
//	event: message_delta        data:{"delta":{"stop_reason":"tool_use"},...}
//	event: message_stop
//
// Text blocks use the same envelope but with "text" type and "text_delta" delta.

type blockState struct {
	kind   string // "text" / "tool_use"
	toolID string
}

func (a *Adaptor) StreamAdapter(ctx context.Context, body io.Reader) (<-chan provider.StreamFrame, error) {
	out := make(chan provider.StreamFrame, 32)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		blocks := map[int]*blockState{}
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				currentEvent = ""
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				currentEvent = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data := strings.TrimPrefix(line, "data: ")
				a.handleSSEData(currentEvent, data, blocks, out)
			}
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			out <- provider.StreamFrame{Type: provider.FrameError, Err: err}
		}
	}()
	return out, nil
}

func (a *Adaptor) handleSSEData(event, data string, blocks map[int]*blockState, out chan<- provider.StreamFrame) {
	switch event {
	case "content_block_start":
		var d struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return
		}
		bs := &blockState{kind: d.ContentBlock.Type}
		if d.ContentBlock.Type == "tool_use" {
			bs.toolID = d.ContentBlock.ID
			out <- provider.StreamFrame{
				Type: provider.FrameToolCallStart,
				ToolCall: &provider.ToolCallDelta{
					ID:   d.ContentBlock.ID,
					Name: d.ContentBlock.Name,
				},
			}
		}
		blocks[d.Index] = bs

	case "content_block_delta":
		var d struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return
		}
		switch d.Delta.Type {
		case "text_delta":
			out <- provider.StreamFrame{Type: provider.FrameDelta, Delta: d.Delta.Text}
		case "thinking_delta":
			// Extended thinking from Claude. Forward as its own frame
			// type so Brain can route it into a ThinkingBlock instead
			// of inline text.
			out <- provider.StreamFrame{
				Type:  provider.FrameThinking,
				Delta: d.Delta.Thinking,
			}
		case "input_json_delta":
			bs := blocks[d.Index]
			if bs == nil {
				return
			}
			out <- provider.StreamFrame{
				Type: provider.FrameToolCallArgs,
				ToolCall: &provider.ToolCallDelta{
					ID:        bs.toolID,
					ArgsDelta: d.Delta.PartialJSON,
				},
			}
		case "signature_delta":
			// Thinking signature (cryptographic proof that the model
			// produced the reasoning). Anthropic requires it to be
			// echoed back in the next request to use the thinking
			// blocks for caching, but we don't currently round-trip
			// thinking blocks across turns. Drop silently for now;
			// adding it later won't be a breaking change since
			// signature_delta is purely additive metadata.
		}

	case "content_block_stop":
		var d struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return
		}
		bs := blocks[d.Index]
		if bs != nil && bs.kind == "tool_use" {
			out <- provider.StreamFrame{
				Type:     provider.FrameToolCallEnd,
				ToolCall: &provider.ToolCallDelta{ID: bs.toolID},
			}
		}
		delete(blocks, d.Index)

	case "message_delta":
		var d struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &d); err == nil {
			if d.Delta.StopReason != "" {
				out <- provider.StreamFrame{Type: provider.FrameStop, Stop: d.Delta.StopReason}
			}
		}

	case "message_stop":
		// Don't emit duplicate FrameStop here; message_delta already did it.
	}
}
