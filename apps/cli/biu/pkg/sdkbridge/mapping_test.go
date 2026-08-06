package sdkbridge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// 给每种 biumindkit.Event 一条单测，验证 toSDKFrame 选对类型 + 关键字段保留。
//
// session_id 一处常量；uuid 不直接断言（每帧随机），但断言 ≠ 空。

const testSessionID = "s-test-1"

// AssistantText is the assembled-summary event biumindkit emits AFTER
// the per-chunk StreamingText events. Forwarding both would render the
// same text 2× on the client (the bug fixed in the dedup pass — see
// mapping.go for full rationale). The mapping must therefore return
// nil for AssistantText: only StreamingText flows to the wire.
func TestMapping_AssistantText_Dropped(t *testing.T) {
	frame := ToSDKFrame(biumindkit.AssistantText{
		Text: "hello world", StopReason: "end_turn",
	}, testSessionID)
	if frame != nil {
		t.Fatalf("AssistantText must NOT produce a frame (got %T) — would duplicate StreamingText", frame)
	}
}

// StreamingText is the canonical text-bearing event. Each chunk gets
// one frame; client buffers and renders incrementally.
func TestMapping_StreamingText(t *testing.T) {
	frame := ToSDKFrame(biumindkit.StreamingText{Text: "hel"}, testSessionID)
	st, ok := frame.(*sdkproto.SDKStreamlinedText)
	if !ok {
		t.Fatalf("StreamingText → %T, want *SDKStreamlinedText", frame)
	}
	if st.Text != "hel" {
		t.Errorf("text=%q want hel", st.Text)
	}
	if st.SessionID != testSessionID || st.UUID == "" {
		t.Errorf("session_id/uuid lost: %+v", st)
	}
}

func TestMapping_ToolStart(t *testing.T) {
	frame := ToSDKFrame(biumindkit.ToolStart{
		ID: "tu1", Name: "Bash", Input: map[string]any{"cmd": "ls"},
	}, testSessionID)
	tp, ok := frame.(*sdkproto.SDKToolProgress)
	if !ok {
		t.Fatalf("ToolStart → %T, want *SDKToolProgress", frame)
	}
	if tp.ToolUseID != "tu1" || tp.ToolName != "Bash" {
		t.Errorf("got %+v", tp)
	}
	if tp.ElapsedTimeSeconds != 0 {
		t.Errorf("ToolStart should have elapsed=0, got %v", tp.ElapsedTimeSeconds)
	}
}

func TestMapping_ToolResult(t *testing.T) {
	frame := ToSDKFrame(biumindkit.ToolResult{
		ID: "tu1", Name: "Bash", Output: "file1\nfile2", IsError: false,
	}, testSessionID)
	tus, ok := frame.(*sdkproto.SDKToolUseSummary)
	if !ok {
		t.Fatalf("ToolResult → %T, want *SDKToolUseSummary", frame)
	}
	if tus.Summary != "file1\nfile2" {
		t.Errorf("summary lost: %q", tus.Summary)
	}
	if len(tus.PrecedingToolUseIDs) != 1 || tus.PrecedingToolUseIDs[0] != "tu1" {
		t.Errorf("preceding ids: %v", tus.PrecedingToolUseIDs)
	}
}

func TestMapping_CompactStarted(t *testing.T) {
	frame := ToSDKFrame(biumindkit.CompactStarted{
		Reason: "auto", TokensBefore: 50000,
	}, testSessionID)
	cs, ok := frame.(*sdkproto.BiumindCompactStarted)
	if !ok {
		t.Fatalf("CompactStarted → %T, want *BiumindCompactStarted", frame)
	}
	if cs.Reason != "auto" || cs.TokensBefore != 50000 {
		t.Errorf("got %+v", cs)
	}
	if cs.SessionID != testSessionID {
		t.Errorf("session_id lost")
	}
}

func TestMapping_CompactFinished(t *testing.T) {
	frame := ToSDKFrame(biumindkit.CompactFinished{
		TokensBefore: 50000, TokensAfter: 12000, TokensSaved: 38000,
	}, testSessionID)
	cf, ok := frame.(*sdkproto.BiumindCompactFinished)
	if !ok {
		t.Fatalf("CompactFinished → %T, want *BiumindCompactFinished", frame)
	}
	if cf.TokensSaved != 38000 {
		t.Errorf("tokens_saved=%d", cf.TokensSaved)
	}
}

func TestMapping_Error(t *testing.T) {
	frame := ToSDKFrame(biumindkit.Error{
		Err: errors.New("rate limited"), Recoverable: true,
	}, testSessionID)
	re, ok := frame.(*sdkproto.SDKResultError)
	if !ok {
		t.Fatalf("Error → %T, want *SDKResultError", frame)
	}
	if !re.IsError {
		t.Error("is_error should be true")
	}
	if re.Subtype != "error_during_execution" {
		t.Errorf("subtype=%q", re.Subtype)
	}
	// errors[0] 应该包含 message + recoverable
	if len(re.Errors) != 1 {
		t.Fatalf("errors len=%d", len(re.Errors))
	}
	var e map[string]any
	if err := json.Unmarshal(re.Errors[0], &e); err != nil {
		t.Fatalf("unmarshal errors[0]: %v", err)
	}
	if e["message"] != "rate limited" || e["recoverable"] != true {
		t.Errorf("error body lost: %+v", e)
	}
}

func TestMapping_Done(t *testing.T) {
	frame := ToSDKFrame(biumindkit.Done{
		StopReason:       "end_turn",
		InputTokens:      100,
		OutputTokens:     200,
		CacheReadTokens:  50,
		CacheWriteTokens: 10,
		Elapsed:          2 * time.Second,
	}, testSessionID)
	rs, ok := frame.(*sdkproto.SDKResultSuccess)
	if !ok {
		t.Fatalf("Done → %T, want *SDKResultSuccess", frame)
	}
	if rs.IsError {
		t.Error("is_error should be false")
	}
	if rs.DurationMs != 2000 {
		t.Errorf("duration_ms=%d want 2000", rs.DurationMs)
	}
	if rs.StopReason == nil || *rs.StopReason != "end_turn" {
		t.Errorf("stop_reason: %v", rs.StopReason)
	}
	// usage 应该有 input_tokens / output_tokens
	var u map[string]any
	if err := json.Unmarshal(rs.Usage, &u); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if u["input_tokens"].(float64) != 100 || u["output_tokens"].(float64) != 200 {
		t.Errorf("usage tokens lost: %+v", u)
	}
}

// 用一个未实现的 sdkEvent 作 fallback 测试。biumindkit 内部 sdkEvent 接口
// 是 unexported method —— 我们不能在外部包定义新 impl。所以直接传 nil
// 强制 default 分支。
func TestMapping_UnknownFallback(t *testing.T) {
	frame := ToSDKFrame(nil, testSessionID)
	st, ok := frame.(*sdkproto.SDKStreamlinedText)
	if !ok {
		t.Fatalf("nil event → %T, want fallback *SDKStreamlinedText", frame)
	}
	if !strings.Contains(st.Text, "unsupported") {
		t.Errorf("fallback text should mention 'unsupported', got %q", st.Text)
	}
}

// F10/F13: ToSDKFrame should pull session_id from the event itself when
// present, fall back to caller-supplied id only when event has none.
// parent_tool_use_id flows from event into SDKToolProgress (and other
// frames that have the field) as *string omitempty.

func TestMapping_EventSessionID_Wins(t *testing.T) {
	// Event carries its own session id — caller-supplied is ignored.
	ev := biumindkit.StreamingText{
		BaseEvent: biumindkit.BaseEvent{EventSessionID: "from-event"},
		Text:      "hi",
	}
	frame := ToSDKFrame(ev, "from-caller")
	st := frame.(*sdkproto.SDKStreamlinedText)
	if st.SessionID != "from-event" {
		t.Errorf("SessionID = %q, want from-event (event field beats caller arg)",
			st.SessionID)
	}
}

func TestMapping_CallerSessionIDFallback(t *testing.T) {
	// Event has empty session id — caller-supplied takes over.
	ev := biumindkit.StreamingText{Text: "hi"}
	frame := ToSDKFrame(ev, "from-caller")
	st := frame.(*sdkproto.SDKStreamlinedText)
	if st.SessionID != "from-caller" {
		t.Errorf("SessionID = %q, want from-caller fallback", st.SessionID)
	}
}

func TestMapping_ParentToolUseID_ToolStart(t *testing.T) {
	ev := biumindkit.ToolStart{
		BaseEvent: biumindkit.BaseEvent{
			EventSessionID:       "s1",
			EventParentToolUseID: "tu_outer_42",
		},
		ID: "tu_inner", Name: "Read",
	}
	frame := ToSDKFrame(ev, "")
	tp, ok := frame.(*sdkproto.SDKToolProgress)
	if !ok {
		t.Fatalf("ToolStart → %T, want *SDKToolProgress", frame)
	}
	if tp.ParentToolUseID == nil {
		t.Fatal("ParentToolUseID nil — should have been filled from event")
	}
	if *tp.ParentToolUseID != "tu_outer_42" {
		t.Errorf("ParentToolUseID = %q, want tu_outer_42", *tp.ParentToolUseID)
	}
}

func TestMapping_ParentToolUseID_OmittedWhenEmpty(t *testing.T) {
	// Top-level agent event: no ParentToolUseID. SDK Protocol field is
	// *string omitempty — must be nil so the JSON skips the field.
	ev := biumindkit.ToolStart{
		BaseEvent: biumindkit.BaseEvent{EventSessionID: "s1"},
		ID:        "tu_root", Name: "Read",
	}
	frame := ToSDKFrame(ev, "")
	tp := frame.(*sdkproto.SDKToolProgress)
	if tp.ParentToolUseID != nil {
		t.Errorf("ParentToolUseID = %v, want nil (root agent)", *tp.ParentToolUseID)
	}
}

func TestMapping_ParentToolUseID_AssistantBlock_ToolUse(t *testing.T) {
	// AssistantBlock(tool_use) is the other emit site that produces
	// SDKToolProgress. parent_tool_use_id must flow there too.
	ev := biumindkit.AssistantBlock{
		BaseEvent: biumindkit.BaseEvent{
			EventSessionID:       "s1",
			EventParentToolUseID: "tu_outer",
		},
		Block: biumindkit.ContentBlock{
			Type:        biumindkit.ContentToolUse,
			ToolUseID:   "tu_inner",
			ToolUseName: "Read",
		},
	}
	frame := ToSDKFrame(ev, "")
	tp, ok := frame.(*sdkproto.SDKToolProgress)
	if !ok {
		t.Fatalf("AssistantBlock(tool_use) → %T, want *SDKToolProgress", frame)
	}
	if tp.ParentToolUseID == nil || *tp.ParentToolUseID != "tu_outer" {
		t.Errorf("ParentToolUseID lost: %v", tp.ParentToolUseID)
	}
}
