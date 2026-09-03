// P2 #19 RunV2 接线测试（agent-42 遗留）：检索预算三件套（预算耗尽拒绝 /
// signature 去重拒绝 / 连空早停）在 biumindkit 内核路径同样生效。
//
// 形态镜像 retrieval_guard_test.go（v1），差别在于这里喂 Anthropic
// Messages SSE（anthropicScript）走 RunV2：关卡包在检索工具的 Invoker 里
// （retrievalGuard.WrapTool），拒绝经 biumindkit soft tool error 回到模型，
// 断言点是 invoker 实际执行次数 + 下一次上游请求 body 里的拒绝文案。

package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// anthropicToolUseScene 一个 assistant turn：调用 `name`（raw JSON args），
// stop_reason=tool_use。镜像 agent_v2_test.go TestRunV2_ToolRoundTrip 的
// scene1。
func anthropicToolUseScene(id, name, args string) string {
	esc, _ := json.Marshal(args) // args 作为字符串嵌进 input_json_delta
	return `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"` + id + `","name":"` + name + `"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + string(esc) + `}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}

`
}

// registerRetrievalV2 注册检索类 stub，返回 result；calls 计实际执行次数
// （被拒绝的调用不许到达 invoker）。
func registerRetrievalV2(reg *tools.Registry, name string, result any, calls *atomic.Int32) {
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: name, Runtime: tools.RuntimeCloud},
		Retrieval:  true,
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			calls.Add(1)
			return result, nil
		},
	})
}

func runV2(t *testing.T, loop *AgentLoop, upstream string) {
	t.Helper()
	_, err := loop.RunV2(context.Background(), AgentRunInputV2{
		AnthropicAPIKey:   "sk-fake",
		AnthropicEndpoint: upstream,
		Model:             "claude-haiku-4-5",
		Mode:              tools.ExecutionCloud,
		History:           []hubMessage{{Role: "user", Content: "go"}},
		Emitter:           newTestEmitter(),
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
}

// 1) 预算耗尽：budget=1，第二次检索（不同参数）被拒绝，invoker 只跑 1 次。
func TestRunV2_RetrievalBudgetExceeded(t *testing.T) {
	loop, script, reg, upstream := newRunV2Rig(t,
		anthropicToolUseScene("toolu_1", "wiki_search", `{"query":"alpha"}`),
		anthropicToolUseScene("toolu_2", "wiki_search", `{"query":"beta"}`),
		anthropicTextOnly("done"),
	)
	var calls atomic.Int32
	registerRetrievalV2(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{map[string]any{"title": "hit"}}},
		&calls)
	loop.RetrievalBudget = 1

	runV2(t, loop, upstream)

	if got := calls.Load(); got != 1 {
		t.Errorf("invoker ran %d times, want 1 (budget=1)", got)
	}
	if got := script.calls.Load(); got != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", got)
	}
	body := script.bodies[2]
	if !strings.Contains(body, "retrieval budget exhausted") {
		t.Errorf("3rd request missing budget rejection tool_result: %s", body)
	}
}

// 2) signature 去重：同工具 + 语义相同参数（大小写/空白归一化后相等）被拒，
// 即使预算充足；invoker 只跑 1 次。
func TestRunV2_DuplicateRetrievalRejected(t *testing.T) {
	loop, script, reg, upstream := newRunV2Rig(t,
		anthropicToolUseScene("toolu_1", "websearch", `{"query":" Foo "}`),
		anthropicToolUseScene("toolu_2", "websearch", `{"query":"foo"}`),
		anthropicTextOnly("done"),
	)
	var calls atomic.Int32
	registerRetrievalV2(reg, "websearch",
		map[string]any{"query": "foo", "results": []any{map[string]any{"title": "hit"}}},
		&calls)
	loop.RetrievalBudget = 10

	runV2(t, loop, upstream)

	if got := calls.Load(); got != 1 {
		t.Errorf("invoker ran %d times, want 1 (2nd call is a duplicate)", got)
	}
	body := script.bodies[2]
	if !strings.Contains(body, "duplicate call") {
		t.Errorf("3rd request missing duplicate rejection tool_result: %s", body)
	}
}

// 3) 连空早停：连续 2 次空结果触达 streak limit（2），第 3 次检索被拒。
func TestRunV2_NoYieldEarlyStop(t *testing.T) {
	loop, script, reg, upstream := newRunV2Rig(t,
		anthropicToolUseScene("toolu_1", "wiki_search", `{"query":"q1"}`),
		anthropicToolUseScene("toolu_2", "wiki_search", `{"query":"q2"}`),
		anthropicToolUseScene("toolu_3", "wiki_search", `{"query":"q3"}`),
		anthropicTextOnly("done"),
	)
	var calls atomic.Int32
	registerRetrievalV2(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{}}, // 永远空
		&calls)
	loop.RetrievalBudget = 10
	loop.NoYieldStreakLimit = 2

	runV2(t, loop, upstream)

	if got := calls.Load(); got != 2 {
		t.Errorf("invoker ran %d times, want 2 (streak limit hit)", got)
	}
	body := script.bodies[3]
	if !strings.Contains(body, "no new information") {
		t.Errorf("4th request missing no-yield rejection tool_result: %s", body)
	}
}

// 4) 零预算（默认）不动 loop：两次相同检索都执行 —— 0=关 的语义在 RunV2
// 同样成立。
func TestRunV2_ZeroBudgetDisablesGuard(t *testing.T) {
	loop, script, reg, upstream := newRunV2Rig(t,
		anthropicToolUseScene("toolu_1", "wiki_search", `{"query":"same"}`),
		anthropicToolUseScene("toolu_2", "wiki_search", `{"query":"same"}`),
		anthropicTextOnly("done"),
	)
	var calls atomic.Int32
	registerRetrievalV2(reg, "wiki_search",
		map[string]any{"query": "x", "results": []any{}},
		&calls)
	// RetrievalBudget 保持 0。

	runV2(t, loop, upstream)

	if got := calls.Load(); got != 2 {
		t.Errorf("invoker ran %d times, want 2 (guard disabled)", got)
	}
	if got := script.calls.Load(); got != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", got)
	}
}
