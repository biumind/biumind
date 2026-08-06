// S4-0 follow-up patches — F1 / F2 / F3 / F5 各一个测试。
//
// 这些 follow-up 是 brain S4 的阻塞缺口（详见 PUBLIC_API.md §6.1）：
//
//	F1 MCPRegistry wrapper          —— brain 不再直接 import internal/mcp
//	F2 AssistantBlock event + alias —— brain JsonEmitter 拿 per-block 视图
//	F3 Options.PriorMessages         —— brain 注入 thread 历史
//	F5 Agent.Interrupt()             —— SDK Protocol Interrupt 透传到 engine
//
// 测试都用假 Anthropic upstream（httptest）跑端到端 Submit，这样行为不
// 依赖网络也不依赖真模型。

package biumindkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeUpstream 起一个最小的 SSE upstream：assistant 说 "ok"，stop_reason
// end_turn。Submit 走 Anthropic provider → SSE 解析 → engine event →
// translate → SDK Event。
func fakeAnthropicUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
}

// TestSDK_FollowupF1 ：MCPRegistry wrapper accepts nil + non-nil inner，
// Options.MCPRegistry 类型为 *MCPRegistry。brain 传 nil 不应该 panic。
func TestSDK_FollowupF1(t *testing.T) {
	// nil wrapper：brain S4 chat mode 的典型用法
	a, err := New(Options{
		APIKey:              "sk-fake",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		MCPRegistry:         nil, // 显式 nil，验证类型签名 + 内部 nil-check
	})
	if err != nil {
		t.Fatalf("New with nil MCPRegistry: %v", err)
	}
	_ = a.Close()

	// NewMCPRegistry(nil) 也返 nil，让 cmd/biu 不需要双重 nil-check
	if got := NewMCPRegistry(nil); got != nil {
		t.Errorf("NewMCPRegistry(nil) = %v, want nil", got)
	}

	// MCPRegistry.Inner() 在 nil receiver 上不 panic
	var nilReg *MCPRegistry
	if got := nilReg.Inner(); got != nil {
		t.Errorf("nilReg.Inner() = %v, want nil", got)
	}
}

// TestSDK_FollowupF2 ：AssistantBlock 在 AssistantText 之前 fire；
// ContentBlock / ContentText 等 alias 可访问。
func TestSDK_FollowupF2(t *testing.T) {
	upstream := fakeAnthropicUpstream(t)
	defer upstream.Close()

	a, err := New(Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	var blocks []AssistantBlock
	var textSeen bool
	for ev := range a.Submit(context.Background(), "hi") {
		switch e := ev.(type) {
		case AssistantBlock:
			blocks = append(blocks, e)
			if textSeen {
				t.Errorf("AssistantBlock fired AFTER AssistantText —— wrong order")
			}
		case AssistantText:
			textSeen = true
		}
	}

	if len(blocks) == 0 {
		t.Fatal("expected at least one AssistantBlock event")
	}
	first := blocks[0]
	if first.Block.Type != ContentText {
		t.Errorf("block type=%q want %q", first.Block.Type, ContentText)
	}
	if first.Block.Text != "ok" {
		t.Errorf("block text=%q want ok", first.Block.Text)
	}
	if first.Index != 0 {
		t.Errorf("block index=%d want 0", first.Index)
	}
	if first.StopReason != "end_turn" {
		t.Errorf("block stop_reason=%q want end_turn", first.StopReason)
	}

	// alias smoke-check：ContentText / ContentToolUse / ContentBlock 类型存在
	var b ContentBlock
	b.Type = ContentToolUse
	if b.Type != ContentToolUse {
		t.Errorf("alias ContentBlock.Type assignment fail")
	}
}

// TestSDK_FollowupF3 ：PriorMessages 喂给 engine 后，state.Snapshot
// 包含这些消息（不需要真发 LLM 请求）。
func TestSDK_FollowupF3(t *testing.T) {
	prior := []Message{
		{Role: "user", Content: []ContentBlock{{Type: ContentText, Text: "earlier question"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: ContentText, Text: "earlier answer"}}, StopReason: "end_turn"},
	}
	a, err := New(Options{
		APIKey:              "sk-fake",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		PriorMessages:       prior,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	snap := a.eng.State().Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len=%d, want 2", len(snap))
	}
	if string(snap[0].Role) != "user" || snap[0].Content[0].Text != "earlier question" {
		t.Errorf("msg[0]=%+v", snap[0])
	}
	if string(snap[1].Role) != "assistant" || snap[1].StopReason != "end_turn" {
		t.Errorf("msg[1]=%+v", snap[1])
	}
}

// TestSDK_FollowupF5 ：Submit 跑起来后，另一个 goroutine 调 Interrupt
// 让在飞 turn 立即结束（Done event 出现）。
//
// 用一个永不返回的 upstream（hijack 后 hold connection）模拟正常会卡住
// 的 LLM stream，验证 Interrupt 真的能砍断 ctx。
func TestSDK_FollowupF5(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// 写一个 message_start 然后阻塞，直到 ctx cancel
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	a, err := New(Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// 没有 in-flight Submit 时 Interrupt 是 no-op
	if err := a.Interrupt(); err != nil {
		t.Errorf("Interrupt with no in-flight Submit: %v", err)
	}

	// 启 Submit；100ms 后 Interrupt
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := a.Submit(ctx, "long prompt")

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = a.Interrupt()
	}()

	// 收集事件直到 channel close。如果 Interrupt 不工作，会阻塞到 5s ctx
	// 超时（测试 fail）。
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
drain:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break drain
			}
		case <-deadline.C:
			t.Fatal("Submit did not finish after Interrupt — ctx cancel not propagated")
		}
	}

	// 之后 Interrupt 又是 no-op
	if err := a.Interrupt(); err != nil {
		t.Errorf("Interrupt after channel close: %v", err)
	}
	_ = strings.Builder{} // anti-unused
}
