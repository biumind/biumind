package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/files"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// stubResolver — duplicated from anthropic test (same shape, kept
// separate to avoid intra-test circular imports).
type stubResolver struct {
	urls       map[string]string
	bytes      map[string][]byte
	bytesMT    map[string]string
	presignErr map[string]error
	fetchErr   map[string]error
	calls      []string
}

func (s *stubResolver) PresignURL(_ context.Context, fid string) (string, string, error) {
	s.calls = append(s.calls, "presign:"+fid)
	if e := s.presignErr[fid]; e != nil {
		return "", "", e
	}
	if u, ok := s.urls[fid]; ok {
		return u, "", nil
	}
	return "", "", errors.New("no url")
}

func (s *stubResolver) Fetch(_ context.Context, fid string) ([]byte, string, error) {
	s.calls = append(s.calls, "fetch:"+fid)
	if e := s.fetchErr[fid]; e != nil {
		return nil, "", e
	}
	if b, ok := s.bytes[fid]; ok {
		return b, s.bytesMT[fid], nil
	}
	return nil, "", errors.New("no bytes")
}

var _ files.Resolver = (*stubResolver)(nil)

func TestTranslateRequest_PlainText(t *testing.T) {
	a := New()
	req := &provider.Request{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{Role: "user", Content: provider.JSONString("hello")},
		},
		MaxTokens: 256,
	}
	httpReq, err := a.TranslateRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-x"})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if httpReq.URL.Path != "/v1/chat/completions" {
		t.Errorf("path: %s", httpReq.URL.Path)
	}
	if httpReq.Header.Get("Authorization") != "Bearer sk-x" {
		t.Errorf("auth: %v", httpReq.Header.Get("Authorization"))
	}
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body parse: %v\n%s", err, body)
	}
	if got.Model != "gpt-4o-mini" || got.MaxTokens != 256 {
		t.Errorf("model/tokens: %+v", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Errorf("messages: %+v", got.Messages)
	}
}

func TestTranslateRequest_SystemMessageHoisted(t *testing.T) {
	a := New()
	req := &provider.Request{
		Model:  "gpt-4o",
		System: provider.JSONString("be terse"),
		Messages: []provider.Message{
			{Role: "user", Content: provider.JSONString("hi")},
		},
	}
	httpReq, _ := a.TranslateRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-x"})
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	if len(got.Messages) != 2 {
		t.Fatalf("want 2 messages (system + user), got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Errorf("system not first: %+v", got.Messages[0])
	}
}

func TestTranslateRequest_ToolsAndToolCallRoundtrip(t *testing.T) {
	a := New()
	req := &provider.Request{
		Model: "gpt-4o",
		Tools: []provider.Tool{{
			Name: "get_weather", Description: "Look up weather",
			Parameters: map[string]any{"type": "object"},
		}},
		Messages: []provider.Message{
			{Role: "user", Content: provider.JSONString("weather in SF?")},
			{Role: "assistant", ToolCalls: []provider.ToolCall{{
				ID: "call_1", Name: "get_weather",
				Input: json.RawMessage(`{"city":"SF"}`),
			}}},
			{Role: "tool", ToolCallID: "call_1", Content: provider.JSONString(`{"f":72}`)},
		},
	}
	httpReq, _ := a.TranslateRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-x"})
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" ||
		got.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools: %+v", got.Tools)
	}
	// Assistant tool_call message
	if got.Messages[1].Role != "assistant" || len(got.Messages[1].ToolCalls) != 1 {
		t.Errorf("assistant tool_calls missing: %+v", got.Messages[1])
	}
	if got.Messages[1].ToolCalls[0].Function.Arguments != `{"city":"SF"}` {
		t.Errorf("arguments not preserved: %+v", got.Messages[1].ToolCalls[0])
	}
	// Tool result message
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "call_1" {
		t.Errorf("tool result: %+v", got.Messages[2])
	}
}

func TestTranslateRequest_AnthropicIngressMultiTurnToolCalls(t *testing.T) {
	// Bug #1+#2 复现 + 修复后的回归测试。
	//
	// 模拟 daemon biumindkit 走 Anthropic 协议发的 multi-turn tool history:
	//   user: "ls"
	//   assistant: text + tool_use(Bash) + tool_use(Glob)
	//   user: tool_result(toolu_a) + tool_result(toolu_b)
	//   user: "what next?"
	//
	// 修复前: tool_use/tool_result 全埋在 raw content 块数组,丢失;OpenAI
	// compat 上游收到的对话破碎,glm-5.1 触发 1213。
	// 修复后: Message.UnmarshalJSON 提取到 ToolCalls/ToolResults,openai
	// adaptor 拆 tool_result 成多条 role=tool。
	rawBody := []byte(`{
		"model":"glm-5.1",
		"messages":[
			{"role":"user","content":"ls"},
			{"role":"assistant","content":[
				{"type":"text","text":"Let me check."},
				{"type":"tool_use","id":"toolu_a","name":"Bash","input":{"command":"ls"}},
				{"type":"tool_use","id":"toolu_b","name":"Glob","input":{"pattern":"*.go"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_a","content":"file1\nfile2"},
				{"type":"tool_result","tool_use_id":"toolu_b","content":"main.go"}
			]},
			{"role":"user","content":"what next?"}
		],
		"tools":[{
			"name":"Bash","description":"shell",
			"input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}
		}]
	}`)
	var canon provider.Request
	if err := json.Unmarshal(rawBody, &canon); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	a := New()
	httpReq, err := a.TranslateRequest(context.Background(), &canon,
		&provider.Credentials{APIKey: "sk-x"})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}

	// 验证 tool spec 完整(Bug #1)
	if len(got.Tools) != 1 {
		t.Fatalf("Tools: %+v", got.Tools)
	}
	params := got.Tools[0].Function.Parameters
	if params == nil {
		t.Fatalf("Tools[0].Parameters 为 nil — input_schema 没透过来 (Bug #1 退化)")
	}
	if _, ok := params["properties"]; !ok {
		t.Errorf("Tools[0].Parameters 没 properties: %+v", params)
	}

	// 验证消息序列拆分(Bug #2)
	// 期望: user(ls) + assistant(text + 2 tool_calls) + tool(toolu_a) + tool(toolu_b) + user(what next)
	if len(got.Messages) != 5 {
		for i, m := range got.Messages {
			t.Logf("[%d] role=%s tool_call_id=%q tool_calls=%d content=%v",
				i, m.Role, m.ToolCallID, len(m.ToolCalls), m.Content)
		}
		t.Fatalf("Messages 长度: 期望 5(user+assistant+tool+tool+user), got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("Messages[0]: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || len(got.Messages[1].ToolCalls) != 2 {
		t.Errorf("Messages[1] 应该是 assistant + 2 tool_calls: %+v", got.Messages[1])
	}
	// 检查 assistant 的 tool_calls.arguments 不丢
	if got.Messages[1].ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Errorf("ToolCalls[0].Arguments 丢: %q", got.Messages[1].ToolCalls[0].Function.Arguments)
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "toolu_a" {
		t.Errorf("Messages[2] 应该是 role=tool toolu_a: %+v", got.Messages[2])
	}
	if got.Messages[2].Content != "file1\nfile2" {
		t.Errorf("Messages[2].Content: %v", got.Messages[2].Content)
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "toolu_b" {
		t.Errorf("Messages[3] 应该是 role=tool toolu_b: %+v", got.Messages[3])
	}
	if got.Messages[4].Role != "user" {
		t.Errorf("Messages[4]: %+v", got.Messages[4])
	}
}

func TestTranslateRequest_StreamIncludesUsage(t *testing.T) {
	a := New()
	req := &provider.Request{
		Model:    "gpt-4o-mini",
		Stream:   true,
		Messages: []provider.Message{{Role: "user", Content: provider.JSONString("x")}},
	}
	httpReq, _ := a.TranslateRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-x"})
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	if got.StreamOptions == nil || !got.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage must be true; got %+v", got.StreamOptions)
	}
}

func TestTranslateRequest_RejectsMissingAPIKey(t *testing.T) {
	a := New()
	_, err := a.TranslateRequest(context.Background(),
		&provider.Request{Model: "gpt-4o"}, &provider.Credentials{})
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

// 用户填的 base_url 含尾部 /v1 是 LiteLLM / OpenRouter / New-API / vLLM /
// Ollama 文档推荐的写法; adaptor 必须 normalize 去重, 否则得到
// /v1/v1/chat/completions 404. 这个测试覆盖了 New-API 这种网关的真实场景.
func TestTranslateRequest_CustomBaseURL(t *testing.T) {
	cases := []struct {
		name string
		base string
		want string
	}{
		{"host only", "https://litellm.local", "https://litellm.local/v1/chat/completions"},
		{"host with /v1", "https://litellm.local/v1", "https://litellm.local/v1/chat/completions"},
		{"host with /v1/", "https://litellm.local/v1/", "https://litellm.local/v1/chat/completions"},
		{"trailing slash only", "https://litellm.local/", "https://litellm.local/v1/chat/completions"},
		{"path then /v1", "https://proxy.example.com/api/v1", "https://proxy.example.com/api/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			httpReq, err := a.TranslateRequest(context.Background(),
				&provider.Request{Model: "x", Messages: []provider.Message{{Role: "user", Content: provider.JSONString("y")}}},
				&provider.Credentials{APIKey: "k", BaseURL: tc.base})
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if got := httpReq.URL.String(); got != tc.want {
				t.Errorf("base=%q: got %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

func TestParseResponse_PlainText(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-x",
		"model": "gpt-4o-mini",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "hi back"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}
	}`)
	a := New()
	resp, err := a.ParseResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.ID != "chatcmpl-x" || resp.Model != "gpt-4o-mini" {
		t.Errorf("id/model: %+v", resp)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.ContentAsString() != "hi back" {
		t.Errorf("choices: %+v", resp.Choices)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 4 {
		t.Errorf("usage: %+v", resp.Usage)
	}
	if resp.StopReason != "stop" {
		t.Errorf("stop reason: %s", resp.StopReason)
	}
}

func TestParseResponse_ToolCalls(t *testing.T) {
	body := []byte(`{
		"id": "x",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_42",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"NYC\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 30, "completion_tokens": 8, "total_tokens": 38}
	}`)
	a := New()
	resp, _ := a.ParseResponse(body)
	if len(resp.Choices) != 1 {
		t.Fatalf("choices")
	}
	tc := resp.Choices[0].Message.ToolCalls
	if len(tc) != 1 || tc[0].Name != "get_weather" {
		t.Errorf("tool_calls: %+v", tc)
	}
	if string(tc[0].Input) != `{"city":"NYC"}` {
		t.Errorf("args: %s", tc[0].Input)
	}
}

func TestStreamAdapter_Deltas(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"He"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"!"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	a := New()
	ch, err := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	var deltas, stops, usages int
	var combined string
	var lastUsage provider.Usage
	for f := range ch {
		switch f.Type {
		case provider.FrameDelta:
			deltas++
			combined += f.Delta
		case provider.FrameStop:
			stops++
		case provider.FrameUsage:
			usages++
			if f.Usage != nil {
				lastUsage = *f.Usage
			}
		case provider.FrameError:
			t.Errorf("unexpected error frame: %v", f.Err)
		}
	}
	if deltas != 3 || combined != "Hello!" {
		t.Errorf("deltas=%d combined=%q", deltas, combined)
	}
	if stops != 1 {
		t.Errorf("stops=%d", stops)
	}
	if usages != 1 || lastUsage.PromptTokens != 5 || lastUsage.CompletionTokens != 3 {
		t.Errorf("usage: usages=%d %+v", usages, lastUsage)
	}
}

// 推理模型 (glm-thinking / deepseek-r1 / qwen-r1) 在 OpenAI Compat 模式下,
// 推理阶段的 delta 只填 reasoning_content, 进入回答后 reasoning_content
// 切到 content. 适配器要把推理阶段翻成 FrameThinking, 回答阶段保持 FrameDelta.
func TestStreamAdapter_ReasoningContentEmitsThinking(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		// 推理阶段 — 只填 reasoning_content
		`data: {"id":"x","choices":[{"index":0,"delta":{"reasoning_content":"Let me "}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"reasoning_content":"think...\n"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"reasoning_content":"2+2=4"}}]}`,
		// 答复阶段 — 切回 content
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"Answer: "}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"4"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	a := New()
	ch, err := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	var thinking, deltas int
	var thinkingCombined, deltaCombined string
	for f := range ch {
		switch f.Type {
		case provider.FrameThinking:
			thinking++
			thinkingCombined += f.Delta
		case provider.FrameDelta:
			deltas++
			deltaCombined += f.Delta
		case provider.FrameError:
			t.Errorf("unexpected error frame: %v", f.Err)
		}
	}
	if thinking != 3 || thinkingCombined != "Let me think...\n2+2=4" {
		t.Errorf("thinking=%d combined=%q", thinking, thinkingCombined)
	}
	if deltas != 2 || deltaCombined != "Answer: 4" {
		t.Errorf("deltas=%d combined=%q", deltas, deltaCombined)
	}
}

// 极少数情况 (新版 vLLM / OpenRouter) 单个 delta 同时包含 reasoning_content
// + content. 适配器必须保序: 先 emit thinking 再 emit content, 不能丢任一边。
func TestStreamAdapter_ReasoningAndContentSameDelta(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"x","choices":[{"index":0,"delta":{"reasoning_content":"r1","content":"c1"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	a := New()
	ch, _ := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	var got []provider.StreamFrameType
	var thinkingText, contentText string
	for f := range ch {
		got = append(got, f.Type)
		switch f.Type {
		case provider.FrameThinking:
			thinkingText += f.Delta
		case provider.FrameDelta:
			contentText += f.Delta
		}
	}
	// 期望顺序: thinking, delta, stop. (顺序对端侧渲染重要 — 思考要先于答案)
	if len(got) < 2 || got[0] != provider.FrameThinking || got[1] != provider.FrameDelta {
		t.Errorf("expected thinking before delta; got %v", got)
	}
	if thinkingText != "r1" || contentText != "c1" {
		t.Errorf("thinking=%q content=%q", thinkingText, contentText)
	}
}

func TestStreamAdapter_ToolCallsAcrossFrames(t *testing.T) {
	stream := strings.Join([]string{
		// Tool call header (id + name)
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		// Argument fragments — id omitted on subsequent chunks per OpenAI spec
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"SF\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	a := New()
	ch, _ := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	var startCount, argCount, endCount int
	var assembledArgs string
	for f := range ch {
		switch f.Type {
		case provider.FrameToolCallStart:
			startCount++
			if f.ToolCall.Name != "get_weather" || f.ToolCall.ID != "call_1" {
				t.Errorf("start: %+v", f.ToolCall)
			}
		case provider.FrameToolCallArgs:
			argCount++
			assembledArgs += f.ToolCall.ArgsDelta
			if f.ToolCall.ID != "call_1" {
				t.Errorf("args id propagation: %+v", f.ToolCall)
			}
		case provider.FrameToolCallEnd:
			endCount++
		}
	}
	if startCount != 1 || argCount != 2 || endCount != 1 {
		t.Errorf("counts: start=%d args=%d end=%d", startCount, argCount, endCount)
	}
	if assembledArgs != `{"city":"SF"}` {
		t.Errorf("assembled args: %q", assembledArgs)
	}
}

func TestStreamAdapter_BadJSONEmitsErrorFrame(t *testing.T) {
	stream := "data: {bad json}\n\n"
	a := New()
	ch, _ := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	var err error
	for f := range ch {
		if f.Type == provider.FrameError {
			err = f.Err
		}
	}
	if err == nil {
		t.Error("expected FrameError on bad JSON")
	}
}

// ─── Round-trip integration via httptest stub ────────────

func TestEnd2End_NonStreamingViaStubServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "404", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"e2e","model":"gpt-4o-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer srv.Close()

	a := New()
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{Model: "gpt-4o-mini",
			Messages: []provider.Message{{Role: "user", Content: provider.JSONString("ping")}}},
		&provider.Credentials{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	parsed, err := a.ParseResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Choices[0].Message.ContentAsString() != "pong" || parsed.Usage.PromptTokens != 3 {
		t.Errorf("e2e parsed: %+v", parsed)
	}
}

func TestAnthropicPartsToOpenAI(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		wantType string
	}{
		{"text only", `[{"type":"text","text":"hi"}]`, 1, "text"},
		{
			"base64 image",
			`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`,
			1, "image_url",
		},
		{
			"url image",
			`[{"type":"image","source":{"type":"url","url":"https://x.png"}}]`,
			1, "image_url",
		},
		{
			"text + image mixed",
			`[{"type":"text","text":"see"},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"BB"}}]`,
			2, "text",
		},
		{"empty array", `[]`, 0, ""},
		{"malformed json", `not json`, 0, ""},
		{"unknown type dropped", `[{"type":"shrug","data":"x"}]`, 0, ""},
		{"image without source dropped", `[{"type":"image"}]`, 0, ""},
		{"image base64 missing data dropped",
			`[{"type":"image","source":{"type":"base64"}}]`, 0, ""},
	}
	for _, c := range cases {
		got := (&Adaptor{}).anthropicPartsToOpenAI(context.Background(), []byte(c.input))
		if c.wantLen == 0 {
			if got != nil {
				t.Errorf("%s: expected nil, got %+v", c.name, got)
			}
			continue
		}
		if len(got) != c.wantLen {
			t.Errorf("%s: got len %d want %d (%+v)", c.name, len(got), c.wantLen, got)
			continue
		}
		if got[0]["type"] != c.wantType {
			t.Errorf("%s: first type got %v want %v", c.name, got[0]["type"], c.wantType)
		}
	}
}

func TestAnthropicPartsToOpenAI_Base64URLForm(t *testing.T) {
	got := (&Adaptor{}).anthropicPartsToOpenAI(context.Background(), []byte(
		`[{"type":"image","source":{"type":"base64","media_type":"image/webp","data":"DEADBEEF"}}]`))
	if len(got) != 1 {
		t.Fatalf("got %d blocks", len(got))
	}
	imgURL, ok := got[0]["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url not map: %+v", got[0])
	}
	url, _ := imgURL["url"].(string)
	if url != "data:image/webp;base64,DEADBEEF" {
		t.Errorf("data URL: got %q", url)
	}
}

func TestAnthropicPartsToOpenAI_Base64DefaultsMimeWhenMissing(t *testing.T) {
	got := (&Adaptor{}).anthropicPartsToOpenAI(context.Background(), []byte(
		`[{"type":"image","source":{"type":"base64","data":"AA"}}]`))
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	url := got[0]["image_url"].(map[string]any)["url"].(string)
	if url != "data:image/png;base64,AA" {
		t.Errorf("default mime: got %q", url)
	}
}

// ─── file resolver integration ───────────────────────────────

func TestOpenAI_FileSource_ResolvesToImageURL(t *testing.T) {
	res := &stubResolver{
		urls: map[string]string{"fid-x": "https://minio.example/p?sig=1"},
	}
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "text", "text": "look"},
		{"type": "image", "source": map[string]any{
			"type": "file", "file_id": "fid-x", "media_type": "image/png",
		}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "gpt-4o", MaxTokens: 100,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "sk"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	contents, _ := got.Messages[0].Content.([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(contents))
	}
	imgURL := contents[1].(map[string]any)["image_url"].(map[string]any)["url"]
	if imgURL != "https://minio.example/p?sig=1" {
		t.Errorf("image_url: %v", imgURL)
	}
}

func TestOpenAI_FileSource_FallsBackToDataURLOnPresignFail(t *testing.T) {
	res := &stubResolver{
		presignErr: map[string]error{"fid-y": errors.New("brain")},
		bytes:      map[string][]byte{"fid-y": []byte("raw")},
		bytesMT:    map[string]string{"fid-y": "image/jpeg"},
	}
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "image", "source": map[string]any{
			"type": "file", "file_id": "fid-y",
		}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "gpt-4o", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "sk"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	contents, _ := got.Messages[0].Content.([]any)
	url := contents[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Errorf("expected data URL, got %q", url)
	}
}

func TestOpenAI_FileSource_NoResolver_DropsBlock(t *testing.T) {
	a := New()
	parts := mustJSON(t, []map[string]any{
		{"type": "text", "text": "hi"},
		{"type": "image", "source": map[string]any{"type": "file", "file_id": "x"}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "gpt-4o", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "sk"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var got openAIRequest
	_ = json.Unmarshal(body, &got)
	contents, _ := got.Messages[0].Content.([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["type"] != "text" {
		t.Errorf("expected file block dropped, got %+v", contents)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
