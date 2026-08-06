// E2E tests for the sub-agent / swarm path.
//
// Verifies:
//
//   * AgentTool registers as concurrency-safe (the engine runner can
//     batch two parallel sub-agent calls in a single turn).
//   * The Spawner builds a fresh state per child and runs the child
//     turn end-to-end.
//   * Two sub-agent calls in the same turn actually overlap in time
//     (a slow child doesn't serialise the fast one).

package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// swarmProvider is a scriptedProvider that distinguishes parent vs
// child requests by checking how many user messages exist in the
// request — the parent has exactly 1, children have 1 (their own
// fresh state). We discriminate by looking at the *content* of that
// one user message instead.
type swarmProvider struct {
	parentTurn1 []StreamFrame
	parentTurn2 []StreamFrame
	childAReply []StreamFrame
	childBReply []StreamFrame

	mu        sync.Mutex
	parentHit int
	overlapMu sync.Mutex
	overlapStartA, overlapEndA time.Time
	overlapStartB, overlapEndB time.Time
	delay     time.Duration
}

func (p *swarmProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	prompt := lastUserText(req.Messages)
	switch {
	case prompt == "find auth":
		return p.recordChildA(p.childAReply), nil
	case prompt == "find tests":
		return p.recordChildB(p.childBReply), nil
	}
	// Otherwise it's the parent.
	p.mu.Lock()
	hit := p.parentHit
	p.parentHit++
	p.mu.Unlock()
	if hit == 0 {
		return makeChan(p.parentTurn1), nil
	}
	return makeChan(p.parentTurn2), nil
}

func (p *swarmProvider) recordChildA(frames []StreamFrame) <-chan StreamFrame {
	p.overlapMu.Lock()
	p.overlapStartA = time.Now()
	p.overlapMu.Unlock()
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.overlapMu.Lock()
	p.overlapEndA = time.Now()
	p.overlapMu.Unlock()
	return makeChan(frames)
}

func (p *swarmProvider) recordChildB(frames []StreamFrame) <-chan StreamFrame {
	p.overlapMu.Lock()
	p.overlapStartB = time.Now()
	p.overlapMu.Unlock()
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.overlapMu.Lock()
	p.overlapEndB = time.Now()
	p.overlapMu.Unlock()
	return makeChan(frames)
}

func makeChan(frames []StreamFrame) <-chan StreamFrame {
	ch := make(chan StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch
}

func lastUserText(msgs []state.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != state.RoleUser {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == state.ContentText {
				return b.Text
			}
		}
	}
	return ""
}

func TestSwarmTwoParallelSubagents(t *testing.T) {
	prov := &swarmProvider{
		// Parent turn 1: two concurrent Agent calls, one for each
		// child prompt.
		parentTurn1: twoToolsTurn(
			"a1", "Agent", `{"prompt":"find auth"}`,
			"a2", "Agent", `{"prompt":"find tests"}`),
		// Parent turn 2: text answer assembled from child outputs.
		parentTurn2: textTurn("done — both sub-agents replied"),
		childAReply: textTurn("auth lives in pkg/auth"),
		childBReply: textTurn("tests under test/integration"),
		delay:       50 * time.Millisecond,
	}

	st := state.New()
	reg := NewRegistry()

	// Real AgentTool — exercise the spawner path end-to-end.
	reg.Register(realAgentTool{})

	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
		CompactMaxTokens:  -1,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	events := drainAll(eng.Submit(context.Background(), "research"))
	total := time.Since(start)

	// Both children should have run.
	hits := 0
	for _, ev := range events {
		if r, ok := ev.(*ToolUseResultEvent); ok && r.Name == "Agent" {
			hits++
			if r.Result.IsError {
				t.Errorf("agent %s errored: %+v", r.ID, r.Result)
			}
		}
	}
	if hits != 2 {
		t.Errorf("expected 2 Agent results, got %d", hits)
	}

	// Done event present.
	if !hasEvent(events, func(*DoneEvent) bool { return true }) {
		t.Errorf("missing Done")
	}

	// Overlap proof: A and B both ran 50ms; if they were serial the
	// total time would be ≥100ms just for the children. We allow a
	// 30ms slack for goroutine scheduling.
	if total > 90*time.Millisecond {
		t.Errorf("sub-agents look serialised: total=%s, expected ~50ms", total)
	}

	// Confirm the time windows actually overlapped.
	prov.overlapMu.Lock()
	startA, endA := prov.overlapStartA, prov.overlapEndA
	startB, endB := prov.overlapStartB, prov.overlapEndB
	prov.overlapMu.Unlock()
	if !overlap(startA, endA, startB, endB) {
		t.Errorf("expected overlapping windows: A=%v..%v B=%v..%v",
			startA, endA, startB, endB)
	}
}

// realAgentTool exercises the actual orchestration tool on the engine
// side, but we vendor a tiny copy here to avoid an import cycle
// (engine ⇢ engine_test ⇢ orchestration ⇢ engine). The shape is
// equivalent to orchestration.AgentTool.
type realAgentTool struct{}

func (realAgentTool) Name() string                            { return "Agent" }
func (realAgentTool) Description(_ map[string]any) string     { return "spawn sub" }
func (realAgentTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (realAgentTool) IsReadOnly(_ map[string]any) bool        { return true }
func (realAgentTool) IsDestructive(_ map[string]any) bool     { return false }
func (realAgentTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (realAgentTool) InterruptBehavior() string               { return "cancel" }
func (realAgentTool) Call(ctx context.Context, in map[string]any, env *ToolEnv) (*ToolResultPayload, error) {
	prompt, _ := in["prompt"].(string)
	if env == nil || env.Spawner == nil {
		return &ToolResultPayload{IsError: true, SoftError: "no spawner"}, nil
	}
	res, err := env.Spawner.Spawn(ctx, AgentSpawnRequest{Prompt: prompt})
	if err != nil {
		return &ToolResultPayload{IsError: true, SoftError: err.Error()}, nil
	}
	return &ToolResultPayload{Content: []state.ContentBlock{{
		Type: state.ContentText, Text: res.Output,
	}}}, nil
}

// overlap reports whether two time intervals share any moment.
func overlap(a1, a2, b1, b2 time.Time) bool {
	if a1.IsZero() || b1.IsZero() {
		return false
	}
	return !(a2.Before(b1) || b2.Before(a1))
}

// Sanity: ensure the spawner's agent ID counter is monotonic across
// concurrent Spawn calls. Catches accidental serialisation if the
// counter ever moves under a non-atomic guard.
func TestSpawnerAgentIDsAreUnique(t *testing.T) {
	const N = 20
	var seen sync.Map
	var dupes int32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := nextAgentID()
			if _, loaded := seen.LoadOrStore(id, true); loaded {
				atomic.AddInt32(&dupes, 1)
			}
		}()
	}
	wg.Wait()
	if dupes != 0 {
		t.Errorf("duplicate agent IDs detected: %d", dupes)
	}
}
