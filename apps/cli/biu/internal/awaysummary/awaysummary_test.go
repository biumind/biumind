package awaysummary

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── Tracker ─────────────────────────────────────────────────

func TestTracker_initialNotArmed(t *testing.T) {
	tr := NewTracker(time.Hour)
	// Just MarkActive'd in NewTracker; ShouldFire shouldn't fire.
	if tr.ShouldFire() {
		t.Error("freshly-active tracker should not fire")
	}
}

func TestTracker_firesAfterThreshold(t *testing.T) {
	tr := NewTracker(time.Minute)
	clock := time.Now()
	tr.SetClock(func() time.Time { return clock })
	tr.MarkActive()

	// 30s elapsed — not yet.
	clock = clock.Add(30 * time.Second)
	if tr.ShouldFire() {
		t.Error("30s < 60s threshold; should not fire")
	}

	// 90s — fire.
	clock = clock.Add(60 * time.Second)
	if !tr.ShouldFire() {
		t.Error("90s > 60s threshold; should fire")
	}
}

func TestTracker_firesOnceUntilReactivated(t *testing.T) {
	tr := NewTracker(time.Minute)
	clock := time.Now()
	tr.SetClock(func() time.Time { return clock })
	tr.MarkActive()
	clock = clock.Add(2 * time.Minute)

	if !tr.ShouldFire() {
		t.Fatal("first call should fire")
	}
	// Same idle period; second call must not fire.
	if tr.ShouldFire() {
		t.Error("idle period should fire only once")
	}

	// User comes back, then idles again.
	tr.MarkActive()
	clock = clock.Add(2 * time.Minute)
	if !tr.ShouldFire() {
		t.Error("re-armed tracker should fire on next idle period")
	}
}

func TestTracker_idleReturnsElapsed(t *testing.T) {
	tr := NewTracker(time.Hour)
	clock := time.Now()
	tr.SetClock(func() time.Time { return clock })
	tr.MarkActive()
	clock = clock.Add(7 * time.Minute)

	got := tr.Idle()
	if got < 6*time.Minute || got > 8*time.Minute {
		t.Errorf("Idle = %s, want ≈7m", got)
	}
}

func TestTracker_nilSafe(t *testing.T) {
	var tr *Tracker
	tr.MarkActive() // must not panic
	if tr.ShouldFire() {
		t.Error("nil tracker should never fire")
	}
	if got := tr.Idle(); got != 0 {
		t.Errorf("nil tracker Idle = %s, want 0", got)
	}
}

func TestTracker_zeroThresholdUsesDefault(t *testing.T) {
	tr := NewTracker(0)
	if tr.threshold != DefaultIdleThreshold {
		t.Errorf("threshold = %s, want %s", tr.threshold, DefaultIdleThreshold)
	}
}

// ─── Generate ────────────────────────────────────────────────

type fakeSummer struct {
	calls    int
	lastInst string
	lastMsgs []state.Message
	resp     string
	err      error
}

func (f *fakeSummer) Summarise(ctx context.Context, msgs []state.Message, inst string) (string, error) {
	f.calls++
	f.lastInst = inst
	f.lastMsgs = msgs
	return f.resp, f.err
}

func TestGenerate_emptyHistoryNoOp(t *testing.T) {
	got, err := Generate(context.Background(), &fakeSummer{}, nil, "")
	if err != nil || got != "" {
		t.Errorf("empty history → silent no-op, got %q / %v", got, err)
	}
}

func TestGenerate_nilSummariserNoOp(t *testing.T) {
	hist := []state.Message{{Role: state.RoleUser}}
	got, err := Generate(context.Background(), nil, hist, "")
	if err != nil || got != "" {
		t.Errorf("nil summariser → silent no-op, got %q / %v", got, err)
	}
}

func TestGenerate_truncatesToWindow(t *testing.T) {
	s := &fakeSummer{resp: "you were debugging X"}
	hist := make([]state.Message, RecentMessageWindow+10)
	for i := range hist {
		hist[i] = state.Message{Role: state.RoleUser}
	}
	_, _ = Generate(context.Background(), s, hist, "")
	if len(s.lastMsgs) != RecentMessageWindow {
		t.Errorf("truncated to %d, want %d", len(s.lastMsgs), RecentMessageWindow)
	}
}

func TestGenerate_includesSessionMemory(t *testing.T) {
	s := &fakeSummer{resp: "ok"}
	_, _ = Generate(context.Background(), s,
		[]state.Message{{Role: state.RoleUser}}, "broader context body")
	if !strings.Contains(s.lastInst, "broader context body") {
		t.Errorf("memory not embedded in prompt: %s", s.lastInst)
	}
}

func TestGenerate_omitsMemoryWhenEmpty(t *testing.T) {
	s := &fakeSummer{resp: "ok"}
	_, _ = Generate(context.Background(), s,
		[]state.Message{{Role: state.RoleUser}}, "")
	if strings.Contains(s.lastInst, "Session memory") {
		t.Errorf("empty memory should not produce 'Session memory' header: %s", s.lastInst)
	}
}

func TestGenerate_cancellationIsSilent(t *testing.T) {
	s := &fakeSummer{err: context.Canceled}
	got, err := Generate(context.Background(), s,
		[]state.Message{{Role: state.RoleUser}}, "")
	if err != nil {
		t.Errorf("cancellation should be swallowed, got %v", err)
	}
	if got != "" {
		t.Errorf("cancelled call should return empty, got %q", got)
	}
}

func TestGenerate_otherErrorPropagates(t *testing.T) {
	s := &fakeSummer{err: errors.New("api 500")}
	_, err := Generate(context.Background(), s,
		[]state.Message{{Role: state.RoleUser}}, "")
	if err == nil {
		t.Error("non-cancellation error should propagate")
	}
}

func TestGenerate_trimsResponse(t *testing.T) {
	s := &fakeSummer{resp: "  recap text  \n\n"}
	got, _ := Generate(context.Background(), s,
		[]state.Message{{Role: state.RoleUser}}, "")
	if got != "recap text" {
		t.Errorf("response not trimmed: %q", got)
	}
}
