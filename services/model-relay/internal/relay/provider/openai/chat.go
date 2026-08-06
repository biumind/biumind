// Package openai implements provider.Adaptor for the OpenAI
// /v1/chat/completions API. The same shape works for any
// OpenAI-compatible upstream: OpenAI, Azure OpenAI, LiteLLM proxies,
// vLLM, llama.cpp, OpenRouter, Together — point Credentials.BaseURL
// at the host and they all speak the same wire format.
//
// Supported:
//   - Plain text round-trip (canonical Request → chat/completions, response → canonical)
//   - Streaming text deltas via SSE (`data: {choices:[{delta:{...}}]}`)
//   - Tool use (request `tools`, response `tool_calls` with streaming arg deltas)
//   - Usage accounting (prompt / completion tokens; OpenAI doesn't expose cache splits)
package openai

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

const defaultBaseURL = "https://api.openai.com"

// Adaptor — files resolver 可选, 同 anthropic.Adaptor 语义。
// nil → source.type=file 块被丢弃 (defensive)。
type Adaptor struct {
	files files.Resolver
}

func New() *Adaptor { return &Adaptor{} }

// NewWithResolver — 注入 file resolver, source.type=file 走 presigned
// URL → image_url; 失败时降级 fetch + base64 → data: URL。
func NewWithResolver(r files.Resolver) *Adaptor {
	return &Adaptor{files: r}
}

func (a *Adaptor) Name() string { return "openai" }

// Capabilities — v0.3 全模态 ChatAdaptor 接口要求.
// M0:    chat
// M2:    embedding (openai 兼容 /v1/embeddings 是事实标准, 覆盖 OpenAI /
//
//	Azure / SiliconFlow / 智谱 / DeepSeek / OpenRouter / bge-m3 自部署 TEI 等)
//
// M2.5:  rerank (Cohere /v1/rerank shape, 被 SiliconFlow / Jina / Voyage /
//
//	新-API 等 OpenAI-compat 网关原样照搬, bge-reranker-v2-m3 通用)
//
// M6:    audio_transcription (Whisper / GPT-4o-transcribe, multipart upload;
//
//	SiliconFlow / Groq / 自部署 faster-whisper 等通用)
//
// 后续 speech/image 由 dashscope.Adaptor 等 native 协议处理.
func (a *Adaptor) Capabilities() []string {
	return []string{"chat", "embedding", "rerank", "audio_transcription"}
}

// ─── Request translation ──────────────────────────────────────────────

// openAIMessage models the chat.completions message shape. Content is
// `any` because OpenAI accepts either a plain string or an array of
// content parts (e.g. with images). We always emit a string for the
// MVP; the field is `any` so future image/file support doesn't break
// the JSON shape.
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // always "function" today
	Function openAIToolCallFunc `json:"function"`
}

type openAIToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string per OpenAI spec
}

// openAITool wraps a function declaration in the OpenAI envelope.
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
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	// OpenAI calls this `stop` not `stop_sequences`. Accepts string
	// or array of strings; we always send array for simplicity.
	Stop  []string     `json:"stop,omitempty"`
	Tools []openAITool `json:"tools,omitempty"`
	// StreamOptions.IncludeUsage causes the SSE stream's terminal
	// event to carry a `usage` object — without it the stream finishes
	// without per-stream token counts and our reportUsage no-ops.
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// TranslateRequest builds the upstream HTTP request.
func (a *Adaptor) TranslateRequest(ctx context.Context, req *provider.Request, creds *provider.Credentials) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("openai: missing API key")
	}
	base := creds.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	// 去掉尾部 / 和 /v1, 让下面拼 /v1/chat/completions 不重复.
	// 用户 base_url 填 "https://host" 或 "https://host/v1" 都正确.
	base = provider.NormalizeBaseURL(base)

	or := openAIRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}

	// Lift req.System into a leading system message — OpenAI doesn't
	// have a top-level system field on chat.completions。req.System 是
	// json.RawMessage 支持 string / 块数组，OpenAI 不识别块数组，
	// SystemAsString 兜底拼成单字符串。
	if sys := req.SystemAsString(); sys != "" {
		or.Messages = append(or.Messages, openAIMessage{
			Role: "system", Content: sys,
		})
	}
	for _, m := range req.Messages {
		// Anthropic 入口下,user 消息块数组里多个 tool_result 经
		// Message.UnmarshalJSON 提取到 m.ToolResults。OpenAI compat 协议
		// 要求每个 tool_result 单独一条 role=tool 消息,这里拆。
		if len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				content := tr.Content
				if tr.IsError && content == "" {
					content = "[tool error]"
				}
				or.Messages = append(or.Messages, openAIMessage{
					Role:       "tool",
					ToolCallID: tr.ToolUseID,
					Content:    content,
				})
			}
			continue
		}

		out := openAIMessage{Role: m.Role}
		// m.Content 是 RawMessage（可能是 string 也可能是块数组）。OpenAI
		// chat.completions content 字段要求 string 或 [{type:...}] 数组，
		// 不接受 Anthropic 的 tool_use / tool_result 块；这里全降级到
		// 单字符串（ContentAsString 拼接 text 块）。
		contentStr := m.ContentAsString()
		if len(m.ToolCalls) > 0 {
			// Assistant tool-use turn. OpenAI 标准允许 content 省略/null,
			// 但智谱 GLM-5.1 严格要求 content 必须是字符串(空串可) ——
			// 否则下一轮带 tool_result 的请求触发 upstream 400 code 1213
			// "未正常接收到prompt参数"(实测 raw chunk 复现)。
			// 始终设 content,空 rationale 用 sentinel " "(单空格)绕开
			// omitempty 把空串 drop 掉的行为。OpenAI / DeepSeek / 通义
			// 等其他兼容上游都接受空 content,这是最大公约数。
			if contentStr != "" {
				out.Content = contentStr
			} else {
				out.Content = " "
			}
			for _, tc := range m.ToolCalls {
				out.ToolCalls = append(out.ToolCalls, openAIToolCall{
					ID: tc.ID, Type: "function",
					Function: openAIToolCallFunc{
						Name:      tc.Name,
						Arguments: string(tc.Input),
					},
				})
			}
		} else if m.ToolCallID != "" {
			// Tool result message. OpenAI uses role="tool".
			out.Role = "tool"
			out.ToolCallID = m.ToolCallID
			out.Content = contentStr
		} else if len(m.Parts) > 0 {
			// Multimodal: translate Anthropic-shape parts (the
			// canonical wire format BiuMind uses) into OpenAI's
			// `content: [{type: text|image_url, ...}]` form.
			// source.type=file 在这里解成 presigned URL / base64 inline。
			// Bad parts JSON falls back to plain content rather
			// than 400ing the whole request.
			if blocks := a.anthropicPartsToOpenAI(ctx, m.Parts); blocks != nil {
				out.Content = blocks
			} else {
				out.Content = contentStr
			}
		} else {
			out.Content = contentStr
		}
		or.Messages = append(or.Messages, out)
	}

	for _, t := range req.Tools {
		or.Tools = append(or.Tools, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Always ask for usage on streaming so reportUsage can charge the
	// hub.tpm bucket. Non-streaming responses include usage by default.
	if req.Stream {
		or.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}

	body, err := json.Marshal(or)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	if creds.Extra["organization"] != "" {
		httpReq.Header.Set("OpenAI-Organization", creds.Extra["organization"])
	}
	if creds.Extra["project"] != "" {
		httpReq.Header.Set("OpenAI-Project", creds.Extra["project"])
	}
	return httpReq, nil
}

// anthropicPartsToOpenAI converts BiuMind canonical multimodal parts
// (Anthropic-shape: text + image blocks with base64 / url source)
// into OpenAI's `content: [{type: ...}]` array form.
//
// Returns nil on parse failure or empty input — callers fall back
// to the plain `Content: <string>` path.
//
// Shape map:
//
//	{type: "text", text: "..."}
//	  ↔ {type: "text", text: "..."}
//	{type: "image", source: {type: "base64", media_type, data}}
//	  ↔ {type: "image_url", image_url: {url: "data:<media>;base64,<data>"}}
//	{type: "image", source: {type: "url", url}}
//	  ↔ {type: "image_url", image_url: {url}}
//	{type: "image", source: {type: "file", file_id}}
//	  ↔ {type: "image_url", image_url: {url: <presigned-url>}}    (primary)
//	     OR data: URL with fetched bytes                          (fallback)
//
// Unrecognised block types are dropped silently — keeping a partial
// payload working is better than a 400.
func (a *Adaptor) anthropicPartsToOpenAI(ctx context.Context, parts []byte) []map[string]any {
	var blocks []map[string]any
	if err := json.Unmarshal(parts, &blocks); err != nil || len(blocks) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		t, _ := b["type"].(string)
		switch t {
		case "text":
			out = append(out, map[string]any{
				"type": "text",
				"text": b["text"],
			})
		case "image":
			url := a.imageURLFromAnthropicImage(ctx, b)
			if url == "" {
				continue
			}
			out = append(out, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": url},
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// imageURLFromAnthropicImage — 把单个 Anthropic-shape image block 转成
// OpenAI 期望的 image_url.url 字符串。无法 resolve 时返 ""。
func (a *Adaptor) imageURLFromAnthropicImage(ctx context.Context, b map[string]any) string {
	src, _ := b["source"].(map[string]any)
	if src == nil {
		return ""
	}
	srcType, _ := src["type"].(string)
	switch srcType {
	case "base64":
		media, _ := src["media_type"].(string)
		data, _ := src["data"].(string)
		if data == "" {
			return ""
		}
		if media == "" {
			media = "image/png"
		}
		return "data:" + media + ";base64," + data
	case "url", "":
		if u, ok := src["url"].(string); ok && u != "" {
			return u
		}
		return ""
	case "file":
		fid, _ := src["file_id"].(string)
		if fid == "" || a.files == nil {
			return ""
		}
		// presigned URL 路径 (首选) — OpenAI 接受 https URL。
		if u, _, err := a.files.PresignURL(ctx, fid); err == nil && u != "" {
			return u
		}
		// 降级 fetch + data: URL。
		bytesData, mediaType, ferr := a.files.Fetch(ctx, fid)
		if ferr != nil || len(bytesData) == 0 {
			return ""
		}
		if mediaType == "" {
			mediaType, _ = src["media_type"].(string)
			if mediaType == "" {
				mediaType = "image/png"
			}
		}
		return "data:" + mediaType + ";base64," +
			base64.StdEncoding.EncodeToString(bytesData)
	}
	return ""
}

// ─── Non-streaming response ───────────────────────────────────────────

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int           `json:"index"`
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (a *Adaptor) ParseResponse(body []byte) (*provider.Response, error) {
	var or openAIChatResponse
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("openai: parse: %w", err)
	}
	out := &provider.Response{
		ID:    or.ID,
		Model: or.Model,
		Usage: provider.Usage{
			PromptTokens:     or.Usage.PromptTokens,
			CompletionTokens: or.Usage.CompletionTokens,
		},
	}
	for _, ch := range or.Choices {
		msg := provider.Message{Role: ch.Message.Role}
		if s, ok := ch.Message.Content.(string); ok {
			msg.Content = provider.JSONString(s)
		}
		for _, tc := range ch.Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, provider.ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
		out.Choices = append(out.Choices, provider.Choice{
			Index:   ch.Index,
			Message: msg,
		})
		if ch.FinishReason != "" && out.StopReason == "" {
			out.StopReason = ch.FinishReason
		}
	}
	return out, nil
}

// ─── Streaming ────────────────────────────────────────────────────────

// openAIStreamChunk is one SSE `data: {...}` payload from
// chat/completions. choices[].delta carries incremental content;
// the final chunk has finish_reason + (with stream_options.include_usage)
// a non-nil usage block.
type openAIStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			// ReasoningContent — LiteLLM / new-api / 智谱 GLM / DeepSeek-R1 /
			// Qwen-r1 在 OpenAI Compat 模式下用此字段输出推理过程
			// (与 OpenAI 官方 o1 系列不同 — o1 的 reasoning 不流式)。
			// 推理模型推理阶段每帧 delta 只填这个不填 content; 进入回答
			// 阶段切回 content. 翻译为 canonical FrameThinking.
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

// StreamAdapter parses OpenAI's SSE format into canonical frames.
//
// SSE shape:
//
//	data: {"id":"...","choices":[{"delta":{"content":"He"}}]}
//	data: {"id":"...","choices":[{"delta":{"content":"llo"}}]}
//	data: {"id":"...","choices":[{"finish_reason":"stop"}]}
//	data: {"choices":[],"usage":{...}}        // when include_usage
//	data: [DONE]
func (a *Adaptor) StreamAdapter(ctx context.Context, body io.Reader) (<-chan provider.StreamFrame, error) {
	out := make(chan provider.StreamFrame, 16)

	go func() {
		defer close(out)
		// Track in-flight tool calls by index so streamed argument
		// fragments can be correlated to their starting `id`/`name`.
		toolIDByIndex := map[int]string{}

		sc := bufio.NewScanner(body)
		// 1 MB max line — OpenAI chunks are ≤ a few KB but be lenient.
		buf := make([]byte, 0, 1024*1024)
		sc.Buffer(buf, cap(buf))

		for sc.Scan() {
			line := sc.Text()
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[len("data: "):]
			if data == "[DONE]" {
				return
			}
			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				select {
				case out <- provider.StreamFrame{Type: provider.FrameError,
					Err: fmt.Errorf("openai: stream parse: %w", err)}:
				case <-ctx.Done():
				}
				return
			}

			for _, c := range chunk.Choices {
				if c.Delta.ReasoningContent != "" {
					// 推理流先于内容流到达; emit FrameThinking 让上游
					// (anthropic_stream / messages) 翻成 thinking 帧。
					select {
					case out <- provider.StreamFrame{Type: provider.FrameThinking,
						Delta: c.Delta.ReasoningContent}:
					case <-ctx.Done():
						return
					}
				}
				if c.Delta.Content != "" {
					select {
					case out <- provider.StreamFrame{Type: provider.FrameDelta,
						Delta: c.Delta.Content}:
					case <-ctx.Done():
						return
					}
				}
				for _, tc := range c.Delta.ToolCalls {
					id := tc.ID
					if id == "" {
						id = toolIDByIndex[tc.Index]
					}
					if tc.ID != "" {
						toolIDByIndex[tc.Index] = tc.ID
						// First chunk for this index — emit start.
						select {
						case out <- provider.StreamFrame{
							Type: provider.FrameToolCallStart,
							ToolCall: &provider.ToolCallDelta{
								ID: tc.ID, Name: tc.Function.Name,
							},
						}:
						case <-ctx.Done():
							return
						}
					}
					if tc.Function.Arguments != "" {
						select {
						case out <- provider.StreamFrame{
							Type: provider.FrameToolCallArgs,
							ToolCall: &provider.ToolCallDelta{
								ID: id, ArgsDelta: tc.Function.Arguments,
							},
						}:
						case <-ctx.Done():
							return
						}
					}
				}
				if c.FinishReason != "" {
					// If we accumulated tool calls, emit ends so
					// downstream knows arguments are finalised.
					for _, id := range toolIDByIndex {
						select {
						case out <- provider.StreamFrame{
							Type:     provider.FrameToolCallEnd,
							ToolCall: &provider.ToolCallDelta{ID: id},
						}:
						case <-ctx.Done():
							return
						}
					}
					toolIDByIndex = map[int]string{}
					select {
					case out <- provider.StreamFrame{Type: provider.FrameStop,
						Stop: c.FinishReason}:
					case <-ctx.Done():
						return
					}
				}
			}
			if chunk.Usage != nil {
				select {
				case out <- provider.StreamFrame{Type: provider.FrameUsage,
					Usage: &provider.Usage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
					}}:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := sc.Err(); err != nil && err != io.EOF {
			select {
			case out <- provider.StreamFrame{Type: provider.FrameError, Err: err}:
			case <-ctx.Done():
			}
		}
	}()

	return out, nil
}
