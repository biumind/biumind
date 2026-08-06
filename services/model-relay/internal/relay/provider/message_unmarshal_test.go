// Anthropic 入口反序列化测试 —— 防退化。
//
// 历史 bug (2026-06-14): provider.Message JSON tag 默认 OpenAI 风格,
// daemon biumindkit 通过 Anthropic 协议把 tool_use/tool_result/image 块
// 嵌在 content 块数组里 → 反序列化时全丢 → glm-5.1/DeepSeek 等 OpenAI
// compat 上游看不到工具调用历史 → 工具调用全废 + 第二轮 1213。
//
// 这里覆盖三类 ingress 形态:
//   - OpenAI 风格(顶层 tool_calls / tool_call_id / content 字符串)
//   - Anthropic 风格(content 块数组,各种块类型混合)
//   - 边界(空 content / 无 content / 单字符串 content)

package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageUnmarshal_OpenAIStyle(t *testing.T) {
	in := `{
		"role":"assistant",
		"content":"Sure, let me check.",
		"tool_calls":[{"id":"call_1","name":"Bash","input":{"command":"ls"}}]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != "assistant" {
		t.Errorf("role: got %q, want assistant", m.Role)
	}
	if got := m.ContentAsString(); got != "Sure, let me check." {
		t.Errorf("content: got %q", got)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].Name != "Bash" {
		t.Errorf("ToolCalls: %+v", m.ToolCalls)
	}
	if string(m.ToolCalls[0].Input) != `{"command":"ls"}` {
		t.Errorf("ToolCall.Input: %s", m.ToolCalls[0].Input)
	}
}

func TestMessageUnmarshal_AnthropicAssistantToolUse(t *testing.T) {
	// Anthropic 协议: assistant 消息 content 是块数组,tool_use 嵌在里头。
	// 这是 daemon biumindkit 发的实际形态。
	in := `{
		"role":"assistant",
		"content":[
			{"type":"text","text":"Let me check the directory."},
			{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}},
			{"type":"tool_use","id":"toolu_2","name":"Glob","input":{"pattern":"*.go"}}
		]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len: got %d want 2 — tool_use 块没被提取!", len(m.ToolCalls))
	}
	if m.ToolCalls[0].ID != "toolu_1" || m.ToolCalls[0].Name != "Bash" {
		t.Errorf("ToolCalls[0]: %+v", m.ToolCalls[0])
	}
	if string(m.ToolCalls[0].Input) != `{"command":"ls -la"}` {
		t.Errorf("ToolCalls[0].Input: %s", m.ToolCalls[0].Input)
	}
	if m.ToolCalls[1].Name != "Glob" {
		t.Errorf("ToolCalls[1]: %+v", m.ToolCalls[1])
	}
	// text 块还能被 ContentAsString 拿到
	if got := m.ContentAsString(); got != "Let me check the directory." {
		t.Errorf("ContentAsString: got %q", got)
	}
}

func TestMessageUnmarshal_AnthropicUserToolResult(t *testing.T) {
	// Anthropic 协议: user 消息 content 块数组里多个 tool_result。
	// OpenAI compat 上游需要拆成 N 条 role=tool —— 这里只测反序列化提取。
	in := `{
		"role":"user",
		"content":[
			{"type":"tool_result","tool_use_id":"toolu_1","content":"file1.txt\nfile2.txt"},
			{"type":"tool_result","tool_use_id":"toolu_2","content":"main.go","is_error":false},
			{"type":"tool_result","tool_use_id":"toolu_3","content":"missing","is_error":true}
		]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.ToolResults) != 3 {
		t.Fatalf("ToolResults len: got %d want 3 — tool_result 块没被提取!", len(m.ToolResults))
	}
	if m.ToolResults[0].ToolUseID != "toolu_1" || m.ToolResults[0].Content != "file1.txt\nfile2.txt" {
		t.Errorf("ToolResults[0]: %+v", m.ToolResults[0])
	}
	if !m.ToolResults[2].IsError {
		t.Errorf("ToolResults[2] IsError should be true")
	}
}

func TestMessageUnmarshal_ToolResultBlockArrayContent(t *testing.T) {
	// Anthropic spec: tool_result.content 也允许是 [{type:text,text}, ...] 子块
	in := `{
		"role":"user",
		"content":[{
			"type":"tool_result",
			"tool_use_id":"toolu_x",
			"content":[
				{"type":"text","text":"line1"},
				{"type":"text","text":"line2"}
			]
		}]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.ToolResults) != 1 {
		t.Fatalf("ToolResults len: got %d", len(m.ToolResults))
	}
	want := "line1\n\nline2"
	if m.ToolResults[0].Content != want {
		t.Errorf("Content: got %q want %q", m.ToolResults[0].Content, want)
	}
}

func TestMessageUnmarshal_AnthropicImageBlock(t *testing.T) {
	in := `{
		"role":"user",
		"content":[
			{"type":"text","text":"What is this?"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
		]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// image 应该被搬到 Parts(原 Parts 为空)
	if len(m.Parts) == 0 {
		t.Fatalf("Parts: empty — image 块没被迁移!")
	}
	if !strings.Contains(string(m.Parts), "image/png") {
		t.Errorf("Parts: %s", m.Parts)
	}
	if !strings.Contains(string(m.Parts), `"type":"text"`) {
		t.Errorf("Parts 应该带上原 text 块: %s", m.Parts)
	}
}

func TestMessageUnmarshal_PartsExplicitTakesPriority(t *testing.T) {
	// 调用方显式给了 Parts 时,不要被块数组里的 image 覆盖
	in := `{
		"role":"user",
		"content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"X"}}],
		"parts":[{"type":"text","text":"explicit"}]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(string(m.Parts), `"explicit"`) {
		t.Errorf("Parts 被覆盖了,应该保留显式值: %s", m.Parts)
	}
	if strings.Contains(string(m.Parts), "image/jpeg") {
		t.Errorf("不该把 content 里的 image 合并进显式 Parts: %s", m.Parts)
	}
}

func TestMessageUnmarshal_PlainStringContent(t *testing.T) {
	// 简单 user 消息 — string content,不是块数组
	in := `{"role":"user","content":"hello"}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := m.ContentAsString(); got != "hello" {
		t.Errorf("got %q", got)
	}
	if len(m.ToolCalls) != 0 || len(m.ToolResults) != 0 {
		t.Errorf("不该有 ToolCalls/ToolResults")
	}
}

func TestMessageUnmarshal_EmptyContent(t *testing.T) {
	in := `{"role":"assistant"}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != "assistant" {
		t.Errorf("role: %q", m.Role)
	}
}

func TestMessageUnmarshal_MixedAssistantTextAndToolUse(t *testing.T) {
	// 真实 daemon emit: thinking text + tool_use 混合
	in := `{
		"role":"assistant",
		"content":[
			{"type":"text","text":"I'll list the directory first."},
			{"type":"tool_use","id":"toolu_a","name":"Bash","input":{"command":"ls"}}
		]
	}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len: %d", len(m.ToolCalls))
	}
	if got := m.ContentAsString(); got != "I'll list the directory first." {
		t.Errorf("text 块应该可被 ContentAsString 拿到: got %q", got)
	}
}
