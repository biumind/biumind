// Tests for /cost output. We focus on the formatting helpers +
// the no-engine fallback; the full table renders depend on a real
// engine + cost tracker which the existing engine tests cover.

package repl

import (
	"strings"
	"testing"
)

func TestFormatThousandsShape(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		1:       "1",
		999:     "999",
		1000:    "1,000",
		12345:   "12,345",
		1234567: "1,234,567",
		-1234:   "-1,234",
	}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatPercentShape(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "0%"},
		{0.5, "50%"},
		{1.0, "100%"},
		{0.123, "12%"},
		{-0.1, "n/a"},
		{1.5, "n/a"},
	}
	for _, c := range cases {
		if got := formatPercent(c.in); got != c.want {
			t.Errorf("formatPercent(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// /cost without an engine surfaces a useful "configure direct/cloud"
// hint rather than just "TBD".
func TestCostNoteWithoutEngineExplains(t *testing.T) {
	m := model{}
	got := m.costNote()
	for _, must := range []string{"turns=", "uptime=", "TBD", "direct/cloud"} {
		if !strings.Contains(got, must) {
			t.Errorf("no-engine path missing %q; got %q", must, got)
		}
	}
}

// /cost with a real engine but zero usage emits the headers + zero
// rows. We use newTestModel from statusline_test.go for fixture.
func TestCostNoteWithEngineEmitsHeaders(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	got := m.costNote()
	for _, must := range []string{
		"session:", "model=claude-sonnet-4-6", "turns=",
		"tokens (cumulative", "input", "cache read", "cache write", "output",
		"total input",
		"cost: $",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("output missing %q;\nfull:\n%s", must, got)
		}
	}
}

// Once last-turn usage is set, the context block appears. The
// status bar uses the same calculation; here we lock the row text.
func TestCostNoteShowsContextWhenUsageRecorded(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	m.lastUsageInput = 50_000
	m.lastUsageCacheRead = 50_000
	got := m.costNote()
	for _, must := range []string{
		"context (last turn):",
		"100,000 / 200,000",
		"50% used",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("context block missing %q;\nfull:\n%s", must, got)
		}
	}
}

// 1M-context model halves the percentage for the same usage.
func TestCostNoteHonours1mContextWindow(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6[1m]")
	m.lastUsageInput = 100_000 // 10% of 1M
	got := m.costNote()
	if !strings.Contains(got, "10% used") {
		t.Errorf("1M window should report 10%%; got %q", got)
	}
	if !strings.Contains(got, "1,000,000") {
		t.Errorf("1M window cap should appear; got %q", got)
	}
}

// No context block when no usage event has fired (cold start). Same
// posture as the status-line "hide ctx until first usage" rule.
func TestCostNoteHidesContextOnColdStart(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	got := m.costNote()
	if strings.Contains(got, "context (last turn)") {
		t.Errorf("context block should be hidden until first usage event; got %q", got)
	}
}
