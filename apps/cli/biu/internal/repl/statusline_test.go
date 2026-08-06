// Tests for the cost + context-bar pieces of the REPL status line.
//
// We test the pure helpers (contextBar, costAndContextNote) — the
// full statusBar() depends on lipgloss styles + terminal width, so
// it's exercised via end-to-end visual review rather than golden
// strings. The key contracts here are:
//
//   * contextBar always renders WIDTH=10 cells, fillage proportional
//     to percent, never lies (1+ used → 1+ filled cells).
//   * costAndContextNote omits the cost block when USD is below the
//     four-decimal display threshold (avoids "$0.0000").
//   * costAndContextNote omits the ctx block until the first
//     StreamUsageEvent — distinguishes "not measured yet" from "0%".

package repl

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func TestContextBarFillage(t *testing.T) {
	cases := []struct {
		pct  int
		want int // expected filled cells out of 10
	}{
		{-5, 0}, // negative clamped to 0
		{0, 0},
		{1, 1},  // tiny non-zero usage rounded UP to one cell
		{9, 1},  // 9% truncates to 0 segments → bumped to 1
		{10, 1}, // exactly one cell
		{50, 5},
		{99, 9},   // 9.9 truncates to 9
		{100, 10}, // saturated
		{120, 10}, // over-budget clamped to 100
	}
	for _, c := range cases {
		got := contextBar(c.pct)
		if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
			t.Errorf("bar(%d) missing brackets: %q", c.pct, got)
		}
		filled := strings.Count(got, "█")
		empty := strings.Count(got, "░")
		if filled+empty != 10 {
			t.Errorf("bar(%d) total cells = %d, want 10", c.pct, filled+empty)
		}
		if filled != c.want {
			t.Errorf("bar(%d) filled = %d, want %d (rendered %q)",
				c.pct, filled, c.want, got)
		}
	}
}

func TestCostAndContextNoteEmptyOnFreshSession(t *testing.T) {
	// No engine → no note (early return guard).
	m := model{}
	if got := m.costAndContextNote(); got != "" {
		t.Errorf("no engine should yield empty note; got %q", got)
	}
}

// nullProvider satisfies engine.Provider but never streams — we only
// need a constructed *QueryEngine, not a running one.
type nullProvider struct{}

func (nullProvider) Stream(_ context.Context, _ engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	ch := make(chan engine.StreamFrame)
	close(ch)
	return ch, nil
}

func newTestModel(t *testing.T, modelID string) model {
	t.Helper()
	eng, err := engine.New(engine.Options{
		State:    state.New(),
		Tools:    engine.NewRegistry(),
		Provider: nullProvider{},
		Model:    modelID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return model{engine: eng, modelID: modelID}
}

// Without any usage event we should NOT emit a ctx fragment — that
// distinguishes "fresh session, nothing measured" from "0% used".
func TestCostAndContextNoteHidesCtxBeforeFirstUsageEvent(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	if got := m.costAndContextNote(); strings.Contains(got, "ctx") {
		t.Errorf("note should not show ctx until first usage event; got %q", got)
	}
}

// After a usage event (lastUsageInput set), the ctx fragment must
// surface with the percent rounded against the model's 200K window.
func TestCostAndContextNoteShowsCtxAfterUsageEvent(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	// 100K input → 50% of the default 200K window.
	m.lastUsageInput = 100_000
	got := m.costAndContextNote()
	if !strings.Contains(got, "ctx 50%") {
		t.Errorf("expected `ctx 50%%` fragment; got %q", got)
	}
	if !strings.Contains(got, "█") {
		t.Errorf("ctx bar should have at least one filled cell at 50%%; got %q", got)
	}
}

// Cache reads count toward the context budget even though they're
// cheap dollar-wise. Verify the bar agrees.
func TestCostAndContextNoteIncludesCacheReadInBudget(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	m.lastUsageInput = 10_000
	m.lastUsageCacheRead = 90_000
	got := m.costAndContextNote()
	// 100K total / 200K window → 50%.
	if !strings.Contains(got, "ctx 50%") {
		t.Errorf("cache_read should count toward ctx; got %q", got)
	}
}

// The 1M opt-in must change the denominator visibly.
func TestCostAndContextNoteHonours1mWindow(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6[1m]")
	m.lastUsageInput = 100_000 // 10% of 1M
	got := m.costAndContextNote()
	if !strings.Contains(got, "ctx 10%") {
		t.Errorf("1M window should yield 10%% for 100K input; got %q", got)
	}
}
