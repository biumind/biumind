package engine

import (
	"context"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// pushFrames spawns a goroutine that drains a slice of frames into the
// provided channel and closes it. Used by tests to simulate a streaming
// provider without spinning up a real LLM.
func pushFrames(frames []StreamFrame) <-chan StreamFrame {
	ch := make(chan StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch
}

// drain collects every event written to out into a slice (and reads
// until the test calls close). Returned channel close indicates the
// drainer goroutine has exited.
func drain(out <-chan Event, done chan struct{}) (events []Event) {
	go func() {
		for ev := range out {
			events = append(events, ev)
		}
		close(done)
	}()
	return events // race-free because tests sync on done
}

func TestParseTextOnlyMessage(t *testing.T) {
	frames := pushFrames([]StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{
			ID: "msg_1", Model: "claude-opus-4-7",
			Usage: &StreamUsage{InputTokens: 12},
		}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{Type: "text"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: "text_delta", Text: "Hello"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: "text_delta", Text: " world"}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "end_turn"}, Usage: &StreamUsage{OutputTokens: 7}},
		{Type: FrameMessageStop},
	})

	out := make(chan Event, 16)
	done := make(chan struct{})
	var events []Event
	go func() {
		for ev := range out {
			events = append(events, ev)
		}
		close(done)
	}()

	msg, stop, usage, err := ParseStream(context.Background(), frames, out)
	close(out)
	<-done

	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if stop != "end_turn" {
		t.Errorf("stop_reason = %q", stop)
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
	if msg.Model != "claude-opus-4-7" || msg.ID != "msg_1" {
		t.Errorf("message head wrong: %+v", msg)
	}
	if len(msg.Content) != 1 ||
		msg.Content[0].Type != state.ContentText ||
		msg.Content[0].Text != "Hello world" {
		t.Errorf("content: %+v", msg.Content)
	}

	// Token events: 2 deltas + 1 final usage = 3 stream events.
	var tokens []string
	for _, ev := range events {
		if t, ok := ev.(*StreamTokenEvent); ok {
			tokens = append(tokens, t.Text)
		}
	}
	if len(tokens) != 2 || tokens[0] != "Hello" || tokens[1] != " world" {
		t.Errorf("tokens %+v", tokens)
	}
}

func TestParseToolUseMessage(t *testing.T) {
	// LLM decides to call Bash({"command": "ls"}). The tool_use block's
	// input arrives as fragmented JSON.
	frames := pushFrames([]StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "x"}},
		// First a small leading text block (common: "I'll list files…")
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{Type: "text"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: "text_delta", Text: "Listing."}},
		{Type: FrameContentBlockStop, Index: 0},
		// Then the tool_use block
		{Type: FrameContentBlockStart, Index: 1, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: "toolu_abc", Name: "Bash",
		}},
		{Type: FrameContentBlockDelta, Index: 1, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: `{"comm`,
		}},
		{Type: FrameContentBlockDelta, Index: 1, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: `and": "ls"}`,
		}},
		{Type: FrameContentBlockStop, Index: 1},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "tool_use"}},
		{Type: FrameMessageStop},
	})

	out := make(chan Event, 16)
	msg, stop, _, err := ParseStream(context.Background(), frames, out)
	close(out)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stop != "tool_use" {
		t.Errorf("stop %s", stop)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %+v", len(msg.Content), msg.Content)
	}
	tu := msg.Content[1]
	if tu.Type != state.ContentToolUse ||
		tu.ToolUseName != "Bash" ||
		tu.ToolUseID != "toolu_abc" {
		t.Errorf("tool block: %+v", tu)
	}
	cmd, _ := tu.ToolUseInput["command"].(string)
	if cmd != "ls" {
		t.Errorf("input: %+v", tu.ToolUseInput)
	}
}

func TestParseEmptyToolInput(t *testing.T) {
	// Tool that takes no arguments — server emits no input_json_delta.
	frames := pushFrames([]StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "x"}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: "toolu_z", Name: "TaskList",
		}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "tool_use"}},
		{Type: FrameMessageStop},
	})
	out := make(chan Event, 4)
	msg, _, _, err := ParseStream(context.Background(), frames, out)
	close(out)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].ToolUseName != "TaskList" {
		t.Errorf("content: %+v", msg.Content)
	}
	if got := msg.Content[0].ToolUseInput; len(got) != 0 {
		t.Errorf("input should be empty: %+v", got)
	}
}

func TestParseMalformedToolJSON(t *testing.T) {
	// tool_use with partial JSON that doesn't close — should yield an
	// ErrorEvent and skip the block, not crash.
	frames := pushFrames([]StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "x"}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: "toolu_x", Name: "Bash",
		}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: `{"command":`, // truncated!
		}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "tool_use"}},
		{Type: FrameMessageStop},
	})
	out := make(chan Event, 8)
	collected := make([]Event, 0)
	doneCh := make(chan struct{})
	go func() {
		for ev := range out {
			collected = append(collected, ev)
		}
		close(doneCh)
	}()
	msg, _, _, _ := ParseStream(context.Background(), frames, out)
	close(out)
	<-doneCh

	// Block should be dropped — only the (malformed) tool_use, no
	// healthy content.
	if len(msg.Content) != 0 {
		t.Errorf("expected 0 blocks, got %+v", msg.Content)
	}
	hasErr := false
	for _, ev := range collected {
		if _, ok := ev.(*ErrorEvent); ok {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected ErrorEvent for malformed tool_use json")
	}
}

func TestParseRespectsCancel(t *testing.T) {
	// Slow producer: only send the first frame, then never close.
	frames := make(chan StreamFrame, 1)
	frames <- StreamFrame{Type: FrameMessageStart, Message: &StreamMessageHead{}}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Event, 8)
	doneCh := make(chan struct{})
	go func() {
		_, _, _, err := ParseStream(ctx, frames, out)
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		close(doneCh)
	}()
	time.Sleep(10 * time.Millisecond) // let it consume the first frame
	cancel()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("ParseStream did not exit on cancel")
	}
}

// 推理模型 (Anthropic 原生 extended thinking, 或 model-relay 翻译过来的
// 开源 r1/glm) 走 thinking 块. ParseStream 必须把 thinking 内容流式
// emit 为 token 事件 (端侧靠 reasoning_parser 的 `<think>` 标签识别),
// 但不能把推理段写进 msg.Content (避免污染下轮 history).
func TestParseThinkingBlock(t *testing.T) {
	frames := pushFrames([]StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "glm-5.1"}},
		// thinking block 先到
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{Type: "thinking"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{
			Type: "thinking_delta", Thinking: "Let me think...",
		}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{
			Type: "thinking_delta", Thinking: " 2+2=4.",
		}},
		{Type: FrameContentBlockStop, Index: 0},
		// 答复 text block
		{Type: FrameContentBlockStart, Index: 1, ContentBlock: &StreamBlockHead{Type: "text"}},
		{Type: FrameContentBlockDelta, Index: 1, Delta: &StreamDelta{Type: "text_delta", Text: "Answer: 4"}},
		{Type: FrameContentBlockStop, Index: 1},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "end_turn"}},
		{Type: FrameMessageStop},
	})
	out := make(chan Event, 32)
	done := make(chan struct{})
	var events []Event
	go func() {
		for ev := range out {
			events = append(events, ev)
		}
		close(done)
	}()
	msg, _, _, err := ParseStream(context.Background(), frames, out)
	close(out)
	<-done
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// msg.Content 不该含 thinking — 只有 text 块.
	if len(msg.Content) != 1 || msg.Content[0].Type != state.ContentText ||
		msg.Content[0].Text != "Answer: 4" {
		t.Errorf("msg.Content should only carry the text answer; got %+v", msg.Content)
	}
	// Token 事件应该按顺序: <think>\n / "Let me think..." / " 2+2=4." /
	// </think>\n\n / "Answer: 4"
	var tokens []string
	for _, ev := range events {
		if t, ok := ev.(*StreamTokenEvent); ok {
			tokens = append(tokens, t.Text)
		}
	}
	want := []string{"<think>\n", "Let me think...", " 2+2=4.", "</think>\n\n", "Answer: 4"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens len=%d want %d: %#v", len(tokens), len(want), tokens)
	}
	for i, w := range want {
		if tokens[i] != w {
			t.Errorf("token[%d]=%q want %q", i, tokens[i], w)
		}
	}
}

func TestParseProviderError(t *testing.T) {
	frames := pushFrames([]StreamFrame{
		{Type: FrameError, Error: &StreamError{Type: "overloaded", Message: "rate limited"}},
	})
	out := make(chan Event, 4)
	_, _, _, err := ParseStream(context.Background(), frames, out)
	close(out)
	if err == nil {
		t.Errorf("expected error from provider error frame")
	}
}
