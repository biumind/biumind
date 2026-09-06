// AskUserQuestion 可见性 + 应答链路测试（agent-ask-form P0/P1）。
//
// P0 地雷背景：AskUserQuestion 曾对所有 biumindkit embedder 无条件注册，
// 但 translate 没有 UserQuestionAskEvent case —— 模型一旦调用，事件被丢、
// engine 的 Decision channel 永远无人应答，session 死锁到超时。修复后的
// 契约：
//
//   - Options.AskUser == nil → 工具不进 catalog（模型根本看不到）；
//     万一模型幻觉出该工具名，engine 走 unknown-tool soft error，不死锁。
//   - Options.AskUser != nil → 工具可见；引擎事件被 Submit 拦截并路由到
//     handler，handler 的答案回注 Decision channel，工具产出 tool_result。
//   - handler 报错 / 返回 Cancelled → 工具 soft error，turn 继续。

package biumindkit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// scriptedProvider 按脚本逐 turn 回放流帧（engine 包内部测试同款手法，
// 这里在包外重新实现一遍）。同时记录每次请求的 tool catalog，供
// 可见性断言。
type scriptedProvider struct {
	mu      sync.Mutex
	scripts [][]engine.StreamFrame
	calls   int
	// toolsSeen 记录每次 Stream 请求携带的工具名列表。
	toolsSeen [][]string
}

func (p *scriptedProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	p.mu.Lock()
	if p.calls >= len(p.scripts) {
		p.mu.Unlock()
		return nil, errors.New("scripted provider out of scripts")
	}
	frames := p.scripts[p.calls]
	p.calls++
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	p.toolsSeen = append(p.toolsSeen, names)
	p.mu.Unlock()
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

func (p *scriptedProvider) catalog(call int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if call >= len(p.toolsSeen) {
		return nil
	}
	return p.toolsSeen[call]
}

func textTurn(text string) []engine.StreamFrame {
	return []engine.StreamFrame{
		{Type: engine.FrameMessageStart, Message: &engine.StreamMessageHead{Model: "test"}},
		{Type: engine.FrameContentBlockStart, Index: 0, ContentBlock: &engine.StreamBlockHead{Type: "text"}},
		{Type: engine.FrameContentBlockDelta, Index: 0, Delta: &engine.StreamDelta{Type: "text_delta", Text: text}},
		{Type: engine.FrameContentBlockStop, Index: 0},
		{Type: engine.FrameMessageDelta, Delta: &engine.StreamDelta{StopReason: "end_turn"}},
		{Type: engine.FrameMessageStop},
	}
}

func toolUseTurn(useID, name, inputJSON string) []engine.StreamFrame {
	return []engine.StreamFrame{
		{Type: engine.FrameMessageStart, Message: &engine.StreamMessageHead{Model: "test"}},
		{Type: engine.FrameContentBlockStart, Index: 0, ContentBlock: &engine.StreamBlockHead{
			Type: "tool_use", ID: useID, Name: name,
		}},
		{Type: engine.FrameContentBlockDelta, Index: 0, Delta: &engine.StreamDelta{
			Type: "input_json_delta", PartialJSON: inputJSON,
		}},
		{Type: engine.FrameContentBlockStop, Index: 0},
		{Type: engine.FrameMessageDelta, Delta: &engine.StreamDelta{StopReason: "tool_use"}},
		{Type: engine.FrameMessageStop},
	}
}

const askQuestionInput = `{"questions":[{"question":"Pick a color?","header":"Color",` +
	`"options":[{"label":"red","description":"warm"},{"label":"blue","description":"cool"}]}]}`

// drainWithTimeout 收干 Submit channel；超时就 fail（死锁探针 —— 修复前
// 模型调 AskUserQuestion 时 channel 永不关闭）。
func drainWithTimeout(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("Submit channel did not close within 10s — likely deadlocked on an unanswered Decision channel")
			return nil
		}
	}
}

func containsTool(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// P0 回归：未配 AskUser handler 时 AskUserQuestion 不在工具目录里。
// 这就是 brain chat 模式（修复前）死锁的拆雷证据。
func TestAskUserQuestion_HiddenWithoutHandler(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]engine.StreamFrame{
		// 模型幻觉出一个未注册的工具名 —— engine 应 soft error 并继续，
		// 而不是死锁。
		toolUseTurn("tu_1", "AskUserQuestion", askQuestionInput),
		textTurn("done"),
	}}
	a, err := New(Options{
		Provider:            prov,
		Model:               "test",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	events := drainWithTimeout(t, a.Submit(ctx, "hi"))

	if containsTool(prov.catalog(0), "AskUserQuestion") {
		t.Error("AskUserQuestion must NOT be in the catalog when Options.AskUser is nil")
	}
	// 幻觉调用 → IsError 的 tool result（unknown tool），turn 正常结束。
	var sawToolErr, sawDone bool
	for _, ev := range events {
		switch e := ev.(type) {
		case ToolResult:
			if e.ID == "tu_1" && e.IsError {
				sawToolErr = true
			}
		case Done:
			sawDone = true
		}
	}
	if !sawToolErr {
		t.Error("expected an IsError ToolResult for the hallucinated AskUserQuestion call")
	}
	if !sawDone {
		t.Error("expected Done event — turn must complete")
	}
}

// P1 链路：配了 handler 时工具可见，提问路由到 handler，答案回注成
// tool_result（"User has answered" 形态）。
func TestAskUserQuestion_RoutedToHandler(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]engine.StreamFrame{
		toolUseTurn("tu_1", "AskUserQuestion", askQuestionInput),
		textTurn("red it is"),
	}}
	var gotQuestion UserQuestion
	a, err := New(Options{
		Provider:            prov,
		Model:               "test",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
		AskUser: func(_ context.Context, q UserQuestion) (UserAnswer, error) {
			gotQuestion = q
			return UserAnswer{Selected: []int{0}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	events := drainWithTimeout(t, a.Submit(ctx, "hi"))

	if !containsTool(prov.catalog(0), "AskUserQuestion") {
		t.Error("AskUserQuestion must be in the catalog when Options.AskUser is set")
	}
	if gotQuestion.Question != "Pick a color?" {
		t.Errorf("handler received question %q, want %q", gotQuestion.Question, "Pick a color?")
	}
	if len(gotQuestion.Options) != 2 || gotQuestion.Options[0].Label != "red" {
		t.Errorf("handler options = %+v, want red/blue", gotQuestion.Options)
	}
	var result ToolResult
	var sawResult, sawDone bool
	for _, ev := range events {
		switch e := ev.(type) {
		case ToolResult:
			if e.ID == "tu_1" {
				result, sawResult = e, true
			}
		case Done:
			sawDone = true
		}
	}
	if !sawResult {
		t.Fatal("expected ToolResult for tu_1")
	}
	if result.IsError {
		t.Errorf("answered question should not be an error, got %q", result.Output)
	}
	if want := `User has answered`; !contains(result.Output, want) {
		t.Errorf("tool result %q missing %q", result.Output, want)
	}
	if want := `"red"`; !contains(result.Output, want) {
		t.Errorf("tool result %q missing selected label %q", result.Output, want)
	}
	if !sawDone {
		t.Error("expected Done event — turn must complete")
	}
}

// handler 返回 Cancelled（用户 decline/cancel）→ 工具 soft error，turn
// 继续跑完（模型降级）。这是超时 / 用户不答的统一收口形态。
func TestAskUserQuestion_CancelDegradesToSoftError(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]engine.StreamFrame{
		toolUseTurn("tu_1", "AskUserQuestion", askQuestionInput),
		textTurn("proceeding with a default"),
	}}
	a, err := New(Options{
		Provider:            prov,
		Model:               "test",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
		AskUser: func(_ context.Context, _ UserQuestion) (UserAnswer, error) {
			return UserAnswer{Cancelled: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	events := drainWithTimeout(t, a.Submit(ctx, "hi"))

	var sawSoftErr, sawDone bool
	for _, ev := range events {
		switch e := ev.(type) {
		case ToolResult:
			if e.ID == "tu_1" && e.IsError && contains(e.Output, "cancelled") {
				sawSoftErr = true
			}
		case Done:
			sawDone = true
		}
	}
	if !sawSoftErr {
		t.Error("expected soft-error ToolResult mentioning cancellation")
	}
	if !sawDone {
		t.Error("expected Done event — cancelled question must not wedge the turn")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
