// F10/F13 — biumindkit Event interface exposes SessionID() /
// ParentToolUseID() via embedded BaseEvent. Translate() copies the
// metadata across the engine ↔ SDK boundary.

package biumindkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSDK_EventsCarrySessionID — Submit-emitted events carry the
// agent's AgentID as SessionID. Embedders relying on the interface
// methods get a non-empty value without per-event type assertions.
func TestSDK_EventsCarrySessionID(t *testing.T) {
	upstream := fakeAnthropicUpstream(t)
	defer upstream.Close()

	a, err := New(Options{
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
		SessionID:           "test-session-abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var seen int
	for ev := range a.Submit(ctx, "go") {
		seen++
		if ev.SessionID() != "test-session-abc" {
			t.Errorf("event %T SessionID = %q, want test-session-abc",
				ev, ev.SessionID())
		}
		// Top-level agent has no parent tool_use.
		if ev.ParentToolUseID() != "" {
			t.Errorf("event %T ParentToolUseID = %q, want empty",
				ev, ev.ParentToolUseID())
		}
	}
	if seen == 0 {
		t.Fatal("no events emitted")
	}
}

// TestSDK_BaseEventDirectAccessors — Manually constructed events still
// expose the accessors so test fixtures can assert routing without
// going through Submit. Verifies BaseEvent's value receiver methods
// work via embedding.
func TestSDK_BaseEventDirectAccessors(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"StreamingText", StreamingText{
			BaseEvent: BaseEvent{EventSessionID: "s1", EventParentToolUseID: "tu_p"},
			Text:      "hi",
		}},
		{"ToolStart", ToolStart{
			BaseEvent: BaseEvent{EventSessionID: "s1", EventParentToolUseID: "tu_p"},
			ID:        "tu_inner", Name: "Read",
		}},
		{"Done", Done{
			BaseEvent:  BaseEvent{EventSessionID: "s1", EventParentToolUseID: "tu_p"},
			StopReason: "end_turn",
		}},
		{"AssistantBlock", AssistantBlock{
			BaseEvent: BaseEvent{EventSessionID: "s1", EventParentToolUseID: "tu_p"},
			Index:     0,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ev.SessionID() != "s1" {
				t.Errorf("SessionID = %q, want s1", tc.ev.SessionID())
			}
			if tc.ev.ParentToolUseID() != "tu_p" {
				t.Errorf("ParentToolUseID = %q, want tu_p", tc.ev.ParentToolUseID())
			}
		})
	}
}

// holdingUpstreamForMeta exposes the same shape as fakeAnthropicUpstream
// but lets us assert metadata on streaming events too. We reuse the
// existing fake; nothing new needed here.
var _ = httptest.NewServer
var _ http.Handler
