// Package provider defines the Adaptor interface — every LLM provider implements
// this. model-relay's relay layer iterates a registry to translate, dispatch, and stream.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// ErrNotImplemented — modality adaptor 接口里某条 method 当前阶段未实装时返.
// 调用方应直接 return 415/501; 不应吞掉.
var ErrNotImplemented = errors.New("provider: method not implemented in this milestone")

// NormalizeBaseURL strips a trailing slash and `/v1` suffix from the
// user-provided base URL, so adaptors can unconditionally append their
// own versioned path (e.g. `/v1/chat/completions`) without doubling.
//
// 历史 bug: 各 adaptor 写死 defaultBaseURL = "https://api.openai.com"
// (不含 /v1) 然后拼 "/v1/chat/completions"; 但很多 OpenAI 兼容上游
// (LiteLLM, OpenRouter, New-API, vLLM, Ollama) 文档示例都让用户填
// "https://host/v1" 这种带 /v1 的 base — 拼出来就是 /v1/v1/... 404.
//
// Examples:
//
//	"https://api.openai.com"            → "https://api.openai.com"
//	"https://api.openai.com/"           → "https://api.openai.com"
//	"https://api.openai.com/v1"         → "https://api.openai.com"
//	"https://api.openai.com/v1/"        → "https://api.openai.com"
//	"https://new-api.example.com/v1"     → "https://new-api.example.com"
//	"https://proxy.example.com/api/v1"  → "https://proxy.example.com/api"
//	"https://host/v1/extra"             → unchanged (only TRAILING /v1 is stripped)
func NormalizeBaseURL(s string) string {
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/v1")
	return s
}

// Request is the canonical OpenAI-style chat request that all adaptors translate from.
type Request struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	// Sampling parameters. All optional — adaptor should omit from
	// upstream payload when nil so providers fall back to their own
	// defaults rather than e.g. "temperature=0" being sent as a
	// genuine override.
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
	Tools         []Tool   `json:"tools,omitempty"`
	// System 字段 Anthropic 协议接受两种 shape：
	//   "you are helpful"                                         → 单字符串
	//   [{"type":"text","text":"..."},{"type":"text","text":"..."}] → 块数组
	// （含 prompt cache 时常用块数组以便给个别块加 cache_control）
	// 用 json.RawMessage 保 raw 透传上游；adaptor 需要"看 system 内容"时
	// 调 SystemAsString() 兜底拼成单字符串。
	System   json.RawMessage `json:"system,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

// JSONString 把 string 编成单字符串 JSON RawMessage（含两端引号 + escape）。
// 调用方构造 Message{Role: "...", Content: provider.JSONString("hi")} 用。
func JSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// ContentAsString 把 Message.Content 兜底成单字符串。同 SystemAsString
// 算法：识别 string / array of {type:text,text} 两种 shape，array 拼接
// 所有 text 字段。
func (m *Message) ContentAsString() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		var b []byte
		for _, blk := range blocks {
			if blk.Type != "text" || blk.Text == "" {
				continue
			}
			if len(b) > 0 {
				b = append(b, '\n', '\n')
			}
			b = append(b, blk.Text...)
		}
		return string(b)
	}
	return ""
}

// SystemAsString 把 Request.System 兜底成单字符串：
//   - raw 是 JSON string → 解码返字符串
//   - raw 是 JSON array of {type:"text", text:"..."} → 拼接所有 text 字段
//   - raw 空 / 类型不识别 → 返 ""
//
// OpenAI / 其他 provider 不支持系统块数组时调这个降级。
func (r *Request) SystemAsString() string {
	if len(r.System) == 0 {
		return ""
	}
	// try string
	var s string
	if err := json.Unmarshal(r.System, &s); err == nil {
		return s
	}
	// try array of {type, text}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.System, &blocks); err == nil {
		var b []byte
		for _, blk := range blocks {
			if blk.Type != "text" || blk.Text == "" {
				continue
			}
			if len(b) > 0 {
				b = append(b, '\n', '\n')
			}
			b = append(b, blk.Text...)
		}
		return string(b)
	}
	return ""
}

type Message struct {
	Role string `json:"role"`
	// Content 跟 System 一样，Anthropic 协议接受两种 shape：
	//   "hello world"                                   → 单字符串
	//   [{"type":"text","text":"..."},{"type":"tool_use",...}]  → 块数组
	// biumindkit 在 tool-use 多 turn 场景必发块数组。用 RawMessage 保 raw
	// 透传上游；adaptor 需要"看 Content 文本"时调 ContentAsString() 兜底。
	Content json.RawMessage `json:"content"`

	// Parts: optional multimodal content. When non-empty, replaces
	// Content for adaptor purposes — the bytes are a JSON array of
	// content blocks already shaped per the *target* provider
	// (Anthropic: [{type:text,text}, {type:image,source:{...}}]).
	// Cross-provider translation is the caller's responsibility.
	// Adaptors that don't recognise multimodal fall back to Content.
	Parts json.RawMessage `json:"parts,omitempty"`

	// Tool-use round-trip fields (optional):
	//
	//	role=assistant: ToolCalls non-empty → assistant emitted tool_use blocks
	//	role=tool / role=user: ToolCallID set → this message is a tool_result
	//	  for that tool_use_id; Content is the result text.
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	// ToolResults: Anthropic 入口下,user 消息 content 块数组里的 tool_result
	// 块经 UnmarshalJSON 提取到这里。json:"-" 因为这不是 wire 字段,只是
	// 反序列化产物 —— Anthropic 协议把多个 tool_result 塞在一条 user 消
	// 息里,而 OpenAI compat 上游要求每条 tool_result 单独 role=tool 消
	// 息,适配器需要拆。
	//
	// OpenAI 入口走 ToolCallID + Content 单条形式;Anthropic 入口走 这里
	// 的多条形式。两条路径互斥,不会同时填。
	ToolResults []ToolResult `json:"-"`
}

// ToolResult 是 Anthropic 协议 tool_result 块的 canonical 表示。
type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// UnmarshalJSON 兼容 OpenAI 风格(顶层 tool_calls/tool_call_id)和 Anthropic
// 风格(content 块数组里嵌入 tool_use/tool_result/image)两种入口形态。
//
// 解析流程:
//  1. 标准字段先反序列化(Role, Content, Parts, ToolCalls, ToolCallID)
//  2. 如果 Content 是块数组,扫每个块:
//     - tool_use → 追加到 ToolCalls
//     - tool_result → 追加到 ToolResults
//     - image → 提取到 Parts(如果 Parts 之前为空)
//
// text 块留在 Content 里,ContentAsString() 拼接出文本。这样:
//   - 既存的 OpenAI 入口路径不变(ToolCalls/ToolCallID 直接来自顶层字段)
//   - Anthropic 入口路径通过这里把信息从块数组里"抢救"出来,适配器照常工作
//
// 不修这里的话,Anthropic 入口下:tool_use/tool_result/image 全埋在
// raw Content RawMessage 里,适配器走 ContentAsString() 只拿 text 块,
// 工具调用历史和图片块全丢失 —— 实测在 glm-5.1 + Agent 模式触发 1213。
func (m *Message) UnmarshalJSON(data []byte) error {
	type wireMessage struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Parts      json.RawMessage `json:"parts,omitempty"`
		ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	var w wireMessage
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.Content = w.Content
	m.Parts = w.Parts
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	m.ToolResults = nil

	if len(m.Content) == 0 {
		return nil
	}
	// 看 Content 是不是块数组(Anthropic 风格);string content 走老路
	var blocks []json.RawMessage
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil // 单字符串,无须扫
	}

	var imageBlocks, textOrOtherBlocks []json.RawMessage
	for _, blk := range blocks {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(blk, &head); err != nil {
			textOrOtherBlocks = append(textOrOtherBlocks, blk)
			continue
		}
		switch head.Type {
		case "tool_use":
			var tu struct {
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(blk, &tu); err == nil && (tu.ID != "" || tu.Name != "") {
				m.ToolCalls = append(m.ToolCalls, ToolCall{
					ID: tu.ID, Name: tu.Name, Input: tu.Input,
				})
			}
		case "tool_result":
			var tr struct {
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			}
			if err := json.Unmarshal(blk, &tr); err == nil {
				m.ToolResults = append(m.ToolResults, ToolResult{
					ToolUseID: tr.ToolUseID,
					Content:   toolResultContentToString(tr.Content),
					IsError:   tr.IsError,
				})
			}
		case "image":
			imageBlocks = append(imageBlocks, blk)
		default:
			textOrOtherBlocks = append(textOrOtherBlocks, blk)
		}
	}

	// 如果发现 image 且原 Parts 空,把 image + text 块迁到 Parts
	// 让 anthropicPartsToOpenAI / 各 adaptor 多模态分支接管
	if len(imageBlocks) > 0 && len(m.Parts) == 0 {
		all := make([]json.RawMessage, 0, len(textOrOtherBlocks)+len(imageBlocks))
		all = append(all, textOrOtherBlocks...)
		all = append(all, imageBlocks...)
		if b, err := json.Marshal(all); err == nil {
			m.Parts = b
		}
	}
	return nil
}

// toolResultContentToString 把 Anthropic tool_result.content 兜底成字符串。
// content 字段允许 string 或 [{type:text,text}, ...] 子块数组(Anthropic spec)。
func toolResultContentToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var inner []map[string]any
	if err := json.Unmarshal(raw, &inner); err == nil {
		var sb strings.Builder
		for _, blk := range inner {
			t, _ := blk["type"].(string)
			if t != "text" {
				continue
			}
			txt, _ := blk["text"].(string)
			if txt == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(txt)
		}
		return sb.String()
	}
	return ""
}

// ToolCall represents one tool invocation made by the assistant.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"` // raw JSON object
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters: JSON Schema for tool input. 兼容两种入口字段名:
	//   - "parameters" (OpenAI 协议)
	//   - "input_schema" (Anthropic 协议) ← daemon biumindkit 发的就是这个
	// 之前只认 "parameters" 时,Anthropic 入口的 schema 全被丢,glm-5.1
	// 看不到 properties/required, emit arguments="{}" 空参导致工具调用全
	// 部失败 + 第二轮 1213。
	Parameters map[string]any `json:"parameters,omitempty"`
}

// UnmarshalJSON 自定义反序列化:同时接受 "parameters" 和 "input_schema"
// 字段,前者优先(OpenAI 风格);两个都缺则 Parameters = nil。
func (t *Tool) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters,omitempty"`
		InputSchema map[string]any `json:"input_schema,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Name = raw.Name
	t.Description = raw.Description
	if raw.Parameters != nil {
		t.Parameters = raw.Parameters
	} else if raw.InputSchema != nil {
		t.Parameters = raw.InputSchema
	}
	return nil
}

// Response is the canonical non-streaming response.
type Response struct {
	ID         string   `json:"id"`
	Model      string   `json:"model"`
	Choices    []Choice `json:"choices"`
	Usage      Usage    `json:"usage"`
	StopReason string   `json:"stop_reason,omitempty"`
}

type Choice struct {
	Index   int     `json:"index"`
	Message Message `json:"message"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// StreamFrame is one delta chunk from a streaming response.
type StreamFrame struct {
	Type     StreamFrameType
	Delta    string
	ToolCall *ToolCallDelta
	Usage    *Usage
	Stop     string
	Err      error
}

type StreamFrameType int

const (
	FrameDelta StreamFrameType = iota
	FrameToolCallStart
	FrameToolCallArgs
	FrameToolCallEnd
	FrameUsage
	FrameStop
	FrameError
	// FrameThinking carries the model's intermediate reasoning when
	// extended-thinking is enabled (Claude opus / sonnet 4.x). The
	// Delta field holds incremental thinking text. Brain routes
	// these into a separate ThinkingBlock so the renderer can fold
	// the reasoning behind a "Thought for Xs" disclosure.
	FrameThinking
)

type ToolCallDelta struct {
	ID        string
	Name      string
	ArgsDelta string
}

// Credentials passed to adaptor (looked up by model-relay from BYOK or platform pool).
type Credentials struct {
	APIKey  string
	BaseURL string            // optional override (e.g. Azure / private deployment)
	Extra   map[string]string // provider-specific (project_id for Vertex etc.)
}

// Adaptor is the contract every provider implements.
type Adaptor interface {
	// Name returns canonical name ("anthropic" / "openai" / ...).
	Name() string

	// TranslateRequest builds the upstream HTTP request from canonical Request + credentials.
	TranslateRequest(ctx context.Context, req *Request, creds *Credentials) (*http.Request, error)

	// ParseResponse parses a non-streaming response body.
	ParseResponse(body []byte) (*Response, error)

	// StreamAdapter consumes the upstream SSE / chunk stream and yields canonical frames.
	StreamAdapter(ctx context.Context, body io.Reader) (<-chan StreamFrame, error)
}

// Registry holds all configured adaptors. v0.3 升级到 BaseAdaptor 让多
// modality adaptor (chat / speech / embed / image / ...) 共存. 老 chat-only
// 调用方在 Get 后做一次 type assertion 拿到 ChatAdaptor / Adaptor — 与
// ModeRouter 的 type-assert dispatch 风格一致.
type Registry struct {
	adaptors map[string]BaseAdaptor
}

func NewRegistry() *Registry {
	return &Registry{adaptors: make(map[string]BaseAdaptor)}
}

func (r *Registry) Register(a BaseAdaptor) {
	r.adaptors[a.Name()] = a
}

func (r *Registry) Get(name string) (BaseAdaptor, bool) {
	a, ok := r.adaptors[name]
	return a, ok
}

// GetChat — 便捷方法: 拿到 BaseAdaptor 同时强制满足老 chat Adaptor 接口.
// chat 路径 (messages.go / probe.go chat 分支) 用. 不满足时 ok=false 让
// 调用方返 415 / 502.
func (r *Registry) GetChat(name string) (Adaptor, bool) {
	a, ok := r.adaptors[name]
	if !ok {
		return nil, false
	}
	chat, ok := a.(Adaptor)
	return chat, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.adaptors))
	for k := range r.adaptors {
		out = append(out, k)
	}
	return out
}
