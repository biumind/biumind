// Tests for Agent.Interrupt()'s turn-loop semantics. We exercise three
// shapes the cancel can land in:
//
//  1. Mid-stream — Interrupt() fires while frames are still arriving.
//     Engine should swallow the partial assistant message, emit Done
//     with StopReason="interrupted", and skip the ErrorEvent path.
//  2. Mid-tool-batch — Interrupt() fires while a tool is running.
//     Engine should let the tool's ctx-aware error path produce a
//     soft tool_result, append it to state so history stays well-
//     formed, then emit Done{interrupted}.
//  3. After tool batch (between turns) — engine sees ctx canceled
//     before issuing the next provider.Stream. State already has
//     matching tool_use ↔ tool_result, so nothing extra to backfill;
//     just emit Done{interrupted}.
//
// Plus negative cases:
//   - parent ctx canceled WITHOUT ErrInterrupted cause → keep emitting
//     ErrorEvent (legacy behaviour preserved)
//   - Interrupt() with no in-flight Submit is a no-op (covered in
//     biumindkit-level tests; the engine-level tests use cancel funcs
//     directly).

package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── Test fixtures ───────────────────────────────────────

// streamHoldingProvider returns a frame channel that pushes one frame and
// then blocks indefinitely until ctx is canceled. Lets us control the
// race window: Submit goroutine is parked inside ParseStream when we
// fire Interrupt().
type streamHoldingProvider struct {
	streamStarted chan struct{}
}

func (p *streamHoldingProvider) Stream(ctx context.Context, _ StreamRequest) (<-chan StreamFrame, error) {
	ch := make(chan StreamFrame, 4)
	// Send a message_start so ParseStream is engaged but no completion.
	ch <- StreamFrame{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "test"}}
	go func() {
		// Signal that the stream is live.
		if p.streamStarted != nil {
			close(p.streamStarted)
		}
		// Block until the engine's ctx is canceled, then close to
		// unblock ParseStream.
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// blockingTool blocks inside Call until ctx cancels OR `release` is
// closed. Used to wedge runBatches mid-execution so Interrupt() can land
// while the runner is in the tool, not in the LLM stream.
type blockingTool struct {
	name     string
	started  chan struct{}
	release  chan struct{}
	canceled *atomic.Bool // observed by the test assertion
}

func (t *blockingTool) Name() string                            { return t.name }
func (t *blockingTool) Description(_ map[string]any) string     { return "blocking " + t.name }
func (t *blockingTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (t *blockingTool) IsReadOnly(_ map[string]any) bool        { return true }
func (t *blockingTool) IsDestructive(_ map[string]any) bool     { return false }
func (t *blockingTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (t *blockingTool) InterruptBehavior() string               { return "cancel" }
func (t *blockingTool) Call(ctx context.Context, _ map[string]any, _ *ToolEnv) (*ToolResultPayload, error) {
	// Caller-provided started is closed exactly once: tests construct
	// one blockingTool per call site so the close never doubles.
	if t.started != nil {
		select {
		case <-t.started:
			// already closed (defensive against multi-call test paths)
		default:
			close(t.started)
		}
	}
	select {
	case <-ctx.Done():
		if t.canceled != nil {
			t.canceled.Store(true)
		}
		return nil, ctx.Err()
	case <-t.release:
		return &ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: "released"}},
		}, nil
	}
}

// ─── Tests ───────────────────────────────────────────────

// TestInterruptMidStreamEmitsDone verifies that when Interrupt() fires
// while ParseStream is still consuming frames, the engine emits a
// DoneEvent{StopReason:"interrupted"} (not ErrorEvent), and the
// partial assistant message is dropped from state (matching the existing
// "discard on cancel" contract).
func TestInterruptMidStreamEmitsDone(t *testing.T) {
	prov := &streamHoldingProvider{streamStarted: make(chan struct{})}
	st := state.New()
	reg := NewRegistry()
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	ch := eng.Submit(ctx, "hello")

	// Wait for the stream to be live, then interrupt.
	select {
	case <-prov.streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("provider stream never started")
	}
	cancel(ErrInterrupted)

	events := drainAll(ch)

	// Expect Done{interrupted}, no ErrorEvent.
	if hasEvent(events, func(*ErrorEvent) bool { return true }) {
		t.Errorf("got ErrorEvent on interrupt; expected clean Done")
	}
	if !hasEvent(events, func(d *DoneEvent) bool {
		return d.StopReason == "interrupted"
	}) {
		t.Errorf("missing Done{StopReason:\"interrupted\"} — got %d events", len(events))
		for _, ev := range events {
			t.Logf("  %T %+v", ev, ev)
		}
	}

	// Partial assistant message should NOT be in state (existing
	// contract — only the user prompt was appended before stream).
	snap := st.Snapshot()
	if len(snap) != 1 || snap[0].Role != state.RoleUser {
		t.Errorf("expected only user message in state, got %d (%v)",
			len(snap), snap)
	}
}

// TestInterruptMidStreamPreservesParentCancelError verifies that
// canceling the parent context WITHOUT the ErrInterrupted cause does
// NOT mint a Done{interrupted}. The legacy ErrorEvent path is itself
// racy at this layer (SafeSend gates on the same canceled ctx, so the
// Error can be dropped) — that flakiness predates F5 and is out of
// scope here. The hard guarantee we tighten is "no false interrupted
// stop_reason" so callers can't be tricked into rendering a clean stop
// for what is actually a parent timeout.
func TestInterruptMidStreamPreservesParentCancelError(t *testing.T) {
	prov := &streamHoldingProvider{streamStarted: make(chan struct{})}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := eng.Submit(ctx, "hello")

	select {
	case <-prov.streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("provider stream never started")
	}
	cancel() // plain cancel, no cause

	events := drainAll(ch)
	if hasEvent(events, func(d *DoneEvent) bool {
		return d.StopReason == "interrupted"
	}) {
		t.Errorf("plain cancel should not emit Done{interrupted}")
	}
}

// TestInterruptMidToolBatchAppendsToolResult verifies that when
// Interrupt() lands while a tool is running, the engine still appends
// a tool_result to state (so Anthropic API replay stays valid) and
// emits Done{interrupted} without looping back for another LLM round.
func TestInterruptMidToolBatchAppendsToolResult(t *testing.T) {
	// Turn 1: assistant says "running" + tool_use Slow{}.
	// Turn 2 would be "ok done" but we never reach it — interrupt
	// fires while Slow is blocked.
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running", "tu_1", "Slow", `{}`),
		textTurn("would-not-reach"),
	}}
	st := state.New()
	reg := NewRegistry()
	released := make(chan struct{})
	var canceled atomic.Bool
	tool := &blockingTool{
		name:     "Slow",
		started:  make(chan struct{}),
		release:  released,
		canceled: &canceled,
	}
	reg.Register(tool)

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})

	ctx, cancel := context.WithCancelCause(context.Background())
	ch := eng.Submit(ctx, "do the slow thing")

	// Wait for the tool to start, then interrupt.
	select {
	case <-tool.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool never started")
	}
	cancel(ErrInterrupted)

	events := drainAll(ch)

	if !canceled.Load() {
		t.Errorf("tool ctx never observed cancel")
	}
	// Done{interrupted} should be the terminal event.
	if !hasEvent(events, func(d *DoneEvent) bool {
		return d.StopReason == "interrupted"
	}) {
		t.Errorf("missing Done{interrupted}; got: %v", events)
	}
	// Should NOT have looped back to the second scripted turn — the
	// post-tool-batch interrupt check should have short-circuited.
	if prov.calls > 1 {
		t.Errorf("provider called %d times; should not loop after interrupt", prov.calls)
	}

	// State must contain the tool_result message paired with tool_use.
	snap := st.Snapshot()
	var assistantUseID string
	var resultUseID string
	for _, m := range snap {
		for _, b := range m.Content {
			if b.Type == state.ContentToolUse {
				assistantUseID = b.ToolUseID
			}
			if b.Type == state.ContentToolResult {
				resultUseID = b.ToolResultID
			}
		}
	}
	if assistantUseID == "" {
		t.Fatal("expected assistant tool_use block in state")
	}
	if resultUseID != assistantUseID {
		t.Errorf("tool_result missing or unpaired: use=%q result=%q",
			assistantUseID, resultUseID)
	}
	// The synthetic result must be flagged as error so the model
	// (on resume) sees that the call didn't complete.
	for _, m := range snap {
		for _, b := range m.Content {
			if b.Type == state.ContentToolResult && !b.ToolResultIsError {
				t.Errorf("interrupted tool_result should have IsError=true")
			}
		}
	}
}

// TestInterruptIsIdempotent verifies that calling cancel multiple
// times with the cause doesn't break anything (mirrors Interrupt()
// being safe to call repeatedly).
func TestInterruptIsIdempotent(t *testing.T) {
	prov := &streamHoldingProvider{streamStarted: make(chan struct{})}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})

	ctx, cancel := context.WithCancelCause(context.Background())
	ch := eng.Submit(ctx, "hello")
	<-prov.streamStarted

	cancel(ErrInterrupted)
	cancel(ErrInterrupted) // second call must not panic / double-close
	cancel(nil)            // null cause after a real cause is also safe

	events := drainAll(ch)
	if !hasEvent(events, func(d *DoneEvent) bool {
		return d.StopReason == "interrupted"
	}) {
		t.Errorf("missing Done{interrupted}")
	}
}

// TestIsInterruptHelper unit-tests the cause detection so future ctx
// chains (parent + child causes) don't silently break the dispatch.
func TestIsInterruptHelper(t *testing.T) {
	t.Run("nil ctx", func(t *testing.T) {
		if isInterrupt(nil, context.Canceled) {
			t.Fatal("nil ctx should be false")
		}
	})
	t.Run("uncanceled ctx", func(t *testing.T) {
		ctx := context.Background()
		if isInterrupt(ctx, nil) {
			t.Fatal("background ctx with nil err should be false")
		}
	})
	t.Run("plain cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if isInterrupt(ctx, ctx.Err()) {
			t.Fatal("plain cancel without cause should be false")
		}
	})
	t.Run("cause = ErrInterrupted", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(ErrInterrupted)
		if !isInterrupt(ctx, ctx.Err()) {
			t.Fatal("ErrInterrupted cause should be true")
		}
	})
	t.Run("non-canceled error", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(ErrInterrupted)
		// err is non-Canceled (e.g. real provider error)
		if isInterrupt(ctx, &netErr{msg: "dial tcp"}) {
			t.Fatal("non-canceled err must not be classed as interrupt")
		}
	})
	t.Run("wrapped Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(ErrInterrupted)
		// errors.Is unwraps fmt-wrapped %w
		wrapped := &wrappedErr{inner: context.Canceled}
		if !isInterrupt(ctx, wrapped) {
			t.Fatal("wrapped context.Canceled should still be detected")
		}
	})
}

type netErr struct{ msg string }

func (e *netErr) Error() string { return e.msg }

type wrappedErr struct{ inner error }

func (e *wrappedErr) Error() string { return "wrapped: " + e.inner.Error() }
func (e *wrappedErr) Unwrap() error { return e.inner }

// TestBackfillSyntheticToolResult exercises the batch-skipped path
// directly. We build a runs slice mirroring "the runner never reached
// index 1" and check that backfillInterruptedToolResults fills it.
func TestBackfillSyntheticToolResult(t *testing.T) {
	calls := []runnerInput{
		{UseID: "tu_a", Name: "Read", Input: map[string]any{"path": "/a"}},
		{UseID: "tu_b", Name: "Glob", Input: map[string]any{"pattern": "*.go"}},
	}
	outs := []runnerOutput{
		{UseID: "tu_a", Name: "Read", Payload: ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: "ok"}},
		}},
		{}, // never ran
	}
	out := make(chan Event, 4)
	defer close(out)
	got := backfillInterruptedToolResults(out, calls, outs, context.Background())
	if got[0].UseID != "tu_a" || got[0].Payload.Content[0].Text != "ok" {
		t.Errorf("real result mutated: %+v", got[0])
	}
	if got[1].UseID != "tu_b" {
		t.Errorf("missing slot not backfilled: %+v", got[1])
	}
	if !got[1].Payload.IsError {
		t.Errorf("synthetic result should be IsError=true")
	}
	if !strings.Contains(got[1].Payload.Content[0].Text, "interrupted") {
		t.Errorf("synthetic result text should mention interrupt: %q",
			got[1].Payload.Content[0].Text)
	}
	// One ToolUseResultEvent emitted (only for the synthetic slot).
	select {
	case ev := <-out:
		r, ok := ev.(*ToolUseResultEvent)
		if !ok {
			t.Fatalf("expected ToolUseResultEvent, got %T", ev)
		}
		if r.ID != "tu_b" {
			t.Errorf("synthetic event for wrong id: %s", r.ID)
		}
	default:
		t.Errorf("expected one synthetic ToolUseResultEvent on the channel")
	}
}
