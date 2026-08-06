// S4-3 RunV2 单测：用 httptest 假装 Anthropic upstream 跑端到端。
//
// 不复用 v1 的 hubScript（它喂 brain dialect）—— 这里直接喂 Anthropic
// Messages SSE 给 biumindkit。关键覆盖：
//
//	1. 单 turn text → emitter 收到 TextDelta + 正确 stop_reason
//	2. 工具回路 → 第一 turn tool_use → adapter 跑 tool → 第二 turn 收尾文本
//	3. PriorMessages 转换 → assistant + tool_result history 不丢

package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// anthropicScript 是顺序 SSE 应答脚本。第 N 次 POST /v1/messages 返回
// scenes[N-1]。每个 scene 是 raw SSE bytes（caller 自己拼 message_start
// 等事件），调用方负责 wire-shape 正确。
type anthropicScript struct {
	scenes []string
	calls  atomic.Int32
	bodies []string
}

func (s *anthropicScript) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(s.calls.Add(1)) - 1
		if idx >= len(s.scenes) {
			http.Error(w, "scenes exhausted", http.StatusInternalServerError)
			return
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		s.bodies = append(s.bodies, string(buf))

		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.scenes[idx]))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
}

// anthropicTextOnly 一个完整 single-turn text answer（end_turn）。
func anthropicTextOnly(text string) string {
	return `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`
}

func newRunV2Rig(t *testing.T, scenes ...string) (
	*AgentLoop, *anthropicScript, *tools.Registry, string,
) {
	t.Helper()
	script := &anthropicScript{scenes: scenes}
	srv := httptest.NewServer(script.handler())
	t.Cleanup(srv.Close)
	reg := tools.New()
	loop := NewAgentLoop(nil, reg) // RunV2 不走 Relay HTTPSender
	return loop, script, reg, srv.URL
}

// 1) 单 turn text：RunV2 串通 biumindkit → BlockEmitter
func TestRunV2_SingleTurnText(t *testing.T) {
	loop, script, _, upstream := newRunV2Rig(t, anthropicTextOnly("hello"))
	be := newTestEmitter()

	res, err := loop.RunV2(context.Background(), AgentRunInputV2{
		AnthropicAPIKey:   "sk-fake",
		AnthropicEndpoint: upstream,
		Model:             "claude-haiku-4-5",
		Mode:              tools.ExecutionCloud,
		History:           []hubMessage{{Role: "user", Content: "hi"}},
		Emitter:           be,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("upstream calls=%d, want 1", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn", res.StopReason)
	}
	be.CloseActiveText()
	if got := be.AccumulatedText(); !strings.Contains(got, "hello") {
		t.Errorf("accumulated text=%q, want to contain 'hello'", got)
	}
}

// 1b) RunSingleTurn 多轮历史（Runtime v3 R4）：prior turns + 当前 prompt 一起
// 走 RunV2，跑通且不破裂。空内容 / 非法 role 的历史轮被跳过。
func TestRunSingleTurn_WithHistory(t *testing.T) {
	loop, script, _, upstream := newRunV2Rig(t, anthropicTextOnly("mauve"))
	be := newTestEmitter()

	res, err := loop.RunSingleTurn(context.Background(), SingleTurnInput{
		AnthropicAPIKey:   "sk-fake",
		AnthropicEndpoint: upstream,
		Model:             "claude-haiku-4-5",
		Prompt:            "what's my favorite color?",
		History: []PriorTurn{
			{Role: "user", Content: "my favorite color is mauve"},
			{Role: "assistant", Content: "Got it — mauve."},
			{Role: "system", Content: "should be skipped"}, // 非法 role → 跳过
			{Role: "user", Content: ""},                    // 空内容 → 跳过
		},
		Emitter: be,
	})
	if err != nil {
		t.Fatalf("RunSingleTurn with history: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("upstream calls=%d, want 1", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn", res.StopReason)
	}
}

// 1c) 空 History → 退化单轮（向后兼容，老行为）。
func TestRunSingleTurn_NoHistoryStillWorks(t *testing.T) {
	loop, script, _, upstream := newRunV2Rig(t, anthropicTextOnly("hi"))
	be := newTestEmitter()
	_, err := loop.RunSingleTurn(context.Background(), SingleTurnInput{
		AnthropicAPIKey:   "sk-fake",
		AnthropicEndpoint: upstream,
		Model:             "claude-haiku-4-5",
		Prompt:            "hello",
		Emitter:           be,
	})
	if err != nil {
		t.Fatalf("RunSingleTurn no history: %v", err)
	}
	if got := script.calls.Load(); got != 1 {
		t.Errorf("upstream calls=%d, want 1", got)
	}
}

// 2) 工具回路：第一 turn tool_use → 内置 echo 工具回 → 第二 turn 收尾
func TestRunV2_ToolRoundTrip(t *testing.T) {
	scene1 := `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"echo"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"x\":\"hi\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}

`
	scene2 := anthropicTextOnly("done")
	loop, script, reg, upstream := newRunV2Rig(t, scene1, scene2)
	be := newTestEmitter()

	// 注册 echo cloud 工具 —— adapter 把它变 biumindkit.Tool
	if err := reg.Register(tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "echo",
			Description: "echos x",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
			Runtime:     tools.RuntimeCloud,
		},
		Invoke: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				X string `json:"x"`
			}
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"echo": args.X}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := loop.RunV2(context.Background(), AgentRunInputV2{
		AnthropicAPIKey:   "sk-fake",
		AnthropicEndpoint: upstream,
		Model:             "claude-haiku-4-5",
		Mode:              tools.ExecutionCloud,
		History:           []hubMessage{{Role: "user", Content: "say hi"}},
		Emitter:           be,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("upstream calls=%d, want 2 (tool_use → text)", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("final stop_reason=%q want end_turn", res.StopReason)
	}
}

// 3) convertHistoryToPrior round-trip：assistant + tool_result 不丢
func TestRunV2_ConvertHistoryToPrior(t *testing.T) {
	history := []hubMessage{
		{Role: "user", Content: "what time is it?"},
		{Role: "assistant", Content: "Let me check.", ToolCalls: []hubToolCall{{
			ID: "toolu_1", Name: "time_now", Input: json.RawMessage(`{}`),
		}}},
		{Role: "tool", ToolCallID: "toolu_1", Content: `{"iso":"2026-05-30T11:00:00Z"}`},
	}
	prior := convertHistoryToPrior(history)
	if len(prior) != 3 {
		t.Fatalf("prior len=%d, want 3", len(prior))
	}
	// user message
	if prior[0].Role != "user" || prior[0].Content[0].Text != "what time is it?" {
		t.Errorf("prior[0]=%+v", prior[0])
	}
	// assistant: text block + tool_use block
	if prior[1].Role != "assistant" {
		t.Errorf("prior[1].Role=%s", prior[1].Role)
	}
	if len(prior[1].Content) != 2 {
		t.Errorf("assistant content len=%d, want 2 (text + tool_use)", len(prior[1].Content))
	}
	var sawToolUse bool
	for _, b := range prior[1].Content {
		if b.Type == "tool_use" && b.ToolUseID == "toolu_1" && b.ToolUseName == "time_now" {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Errorf("assistant missing tool_use block: %+v", prior[1].Content)
	}
	// tool_result（biumindkit 当 user 角色 + content tool_result block）
	if prior[2].Role != "user" {
		t.Errorf("tool_result mapped role=%s, want user (Anthropic shape)", prior[2].Role)
	}
	if len(prior[2].Content) != 1 || prior[2].Content[0].Type != "tool_result" {
		t.Errorf("tool_result content shape lost: %+v", prior[2].Content)
	}
	if prior[2].Content[0].ToolResultID != "toolu_1" {
		t.Errorf("tool_result.ToolResultID=%q want toolu_1", prior[2].Content[0].ToolResultID)
	}
}

// 4) 错误路径：missing APIKey
func TestRunV2_MissingAPIKey(t *testing.T) {
	loop := NewAgentLoop(nil, tools.New())
	_, err := loop.RunV2(context.Background(), AgentRunInputV2{
		Model:   "claude-haiku-4-5",
		History: []hubMessage{{Role: "user", Content: "x"}},
		Emitter: newTestEmitter(),
	})
	if err == nil {
		t.Fatal("expected APIKey-missing error")
	}
	if !strings.Contains(err.Error(), "AnthropicAPIKey") {
		t.Errorf("err msg=%q", err.Error())
	}
}

// 5) 错误路径：history 最后一条不是 user
func TestRunV2_LastMessageMustBeUser(t *testing.T) {
	loop := NewAgentLoop(nil, tools.New())
	_, err := loop.RunV2(context.Background(), AgentRunInputV2{
		AnthropicAPIKey: "sk-fake",
		Model:           "claude-haiku-4-5",
		History:         []hubMessage{{Role: "assistant", Content: "x"}},
		Emitter:         newTestEmitter(),
	})
	if err == nil {
		t.Fatal("expected last-msg-must-be-user error")
	}
}
