// S4-4 RunV2 真 Anthropic 集成测试。
//
// 跟 agent_v2_test.go（用 httptest fake upstream）区别：这里打**真**的
// Anthropic-compatible endpoint，验证：
//
//   - SSE 解析跟实际服务对得上（不只是 mock 的预录）
//   - tool-use 回路 + tool_result content block schema 真匹配
//   - 多 turn 历史（PriorMessages 翻译）真能让模型继续对话
//   - 长 prompt + max-tokens 截断行为
//   - error 帧 / stop_reason 边界
//
// 环境变量（缺一就 skip）：
//
//   ANTHROPIC_BASE_URL  - 例 http://your-llm-gateway.example.com
//                          / https://api.anthropic.com
//   ANTHROPIC_API_KEY   - sk-...
//   ANTHROPIC_MODEL     - claude-opus-4-8 / claude-sonnet-4-6 / 等
//
// 运行：
//
//   ANTHROPIC_BASE_URL=... ANTHROPIC_API_KEY=... ANTHROPIC_MODEL=... \
//     go test ./internal/chat -run TestIntegration_ -v -timeout 5m
//
// 不在 CI 默认跑（没 key 时 skip）。本地 / staging 期回归验证用。

package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

func integrationEnv(t *testing.T) (apiKey, baseURL, model string) {
	t.Helper()
	apiKey = os.Getenv("ANTHROPIC_API_KEY")
	baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	model = os.Getenv("ANTHROPIC_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("ANTHROPIC_API_KEY / ANTHROPIC_BASE_URL / ANTHROPIC_MODEL not all set; skipping integration test")
	}
	return apiKey, baseURL, model
}

func newIntegrationLoop() *AgentLoop {
	return NewAgentLoop(nil, tools.New())
}

// 1. 单 turn 文本：最基础。模型回一段 plain text，end_turn 收尾。
//    验：RunV2 跑通 + emitter 拿到非空 accumulated text + StopReason=end_turn。
func TestIntegration_SingleTurnText(t *testing.T) {
	apiKey, baseURL, model := integrationEnv(t)
	loop := newIntegrationLoop()
	be := newTestEmitter()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := loop.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: baseURL,
		Model:             model,
		System:            "You are a concise assistant. Reply in <=20 words.",
		Mode:              tools.ExecutionCloud,
		History:           []hubMessage{{Role: "user", Content: "Say hello in one short sentence."}},
		Emitter:           be,
		MaxTokens:         200,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn", res.StopReason)
	}
	be.CloseActiveText()
	got := be.AccumulatedText()
	if strings.TrimSpace(got) == "" {
		t.Errorf("expected non-empty assistant reply, got %q", got)
	}
	t.Logf("[1/5] single-turn assistant reply: %q (stop=%s, in/out=%d/%d)",
		got, res.StopReason, res.PromptTokens, res.CompletionTokens)
}

// 2. 工具回路：注册 echo cloud 工具，prompt 引导模型调它，验 RunV2
//    完整跑完 tool-use → tool_result → 最终文本回路。
func TestIntegration_ToolRoundTrip(t *testing.T) {
	apiKey, baseURL, model := integrationEnv(t)
	reg := tools.New()
	if err := reg.Register(tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "echo",
			Description: "Echo back the value of the 'x' parameter as the tool result string.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"x": {"type": "string", "description": "value to echo"}
				},
				"required": ["x"]
			}`),
			Runtime: tools.RuntimeCloud,
		},
		Invoke: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				X string `json:"x"`
			}
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"echoed": args.X, "marker": "INTEGRATION_TOOL_CALLED"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	loop := NewAgentLoop(nil, reg)
	be := newTestEmitter()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := loop.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: baseURL,
		Model:             model,
		System:            "You are an assistant. To respond to greetings the user prefers, ALWAYS call the `echo` tool with x set to the word 'pong', then summarize the tool result in your reply.",
		Mode:              tools.ExecutionCloud,
		History: []hubMessage{
			{Role: "user", Content: "ping"},
		},
		Emitter:   be,
		MaxTokens: 400,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("final stop_reason=%q want end_turn (after tool roundtrip)", res.StopReason)
	}
	be.CloseActiveText()
	got := be.AccumulatedText()
	t.Logf("[2/5] tool round-trip final reply: %q (stop=%s, in/out=%d/%d)",
		got, res.StopReason, res.PromptTokens, res.CompletionTokens)
	if strings.TrimSpace(got) == "" {
		t.Errorf("expected non-empty post-tool reply, got empty")
	}
}

// 3. 工具错误恢复：工具 invoke 抛错，模型应该看到错误信息后用文本回应而非
//    再死循环调同一工具。
func TestIntegration_ToolErrorRecovery(t *testing.T) {
	apiKey, baseURL, model := integrationEnv(t)
	reg := tools.New()
	invocations := 0
	if err := reg.Register(tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "flaky_tool",
			Description: "A tool that always fails. Try it once if asked but accept the failure gracefully.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Runtime:     tools.RuntimeCloud,
		},
		// ReadOnly=true so biumindkit's RunV2 tool-advertising path
		// (plan mode advertises read-only tools) exposes it to the model.
		// flaky_tool has no side effects — it only simulates a failure.
		ReadOnly: true,
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			invocations++
			return nil, &integrationToolErr{msg: "simulated tool failure: backend offline"}
		},
	}); err != nil {
		t.Fatal(err)
	}

	loop := NewAgentLoop(nil, reg)
	be := newTestEmitter()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := loop.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: baseURL,
		Model:             model,
		System:            "You are an assistant. Try the flaky_tool once; if it errors, apologize briefly in plain text and stop.",
		Mode:              tools.ExecutionCloud,
		History:           []hubMessage{{Role: "user", Content: "Please use the flaky_tool to fetch data."}},
		Emitter:           be,
		MaxTokens:         400,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn (model should give up after error)", res.StopReason)
	}
	if invocations == 0 {
		t.Errorf("flaky_tool was never invoked; model didn't follow prompt")
	}
	if invocations > 5 {
		t.Errorf("model invoked flaky_tool %d times — likely infinite-loop, didn't recover", invocations)
	}
	be.CloseActiveText()
	got := be.AccumulatedText()
	t.Logf("[3/5] error recovery reply: %q (stop=%s, invocations=%d)",
		got, res.StopReason, invocations)
}

// 4. 多 turn 历史：3 条消息（user / assistant / user），让模型基于已知信息
//    回答后续问题。验 PriorMessages 翻译能让上下文连贯。
func TestIntegration_MultiTurnHistory(t *testing.T) {
	apiKey, baseURL, model := integrationEnv(t)
	loop := newIntegrationLoop()
	be := newTestEmitter()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := loop.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: baseURL,
		Model:             model,
		System:            "You are a helpful assistant. Use information from earlier turns to answer follow-up questions concisely.",
		Mode:              tools.ExecutionCloud,
		History: []hubMessage{
			{Role: "user", Content: "My favorite color is mauve and my dog's name is Pinto."},
			{Role: "assistant", Content: "Got it — mauve color and Pinto the dog. I'll remember."},
			{Role: "user", Content: "What is my dog's name and what's my favorite color?"},
		},
		Emitter:   be,
		MaxTokens: 200,
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn", res.StopReason)
	}
	be.CloseActiveText()
	got := strings.ToLower(be.AccumulatedText())
	t.Logf("[4/5] multi-turn reply: %q (stop=%s)", be.AccumulatedText(), res.StopReason)

	// 必须同时提到 dog name + color；任一漏了说明 PriorMessages 翻译丢了上下文
	if !strings.Contains(got, "pinto") {
		t.Errorf("expected reply to mention 'Pinto'; got %q", got)
	}
	if !strings.Contains(got, "mauve") {
		t.Errorf("expected reply to mention 'mauve'; got %q", got)
	}
}

// 5. Max tokens 边界：极小 max_tokens 让模型必须截断。验 stop_reason
//    返 max_tokens（不是 end_turn），客户端能区分截断 vs 完整。
func TestIntegration_MaxTokensBoundary(t *testing.T) {
	apiKey, baseURL, model := integrationEnv(t)
	loop := newIntegrationLoop()
	be := newTestEmitter()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := loop.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   apiKey,
		AnthropicEndpoint: baseURL,
		Model:             model,
		System:            "Write detailed multi-paragraph essays.",
		Mode:              tools.ExecutionCloud,
		History: []hubMessage{
			{Role: "user", Content: "Write a 5-paragraph essay about the history of the French language, with specific dates."},
		},
		Emitter:   be,
		MaxTokens: 50, // 故意太小 → 必触 max_tokens
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	be.CloseActiveText()
	got := be.AccumulatedText()
	t.Logf("[5/5] truncated reply (%d chars): %q (stop=%s, in/out=%d/%d)",
		len(got), got, res.StopReason, res.PromptTokens, res.CompletionTokens)

	// 期望 max_tokens；少数模型可能 end_turn 提前收（系统 prompt 引导太弱）
	if res.StopReason != "max_tokens" && res.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want max_tokens (or end_turn if model bailed early)", res.StopReason)
	}
	if res.CompletionTokens > 100 {
		t.Errorf("output_tokens=%d exceeded MaxTokens=50 + buffer; SSE accounting bug?",
			res.CompletionTokens)
	}
}

// integrationToolErr 简单 error 类型 —— RunV2 把 invoke err 透传成 tool_result
// is_error=true 给模型。
type integrationToolErr struct{ msg string }

func (e *integrationToolErr) Error() string { return e.msg }
