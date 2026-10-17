// Tests for the sub-agent interactive-ask contract.
//
// A sub-engine's PermissionAskEvent carries a Decision channel the
// sub-engine blocks on. runSubmit must either forward the event into
// the parent's live turn stream (so the UI answers it) or answer it
// fail-closed — dropping it deadlocks the sub-engine and, with it,
// the parent's parallel tool batch.
//
// Regression: parallel Agent calls hung the TUI at "thinking…"
// forever whenever a child ran a command the user hadn't approved
// before, because runSubmit's event switch ignored PermissionAskEvent.

package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// routeProvider dispatches Stream calls to a per-conversation scripted
// sequence, keyed by the conversation's first user prompt (which stays
// the last user TEXT block on every subsequent turn — tool_results
// carry no text). Parent and child engines share the provider, so this
// is how the test tells their streams apart.
type routeProvider struct {
	mu     sync.Mutex
	routes map[string]*scriptedProvider
}

func (p *routeProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sp, ok := p.routes[lastUserText(req.Messages)]; ok {
		return sp.Stream(ctx, req)
	}
	return nil, errNoRoute
}

type errNoRouteT struct{}

func (errNoRouteT) Error() string { return "routeProvider: no script for prompt" }

var errNoRoute error = errNoRouteT{}

// TestSpawnerAskForwardedToParentStream runs the full path: parent
// turn → Agent tool → child engine → destructive Bash → ask. The ask
// must surface on the PARENT's event stream (stamped with the child's
// session id and the Agent tool_use id), and answering it there must
// unblock the child.
func TestSpawnerAskForwardedToParentStream(t *testing.T) {
	prov := &routeProvider{routes: map[string]*scriptedProvider{
		"spawn child": {scripts: [][]StreamFrame{
			toolUseTurn("delegating", "tu_agent1", "Agent", `{"prompt":"do risky"}`),
			textTurn("parent done"),
		}},
		"do risky": {scripts: [][]StreamFrame{
			toolUseTurn("running rm", "tu_child1", "Bash", `{"command":"rm -rf /tmp/x"}`),
			textTurn("child done"),
		}},
	}}

	reg := NewRegistry()
	bash := &fakeTool{name: "Bash", destructive: true}
	reg.Register(bash)
	reg.Register(realAgentTool{})

	eng, err := New(Options{
		State: state.New(), Tools: reg, Provider: prov, Model: "test",
		CompactMaxTokens: -1,
		// BypassPermissions=false → destructive Bash triggers ask.
	})
	if err != nil {
		t.Fatal(err)
	}

	ch := eng.Submit(context.Background(), "spawn child")
	doneCh := make(chan []Event)
	go func() {
		var events []Event
		for ev := range ch {
			events = append(events, ev)
			if ask, ok := ev.(*PermissionAskEvent); ok {
				ask.Decision <- PermissionAnswer{Decision: PermAllow}
			}
		}
		doneCh <- events
	}()

	var events []Event
	select {
	case events = <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("turn hung — the forwarded ask never reached the parent stream")
	}

	var ask *PermissionAskEvent
	for _, ev := range events {
		if a, ok := ev.(*PermissionAskEvent); ok {
			ask = a
		}
	}
	if ask == nil {
		t.Fatal("missing PermissionAskEvent on the parent stream")
	}
	if ask.ParentToolUseID() != "tu_agent1" {
		t.Errorf("ask lost its link to the Agent call: ParentToolUseID=%q, want tu_agent1",
			ask.ParentToolUseID())
	}
	if ask.SessionID() == "" {
		t.Error("ask should carry the child engine's session id")
	}
	if bash.calls != 1 {
		t.Errorf("child Bash should have run after approval; calls=%d", bash.calls)
	}
	if !hasEvent(events, func(*DoneEvent) bool { return true }) {
		t.Error("missing Done")
	}
}

// TestSpawnerAskFailClosedWithoutActiveTurn: no parent turn is
// running (e.g. a background teammate asking between turns, or a
// Spawn racing turn teardown). The spawner must answer deny itself —
// before the fix this deadlocked the child engine forever.
func TestSpawnerAskFailClosedWithoutActiveTurn(t *testing.T) {
	prov := &routeProvider{routes: map[string]*scriptedProvider{
		"do risky": {scripts: [][]StreamFrame{
			toolUseTurn("running rm", "tu_child1", "Bash", `{"command":"rm -rf /tmp/x"}`),
			textTurn("child survived denial"),
		}},
	}}

	reg := NewRegistry()
	bash := &fakeTool{name: "Bash", destructive: true}
	reg.Register(bash)

	eng, err := New(Options{
		State: state.New(), Tools: reg, Provider: prov, Model: "test",
		CompactMaxTokens: -1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Note: no eng.Submit — parent has no active turn.
	sp := NewSpawner(eng, SpawnerOptions{})

	type spawnRes struct {
		res *AgentSpawnResult
		err error
	}
	resCh := make(chan spawnRes, 1)
	go func() {
		res, err := sp.Spawn(context.Background(), AgentSpawnRequest{Prompt: "do risky"})
		resCh <- spawnRes{res, err}
	}()

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("spawn failed: %v", r.err)
		}
		if r.res.Output != "child survived denial" {
			t.Errorf("unexpected child output: %q", r.res.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Spawn hung — unanswered ask deadlocked the child engine")
	}

	if bash.calls != 0 {
		t.Errorf("fail-closed deny must not run the tool; calls=%d", bash.calls)
	}
}
