package agentplane

import (
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/agent"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func TestExternalEventToFrame(t *testing.T) {
	cases := []struct {
		name string
		ev   agent.Event
		want any // 期望的 frame 具体类型；nil = 期望返回 nil
	}{
		{"text", agent.Event{Type: agent.EventText, Content: "hi"}, &sdkproto.SDKStreamlinedText{}},
		{"empty-text-dropped", agent.Event{Type: agent.EventText, Content: ""}, nil},
		{"tool_use", agent.Event{Type: agent.EventToolUse, Tool: &agent.ToolEvent{ID: "t1", Name: "Bash"}}, &sdkproto.SDKToolProgress{}},
		{"tool_result", agent.Event{Type: agent.EventToolResult, Tool: &agent.ToolEvent{ID: "t1", Output: "ok"}}, &sdkproto.SDKToolUseSummary{}},
		{"done", agent.Event{Type: agent.EventDone}, &sdkproto.SDKResultSuccess{}},
		{"error", agent.Event{Type: agent.EventError, Content: "boom"}, &sdkproto.SDKResultError{}},
		{"system-dropped", agent.Event{Type: agent.EventSystem}, nil},
		{"raw-dropped", agent.Event{Type: agent.EventRaw}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := externalEventToFrame(tc.ev, "sess-1")
			if tc.want == nil {
				if got != nil {
					t.Fatalf("want nil frame, got %T", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %T, got nil", tc.want)
			}
			// 类型匹配检查。
			switch tc.want.(type) {
			case *sdkproto.SDKStreamlinedText:
				if _, ok := got.(*sdkproto.SDKStreamlinedText); !ok {
					t.Fatalf("want SDKStreamlinedText, got %T", got)
				}
			case *sdkproto.SDKToolProgress:
				if _, ok := got.(*sdkproto.SDKToolProgress); !ok {
					t.Fatalf("want SDKToolProgress, got %T", got)
				}
			case *sdkproto.SDKToolUseSummary:
				if _, ok := got.(*sdkproto.SDKToolUseSummary); !ok {
					t.Fatalf("want SDKToolUseSummary, got %T", got)
				}
			case *sdkproto.SDKResultSuccess:
				if _, ok := got.(*sdkproto.SDKResultSuccess); !ok {
					t.Fatalf("want SDKResultSuccess, got %T", got)
				}
			case *sdkproto.SDKResultError:
				if _, ok := got.(*sdkproto.SDKResultError); !ok {
					t.Fatalf("want SDKResultError, got %T", got)
				}
			}
		})
	}
}

// session_id 兜底：event 自带 SessionID 优先,否则用 caller 入参。
func TestExternalEventToFrame_SessionFallback(t *testing.T) {
	f := externalEventToFrame(agent.Event{Type: agent.EventText, Content: "x"}, "caller-sid")
	st, ok := f.(*sdkproto.SDKStreamlinedText)
	if !ok || st.SessionID != "caller-sid" {
		t.Fatalf("fallback session_id 未生效: %+v", f)
	}
	f2 := externalEventToFrame(agent.Event{Type: agent.EventText, Content: "x", SessionID: "ev-sid"}, "caller-sid")
	st2, _ := f2.(*sdkproto.SDKStreamlinedText)
	if st2.SessionID != "ev-sid" {
		t.Fatalf("event 自带 session_id 应优先, got %q", st2.SessionID)
	}
}
