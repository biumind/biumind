package chat

// agent_test.go — 内核收敛回归：wiki 维护 agent（RunAgentLoop SSE 变体）
// 跑在 RunV2/biumindkit 内核上时，对客户端的 SSE 事件协议保持不变。
// fake relay 喂 Anthropic Messages SSE（anthropicScript，见
// agent_v2_test.go）—— 即生产路径 model-relay 的 verbatim anthropic 形态。
//
// 覆盖：文本流式事件序列、工具 round-trip（tool.created → tool.completed）、
// tool_use / tool_calls 两种 stop 词汇、中途错误、max_turns 终态。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

// newTestEmitter makes a BlockEmitter writing into a discardable
// recorder. The kernel's emitter usage is exercised; the SSE output is
// asserted separately (agent_test.go / blockemitter_test.go).
func newTestEmitter() *BlockEmitter {
	rec := httptest.NewRecorder()
	rwf := &recorderWithFlush{ResponseRecorder: rec}
	return NewBlockEmitter(rwf, rwf, uuid.New())
}

// sseEvent 是解析后的一帧 SSE（event 名 + raw data JSON）。
type sseEvent struct {
	event string
	data  string
}

// parseSSEFrames 把 BlockEmitter 写出的 SSE 字节流拆成有序帧列表。
func parseSSEFrames(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, chunk := range strings.Split(body, "\n\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(line, "event: ") {
				ev.event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
		if ev.event == "" {
			t.Fatalf("SSE frame without event line: %q", chunk)
		}
		out = append(out, ev)
	}
	return out
}

// eventSeq 投影出事件名序列，方便顺序断言。
func eventSeq(events []sseEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.event)
	}
	return out
}

// findEvent 返回第一帧名为 name 的事件 data（不存在 → nil）。
func findEvent(events []sseEvent, name string) map[string]any {
	for _, e := range events {
		if e.event != name {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(e.data), &m); err != nil {
			return nil
		}
		return m
	}
	return nil
}

// countEvent 统计名为 name 的帧数。
func countEvent(events []sseEvent, name string) int {
	n := 0
	for _, e := range events {
		if e.event == name {
			n++
		}
	}
	return n
}

// newAgentLoopSSERig 起一个 fake relay（anthropicScript）+ HTTPSender，
// 返回 sender / script / 以及执行一次 RunAgentLoop 的 runner（自带
// recorderWithFlush，返回解析后的 SSE 帧序列）。
func newAgentLoopSSERig(t *testing.T, scenes ...string) (
	*HTTPSender, *anthropicScript,
	func(in AgentLoopRunInput) (*AgentRunResult, []sseEvent, error),
) {
	t.Helper()
	script := &anthropicScript{scenes: scenes}
	srv := httptest.NewServer(script.handler())
	t.Cleanup(srv.Close)
	sender := NewHTTPSender(nil, srv.URL)

	run := func(in AgentLoopRunInput) (*AgentRunResult, []sseEvent, error) {
		rec := httptest.NewRecorder()
		rwf := &recorderWithFlush{ResponseRecorder: rec}
		req := httptest.NewRequest(http.MethodPost, "/v1/wiki/projects/x/agent/run", nil)
		req.Header.Set("Authorization", "Bearer user-jwt")
		res, err := sender.RunAgentLoop(context.Background(), rwf, req, in)
		return res, parseSSEFrames(t, rec.Body.String()), err
	}
	return sender, script, run
}

// 1) 文本流式：单 turn 文本回答。断言 SSE 事件序列保持 ChunkType v2
// 协议：block.create → block.delta（+ legacy delta）→ block.complete →
// message.done（+ legacy done），且 message.done 带 stop_reason 与
// assistant_message_id。同时钉住 PassThrough 契约：上游收到
// Authorization: Bearer <user JWT> 与 X-Stream-Format: anthropic。
func TestRunAgentLoopSSE_TextStream(t *testing.T) {
	_, script, run := newAgentLoopSSERig(t, anthropicTextOnly("整理完成。"))

	res, events, err := run(AgentLoopRunInput{
		System:   "sys",
		UserText: "整理这个项目",
		Model:    "test-model",
		OwnerID:  uuid.New(),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop_reason: got %q want end_turn", res.StopReason)
	}

	seq := eventSeq(events)
	wantPrefix := []string{"block.create", "block.delta", "delta"}
	if len(seq) < len(wantPrefix) {
		t.Fatalf("events too short: %v", seq)
	}
	for i, w := range wantPrefix {
		if seq[i] != w {
			t.Fatalf("event[%d]: got %q want %q (full: %v)", i, seq[i], w, seq)
		}
	}
	// 结尾：block.complete → message.done → done（legacy）。
	tail := seq[len(seq)-3:]
	if tail[0] != "block.complete" || tail[1] != "message.done" || tail[2] != "done" {
		t.Errorf("tail events: got %v", tail)
	}
	done := findEvent(events, "message.done")
	if done == nil {
		t.Fatal("missing message.done")
	}
	if done["stop_reason"] != "end_turn" {
		t.Errorf("message.done stop_reason: got %v", done["stop_reason"])
	}
	if done["assistant_message_id"] == "" {
		t.Errorf("message.done missing assistant_message_id: %v", done)
	}
	// text delta 内容经 block.delta 送达。
	bd := findEvent(events, "block.delta")
	if bd == nil || bd["delta"] != "整理完成。" {
		t.Errorf("block.delta payload: %v", bd)
	}

	// PassThrough 契约。
	if got := script.calls.Load(); got != 1 {
		t.Fatalf("upstream calls=%d, want 1", got)
	}
	if script.auths[0] != "Bearer user-jwt" {
		t.Errorf("upstream Authorization: got %q", script.auths[0])
	}
	if script.streamFormats[0] != "anthropic" {
		t.Errorf("upstream X-Stream-Format: got %q want anthropic", script.streamFormats[0])
	}
}

// 2) 工具 round-trip：第一 turn tool_use（echo）→ 工具执行 → 第二 turn
// 收尾文本。断言：2 次上游调用；第二次请求 body 带 tool_result 内容；
// SSE 上 tool.created 先于 tool.completed；message.done 收尾。
func TestRunAgentLoopSSE_ToolRoundTrip(t *testing.T) {
	sender, script, run := newAgentLoopSSERig(t,
		anthropicToolUseScene("toolu_1", "echo", `{"msg":"ping"}`),
		anthropicTextOnly("Done."),
	)
	reg := tools.New()
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, in json.RawMessage) (any, error) {
			return map[string]any{"echoed": string(in)}, nil
		},
	})
	sender.Tools = reg

	res, events, err := run(AgentLoopRunInput{
		System:   "sys",
		UserText: "do it",
		Model:    "m",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("final stop_reason: got %q", res.StopReason)
	}
	if got := script.calls.Load(); got != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", got)
	}
	// 第二次请求必须带 tool_result（biumindkit 拼回 Anthropic 形状）。
	body2 := script.bodies[1]
	if !strings.Contains(body2, "tool_result") || !strings.Contains(body2, "echoed") {
		t.Errorf("second request missing tool_result: %s", body2)
	}

	seq := eventSeq(events)
	created, completed, doneAt := -1, -1, -1
	for i, e := range seq {
		switch e {
		case "tool.created":
			created = i
		case "tool.completed":
			completed = i
		case "message.done":
			doneAt = i
		}
	}
	if created < 0 || completed < 0 || doneAt < 0 {
		t.Fatalf("missing tool/done events: %v", seq)
	}
	if !(created < completed && completed < doneAt) {
		t.Errorf("event order: created=%d completed=%d done=%d (%v)",
			created, completed, doneAt, seq)
	}
	tc := findEvent(events, "tool.created")
	if tc["name"] != "echo" {
		t.Errorf("tool.created name: got %v", tc["name"])
	}
}

// 3) 两种 stop 词汇。tool_use（Anthropic 原生）走 3a 已覆盖；这里钉住
// 3b：未归一的 OpenAI 词汇 "tool_calls" 漏到 brain 时必须**响亮失败**
// （block.error），而不是像修前的 v1 那样静默退出、工具一个都不执行。
// 生产路径的词汇归一在 model-relay mapStopReason
// （model-relay/internal/api/anthropic_stream.go，有独立测试）；本测试是
// 防回归双保险：relay 归一失效时维护 run 表面上是失败而不是假成功。
func TestRunAgentLoopSSE_ToolCallsVocabFailsLoud(t *testing.T) {
	// tool_use block 齐全，但 message_delta 的 stop_reason 是未归一的
	// OpenAI 词汇。
	scene := `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"echo"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_calls"}}

event: message_stop
data: {"type":"message_stop"}

`
	sender, _, run := newAgentLoopSSERig(t, scene)
	reg := tools.New()
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	sender.Tools = reg

	_, events, err := run(AgentLoopRunInput{UserText: "go", Model: "m"})
	if err == nil {
		t.Fatal("expected error when relay leaks un-normalized stop vocabulary")
	}
	be := findEvent(events, "block.error")
	if be == nil || be["code"] != "agent_failed" {
		t.Errorf("missing agent_failed block.error: %v", eventSeq(events))
	}
	// 不允许 message.done —— 那会让客户端以为 run 成功。
	if countEvent(events, "message.done") != 0 {
		t.Errorf("message.done must not appear on failure: %v", eventSeq(events))
	}
}

// 4) 错误中途：第一 turn tool_use 正常，第二次上游调用 500。断言：
// RunAgentLoop 返回错误，SSE 上是 block.error（agent_failed）+ legacy
// error，没有 message.done。
func TestRunAgentLoopSSE_MidStreamError(t *testing.T) {
	sender, script, run := newAgentLoopSSERig(t,
		anthropicToolUseScene("toolu_1", "echo", `{"msg":"ping"}`),
		// 无第二 scene —— 第二次调用命中 scenes exhausted → 500。
	)
	reg := tools.New()
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	sender.Tools = reg

	_, events, err := run(AgentLoopRunInput{UserText: "go", Model: "m"})
	if err == nil {
		t.Fatal("expected error when relay 500s mid-loop")
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("upstream calls=%d, want 2", got)
	}
	be := findEvent(events, "block.error")
	if be == nil || be["code"] != "agent_failed" {
		t.Errorf("missing agent_failed block.error: %v", eventSeq(events))
	}
	if countEvent(events, "error") != 1 {
		t.Errorf("legacy error event count: %v", eventSeq(events))
	}
	if countEvent(events, "message.done") != 0 {
		t.Errorf("message.done must not appear on failure: %v", eventSeq(events))
	}
}

// 5) max_turns：模型不停要工具，MaxTurns=2 触顶。触顶是**正常终态**：
// message.done 带 stop_reason=max_turns（跟 v1 契约一致），RunAgentLoop
// 不返回错误。
func TestRunAgentLoopSSE_MaxTurns(t *testing.T) {
	toolScene := anthropicToolUseScene("toolu_1", "echo", `{}`)
	sender, script, run := newAgentLoopSSERig(t, toolScene, toolScene, toolScene)
	reg := tools.New()
	reg.MustRegister(tools.Tool{
		Descriptor: tools.Descriptor{Name: "echo", Runtime: tools.RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	sender.Tools = reg

	res, events, err := run(AgentLoopRunInput{
		UserText: "go", Model: "m", MaxTurns: 2,
	})
	if err != nil {
		t.Fatalf("max_turns must be a graceful terminal state, got err: %v", err)
	}
	if res.StopReason != "max_turns" {
		t.Errorf("stop_reason: got %q want max_turns", res.StopReason)
	}
	if got := script.calls.Load(); got != 2 {
		t.Errorf("upstream calls=%d, want 2 (capped)", got)
	}
	done := findEvent(events, "message.done")
	if done == nil || done["stop_reason"] != "max_turns" {
		t.Errorf("message.done stop_reason: got %v (events: %v)", done, eventSeq(events))
	}
}
