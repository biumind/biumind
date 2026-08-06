package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// collectFrames drains a Stream channel into a slice. Fails if Stream
// returned an error.
func collectFrames(t *testing.T, prov *OpenAIEngineProvider, req engine.StreamRequest) []engine.StreamFrame {
	t.Helper()
	ch, err := prov.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	var got []engine.StreamFrame
	for f := range ch {
		got = append(got, f)
	}
	return got
}

func frameTypes(got []engine.StreamFrame) []engine.StreamFrameType {
	out := make([]engine.StreamFrameType, len(got))
	for i, f := range got {
		out[i] = f.Type
	}
	return out
}

// parseCollected feeds collected frames back through ParseStream to
// verify the assembled state.Message.
func parseCollected(t *testing.T, frames []engine.StreamFrame) (state.Message, string) {
	t.Helper()
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	ev := make(chan engine.Event, 64)
	msg, stop, _, err := engine.ParseStream(context.Background(), ch, ev)
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	return msg, stop
}

// newOpenAISSEServer stands up a fake /v1/chat/completions that writes
// the given SSE payload, capturing the inbound request for body/header
// assertions.
func newOpenAISSEServer(t *testing.T, sse string, status int) (*httptest.Server, func() *http.Request) {
	t.Helper()
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(buf))
		captured = r.Clone(context.Background())
		if status >= 400 {
			http.Error(w, "nope", status)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	t.Cleanup(srv.Close)
	return srv, func() *http.Request { return captured }
}

// ─── Inbound: streaming state machine ─────────────────────────────────

func TestOpenAIEngine_TextTurn(t *testing.T) {
	const sse = `data: {"id":"c1","model":"gpt-test","choices":[{"delta":{"role":"assistant","content":"Hel"}}]}

data: {"id":"c1","model":"gpt-test","choices":[{"delta":{"content":"lo"}}]}

data: {"id":"c1","model":"gpt-test","choices":[{"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]

`
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())

	want := []engine.StreamFrameType{
		engine.FrameMessageStart,
		engine.FrameContentBlockStart,
		engine.FrameContentBlockDelta,
		engine.FrameContentBlockDelta,
		engine.FrameContentBlockStop,
		engine.FrameMessageDelta, // stop_reason
		engine.FrameMessageDelta, // usage
		engine.FrameMessageStop,
	}
	if !reflect.DeepEqual(frameTypes(got), want) {
		t.Fatalf("frame seq = %v, want %v", frameTypes(got), want)
	}
	if got[1].ContentBlock.Type != "text" {
		t.Errorf("block type = %q, want text", got[1].ContentBlock.Type)
	}
	// Verify usage fallback (input tokens delivered via message_delta).
	var usage *engine.StreamUsage
	for _, f := range got {
		if f.Type == engine.FrameMessageDelta && f.Usage != nil {
			usage = f.Usage
		}
	}
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want {in:10 out:2}", usage)
	}
	// ParseStream assembles the text.
	msg, stop := parseCollected(t, got)
	if stop != "end_turn" {
		t.Errorf("stop = %q, want end_turn", stop)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != state.ContentText || msg.Content[0].Text != "Hello" {
		t.Errorf("msg.Content = %+v, want single text block \"Hello\"", msg.Content)
	}
}

func TestOpenAIEngine_ReasoningThenText(t *testing.T) {
	const sse = `data: {"id":"c1","model":"r1","choices":[{"delta":{"reasoning_content":"think"}}]}

data: {"id":"c1","model":"r1","choices":[{"delta":{"content":"ans"}}]}

data: {"id":"c1","model":"r1","choices":[{"finish_reason":"stop"}]}

data: [DONE]

`
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())

	// thinking block opens at index 0, sealed when text opens at index 1.
	if got[1].Type != engine.FrameContentBlockStart || got[1].ContentBlock.Type != "thinking" || got[1].Index != 0 {
		t.Errorf("thinking open = %+v, want thinking@0", got[1])
	}
	// find text block start — must be index 1
	var textStart *engine.StreamFrame
	for i := range got {
		if got[i].Type == engine.FrameContentBlockStart && got[i].ContentBlock.Type == "text" {
			textStart = &got[i]
			break
		}
	}
	if textStart == nil || textStart.Index != 1 {
		t.Errorf("text block open = %+v, want text@1", textStart)
	}
	// reasoning must NOT leak into msg.Content (only streamed as tokens).
	msg, _ := parseCollected(t, got)
	if len(msg.Content) != 1 || msg.Content[0].Text != "ans" {
		t.Errorf("msg.Content = %+v, want single text \"ans\" (reasoning excluded)", msg.Content)
	}
}

func TestOpenAIEngine_ToolUse(t *testing.T) {
	const sse = `data: {"id":"c1","model":"g","choices":[{"delta":{"content":"let me check"}}]}

data: {"id":"c1","model":"g","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}

data: {"id":"c1","model":"g","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}

data: {"id":"c1","model":"g","choices":[{"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())

	// tool_use block must open after text sealed, carry id+name.
	var toolStart *engine.StreamFrame
	for i := range got {
		if got[i].Type == engine.FrameContentBlockStart && got[i].ContentBlock.Type == "tool_use" {
			toolStart = &got[i]
			break
		}
	}
	if toolStart == nil {
		t.Fatalf("no tool_use content_block_start emitted")
	}
	if toolStart.ContentBlock.ID != "call_1" || toolStart.ContentBlock.Name != "get_weather" {
		t.Errorf("tool_use block = %+v, want id=call_1 name=get_weather", toolStart)
	}
	// finish_reason=tool_calls → stop_reason=tool_use.
	msg, stop := parseCollected(t, got)
	if stop != "tool_use" {
		t.Errorf("stop = %q, want tool_use", stop)
	}
	// Content = [text, tool_use{input:{city:SF}}]
	if len(msg.Content) != 2 {
		t.Fatalf("msg.Content len = %d, want 2", len(msg.Content))
	}
	if msg.Content[0].Text != "let me check" {
		t.Errorf("text block = %q", msg.Content[0].Text)
	}
	tu := msg.Content[1]
	if tu.Type != state.ContentToolUse || tu.ToolUseName != "get_weather" {
		t.Errorf("tool_use block = %+v", tu)
	}
	if city, _ := tu.ToolUseInput["city"].(string); city != "SF" {
		t.Errorf("tool_use input = %+v, want city=SF", tu.ToolUseInput)
	}
}

func TestOpenAIEngine_TwoParallelTools(t *testing.T) {
	const sse = `data: {"id":"c1","model":"g","choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"f0","arguments":""}},{"index":1,"id":"b","type":"function","function":{"name":"f1","arguments":""}}]}}]}

data: {"id":"c1","model":"g","choices":[{"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())

	// Two distinct tool_use blocks, distinct anthropic indices.
	var toolStarts []engine.StreamFrame
	for _, f := range got {
		if f.Type == engine.FrameContentBlockStart && f.ContentBlock.Type == "tool_use" {
			toolStarts = append(toolStarts, f)
		}
	}
	if len(toolStarts) != 2 {
		t.Fatalf("tool_use blocks = %d, want 2", len(toolStarts))
	}
	if toolStarts[0].Index == toolStarts[1].Index {
		t.Errorf("two tool blocks share index %d", toolStarts[0].Index)
	}
	ids := map[string]bool{toolStarts[0].ContentBlock.ID: true, toolStarts[1].ContentBlock.ID: true}
	if !ids["a"] || !ids["b"] {
		t.Errorf("tool ids = %v, want {a,b}", ids)
	}
}

func TestOpenAIEngine_MissingFinishReason(t *testing.T) {
	// Stream ends with [DONE], no finish_reason — adapter must
	// synthesise a graceful end_turn + message_stop.
	const sse = `data: {"id":"c1","model":"g","choices":[{"delta":{"content":"hi"}}]}

data: [DONE]

`
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())

	if got[len(got)-1].Type != engine.FrameMessageStop {
		t.Errorf("last frame = %v, want message_stop", got[len(got)-1].Type)
	}
	var sawStopReason bool
	for _, f := range got {
		if f.Type == engine.FrameMessageDelta && f.Delta != nil && f.Delta.StopReason == "end_turn" {
			sawStopReason = true
		}
	}
	if !sawStopReason {
		t.Error("missing synthesised stop_reason=end_turn")
	}
}

func TestOpenAIEngine_MalformedSSE(t *testing.T) {
	const sse = "data: {not valid json\n\ndata: [DONE]\n\n"
	srv, _ := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)
	got := collectFrames(t, prov, streamReqMinimal())
	if len(got) != 1 || got[0].Type != engine.FrameError {
		t.Fatalf("frames = %+v, want single FrameError", got)
	}
	if got[0].Error.Type != "decode_failure" {
		t.Errorf("error type = %q, want decode_failure", got[0].Error.Type)
	}
}

func TestOpenAIEngine_HTTPError(t *testing.T) {
	srv, _ := newOpenAISSEServer(t, "", http.StatusUnauthorized)
	prov := NewOpenAIEngine("k", srv.URL)
	ch, err := prov.Stream(context.Background(), streamReqMinimal())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if ch != nil {
		t.Errorf("expected nil channel for 401, got %v", ch)
	}
}

// ─── Outbound: request body + auth ────────────────────────────────────

func TestOpenAIEngine_BearerAuth(t *testing.T) {
	const sse = "data: [DONE]\n\n"
	srv, captured := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("openai-secret", srv.URL)
	_ = collectFrames(t, prov, streamReqMinimal())
	if got := captured().Header.Get("Authorization"); got != "Bearer openai-secret" {
		t.Errorf("Authorization = %q, want Bearer openai-secret", got)
	}
	if got := captured().Header.Get("x-api-key"); got != "" {
		t.Errorf("x-api-key = %q, must be empty (OpenAI-compat uses Bearer)", got)
	}
}

func TestOpenAIEngine_OutboundBody(t *testing.T) {
	const sse = "data: [DONE]\n\n"
	srv, captured := newOpenAISSEServer(t, sse, 0)
	prov := NewOpenAIEngine("k", srv.URL)

	// Request carrying: system + tool_result turn (two results) +
	// assistant tool-use turn + a tool definition.
	req := engine.StreamRequest{
		Model:  "gpt-test",
		System: "you are helpful",
		Tools: []engine.ToolSpec{{
			Name: "get_weather", Description: "weather",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}},
		Messages: []state.Message{
			{Role: state.RoleUser, Content: []state.ContentBlock{{Type: state.ContentText, Text: "weather?"}}},
			// assistant tool-use turn (empty text → GLM sentinel)
			{Role: state.RoleAssistant, Content: []state.ContentBlock{{
				Type: state.ContentToolUse, ToolUseID: "call_1", ToolUseName: "get_weather",
				ToolUseInput: map[string]any{"city": "SF"},
			}}},
			// tool-result turn (one user message with TWO result blocks)
			{Role: state.RoleUser, Content: []state.ContentBlock{
				{Type: state.ContentToolResult, ToolResultID: "call_1",
					ToolResultContent: []state.ContentBlock{{Type: state.ContentText, Text: "sunny"}}},
				{Type: state.ContentToolResult, ToolResultID: "call_2",
					ToolResultContent: []state.ContentBlock{{Type: state.ContentText, Text: "windy"}}},
			}},
		},
		MaxTokens: 128,
	}
	_ = collectFrames(t, prov, req)

	var body openAIRequest
	dec := json.NewDecoder(captured().Body)
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	// stream_options.include_usage must be set.
	if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
		t.Error("StreamOptions.IncludeUsage not set")
	}
	// tools schema present with parameters.
	if len(body.Tools) != 1 || body.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools = %+v", body.Tools)
	}
	// First message is the folded system.
	if body.Messages[0].Role != "system" {
		t.Errorf("messages[0] = %+v, want role=system", body.Messages[0])
	}

	roles := make([]string, 0, len(body.Messages))
	for _, m := range body.Messages {
		roles = append(roles, m.Role)
	}
	// Expect: system, user(q), assistant(tool_use), tool(call_1), tool(call_2).
	wantRoles := []string{"system", "user", "assistant", "tool", "tool"}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Errorf("roles = %v, want %v", roles, wantRoles)
	}
	// The two tool_result blocks must split into two separate role=tool
	// messages with correct tool_call_id linkage.
	var toolMsgs []openAIMessage
	for _, m := range body.Messages {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("role=tool messages = %d, want 2", len(toolMsgs))
	}
	if toolMsgs[0].ToolCallID != "call_1" || toolMsgs[1].ToolCallID != "call_2" {
		t.Errorf("tool_call_id linkage = %q/%q, want call_1/call_2",
			toolMsgs[0].ToolCallID, toolMsgs[1].ToolCallID)
	}
	// Assistant tool-use turn must carry tool_calls + GLM sentinel content.
	var asst *openAIMessage
	for i := range body.Messages {
		if body.Messages[i].Role == "assistant" {
			asst = &body.Messages[i]
			break
		}
	}
	if asst == nil {
		t.Fatal("no assistant message")
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" ||
		asst.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("assistant tool_calls = %+v", asst.ToolCalls)
	}
	if content, _ := asst.Content.(string); content != " " {
		t.Errorf("assistant empty-text sentinel = %q, want \" \" (GLM-5.1)", content)
	}
}
