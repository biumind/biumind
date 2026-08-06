package cost

import "testing"

func TestContextWindowForModelDefaults(t *testing.T) {
	cases := map[string]int{
		"":                  DefaultContextWindow,
		"claude-sonnet-4-6": DefaultContextWindow,
		"claude-haiku-4-5":  DefaultContextWindow,
		"claude-opus-4-7":   DefaultContextWindow,
		"unknown-model":     DefaultContextWindow,
	}
	for in, want := range cases {
		if got := ContextWindowForModel(in); got != want {
			t.Errorf("ContextWindowForModel(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestContextWindowForModel1mOptIn(t *testing.T) {
	cases := []string{
		"claude-sonnet-4-6[1m]",
		"claude-opus-4-7[1m]",
		"CLAUDE-SONNET-4-6[1M]", // case-insensitive
	}
	for _, in := range cases {
		if got := ContextWindowForModel(in); got != ExtendedContextWindow {
			t.Errorf("ContextWindowForModel(%q) = %d, want %d (1M)", in, got, ExtendedContextWindow)
		}
	}
}

func TestContextUsageTotal(t *testing.T) {
	u := ContextUsage{
		InputTokens:       1000,
		CacheReadTokens:   3000,
		CacheCreateTokens: 500,
	}
	if got, want := u.Total(), 4500; got != want {
		t.Errorf("Total = %d, want %d", got, want)
	}
}

func TestContextPercentZeroUsage(t *testing.T) {
	p := ContextPercent(ContextUsage{}, "claude-sonnet-4-6")
	if p.Used != 0 || p.Remaining != 0 {
		t.Errorf("zero usage should yield zero percentages; got %+v", p)
	}
}

func TestContextPercentTypicalSplit(t *testing.T) {
	// 50K input + 50K cache_read = 100K out of 200K = 50% used.
	u := ContextUsage{
		InputTokens:     50_000,
		CacheReadTokens: 50_000,
	}
	p := ContextPercent(u, "claude-sonnet-4-6")
	if p.Used != 50 {
		t.Errorf("used: got %d, want 50", p.Used)
	}
	if p.Remaining != 50 {
		t.Errorf("remaining: got %d, want 50", p.Remaining)
	}
}

func TestContextPercentClampedAt100(t *testing.T) {
	// Over-budget → clamp to 100, never overshoot.
	u := ContextUsage{InputTokens: 250_000}
	p := ContextPercent(u, "claude-sonnet-4-6")
	if p.Used != 100 || p.Remaining != 0 {
		t.Errorf("clamping failed: got %+v", p)
	}
}

func TestContextPercent1mWindow(t *testing.T) {
	// 100K out of a 1M window = 10%.
	u := ContextUsage{InputTokens: 100_000}
	p := ContextPercent(u, "claude-sonnet-4-6[1m]")
	if p.Used != 10 || p.Remaining != 90 {
		t.Errorf("1M window: got %+v, want Used=10 Remaining=90", p)
	}
}
