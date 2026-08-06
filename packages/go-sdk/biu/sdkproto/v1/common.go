// Package sdkproto v1 — BiuMind SDK Protocol v1 Go bindings.
//
// 字段名严格对齐上游 schema，所以 json tag
// 既有 snake_case (`session_id`) 也有 camelCase (`isSynthetic`、`apiKeySource`)。
// 不要"统一"成一种风格，三端会因此对不上。
//
// 字段类型选择上偏保守 + 向前兼容：
//   - 已知 union 字段（discriminator）严格类型化
//   - 嵌套 message 体（Anthropic AnthropicMessage）保留 json.RawMessage，避免
//     重新建模 content blocks 的复杂结构
//   - 顶层 struct 都带 Extra 字段吸收上游未知新增字段
package sdkproto

import "encoding/json"

// AnthropicMessage 是 Anthropic Messages API 的 message 对象。content 可以是
// string 或 content block 数组，结构由上层 Anthropic SDK 处理 —— 这里保留 raw。
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// NonNullableUsage 是 Anthropic API usage 的子集，外加 server_tool_use 等扩展。
// 字段差异较大且会演化，保 RawMessage。
type NonNullableUsage = json.RawMessage

// ModelUsage 跟上游 ModelUsageSchema 一一对应。所有字段都 required。
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
}

// RateLimitInfo 解析 Anthropic 响应头里的速率限制信息，结构演化频繁，保 raw。
type RateLimitInfo = json.RawMessage
