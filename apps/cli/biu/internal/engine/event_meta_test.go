// F10/F13 — every Event leaving QueryEngine.Submit carries SessionID
// and ParentToolUseID via the embedded baseEvent. The engine's Submit
// goroutine stamps metadata at the channel boundary so individual emit
// sites stay free of the bookkeeping; these tests verify the stamping
// actually runs and that sub-agents inherit the parent's tool_use_id.

package engine

import (
	"context"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// TestEventMeta_TopLevelStampsSessionID — events from a root engine
// carry the configured AgentID as SessionID and have an empty
// ParentToolUseID (root agent has no parent).
func TestEventMeta_TopLevelStampsSessionID(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("hello"),
	}}
	st := state.New()
	reg := NewRegistry()
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		AgentID:           "root-session-1",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drainAll(eng.Submit(context.Background(), "hi"))
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, ev := range events {
		if ev.SessionID() != "root-session-1" {
			t.Errorf("event %T SessionID = %q, want root-session-1",
				ev, ev.SessionID())
		}
		if ev.ParentToolUseID() != "" {
			t.Errorf("event %T ParentToolUseID = %q, want empty (root)",
				ev, ev.ParentToolUseID())
		}
	}
}

// TestEventMeta_SubAgentInheritsParentToolUseID — when the engine is
// spawned with ParentToolUseID set, every event it emits carries that
// id. This is what AgentTool relies on so its sub-agent's events can
// be linked back to the AgentTool tool_use that started them (F13).
func TestEventMeta_SubAgentInheritsParentToolUseID(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("sub-agent reply"),
	}}
	st := state.New()
	reg := NewRegistry()
	sub, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		AgentID:           "sub-1",
		ParentToolUseID:   "tu_outer_42",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drainAll(sub.Submit(context.Background(), "go"))
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, ev := range events {
		if ev.SessionID() != "sub-1" {
			t.Errorf("event %T SessionID = %q, want sub-1",
				ev, ev.SessionID())
		}
		if ev.ParentToolUseID() != "tu_outer_42" {
			t.Errorf("event %T ParentToolUseID = %q, want tu_outer_42",
				ev, ev.ParentToolUseID())
		}
	}
}

// TestEventMeta_ToolEventsCarryMeta — verifies tool-related events
// (start, result) inherit metadata too. They flow through the same
// stamping forwarder but route via a different code path inside
// runner.go than text/done events.
func TestEventMeta_ToolEventsCarryMeta(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running", "tu_1", "Read", `{"path":"/a"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	read := &fakeTool{name: "Read", readOnly: true, concurrencySafe: true}
	reg.Register(read)
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		AgentID:           "s-tool-meta",
		ParentToolUseID:   "tu_outer",
		BypassPermissions: true,
	})

	events := drainAll(eng.Submit(context.Background(), "read /a"))
	var sawStart, sawResult bool
	for _, ev := range events {
		switch e := ev.(type) {
		case *ToolUseStartEvent:
			sawStart = true
			if e.SessionID() != "s-tool-meta" || e.ParentToolUseID() != "tu_outer" {
				t.Errorf("ToolUseStart meta: session=%q parent=%q",
					e.SessionID(), e.ParentToolUseID())
			}
		case *ToolUseResultEvent:
			sawResult = true
			if e.SessionID() != "s-tool-meta" || e.ParentToolUseID() != "tu_outer" {
				t.Errorf("ToolUseResult meta: session=%q parent=%q",
					e.SessionID(), e.ParentToolUseID())
			}
		}
	}
	if !sawStart {
		t.Error("missing ToolUseStartEvent")
	}
	if !sawResult {
		t.Error("missing ToolUseResultEvent")
	}
}

// TestEventMeta_ToolEnvCarriesUseID — verifies runner injects tool_use
// id into ToolEnv before calling Tool.Call. AgentTool / AgentBackground
// rely on this to forward ParentToolUseID to AgentSpawnRequest.
func TestEventMeta_ToolEnvCarriesUseID(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running", "tu_test_99", "Probe", `{}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()

	var observedUseID string
	probe := &fakeTool{
		name: "Probe", readOnly: true, concurrencySafe: true,
		respond: func(_ map[string]any) (*ToolResultPayload, error) {
			// fakeTool's respond doesn't get the env, so we read the
			// observation through a custom wrapper below.
			return &ToolResultPayload{
				Content: []state.ContentBlock{{Type: state.ContentText, Text: "ok"}},
			}, nil
		},
	}
	// Wrap so we can capture env.ToolUseID at call time.
	reg.Register(&envCapturingTool{
		fakeTool:  probe,
		captureID: &observedUseID,
	})

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	drainAll(eng.Submit(context.Background(), "probe"))

	if observedUseID != "tu_test_99" {
		t.Errorf("env.ToolUseID = %q, want tu_test_99", observedUseID)
	}
}

// envCapturingTool wraps fakeTool so we can observe ToolEnv state
// without exporting it from the test fixture.
type envCapturingTool struct {
	*fakeTool
	captureID *string
}

func (t *envCapturingTool) Call(ctx context.Context, input map[string]any, env *ToolEnv) (*ToolResultPayload, error) {
	if t.captureID != nil && env != nil {
		*t.captureID = env.ToolUseID
	}
	return t.fakeTool.Call(ctx, input, env)
}
