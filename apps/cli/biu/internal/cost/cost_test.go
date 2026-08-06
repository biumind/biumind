package cost

import (
	"strings"
	"testing"
)

func TestCostForKnownModels(t *testing.T) {
	cases := map[string]ModelCost{
		"claude-sonnet-4-6":   TierSonnet,
		"claude-haiku-4-5-20251001": TierHaiku45,
		"claude-opus-4-1":     TierOpusOld,
		"claude-opus-4-6":     TierOpus,
		"unknown-model-x":     DefaultCost,
	}
	for m, want := range cases {
		if got := CostFor(m); got != want {
			t.Errorf("CostFor(%q)=%+v, want %+v", m, got, want)
		}
	}
}

func TestUSD(t *testing.T) {
	// 1 Mtok input on Sonnet → $3
	got := USD(TierSonnet, 1_000_000, 0, 0, 0)
	if got != 3 {
		t.Errorf("input cost = %f, want 3", got)
	}
	// 100k output on Haiku 4.5 → 100k/1M * 5 = 0.5
	got = USD(TierHaiku45, 0, 100_000, 0, 0)
	if got != 0.5 {
		t.Errorf("haiku output cost = %f, want 0.5", got)
	}
}

func TestTrackerAccumulates(t *testing.T) {
	tr := NewTracker("claude-sonnet-4-6")
	tr.Add(1_000_000, 0, 0, 0) // $3
	tr.Add(0, 100_000, 0, 0)   // 100k * $15/M = $1.5
	snap := tr.Snapshot()
	if snap.InputTokens != 1_000_000 || snap.OutputTokens != 100_000 {
		t.Errorf("token count wrong: %+v", snap)
	}
	if want := 4.5; absf(snap.USD-want) > 1e-9 {
		t.Errorf("USD=%f, want %f", snap.USD, want)
	}
	if !strings.Contains(snap.String(), "$4.5000") {
		t.Errorf("string format: %q", snap.String())
	}
}

func TestTrackerModelSwitchPreservesUSD(t *testing.T) {
	tr := NewTracker("claude-sonnet-4-6")
	tr.Add(1_000_000, 0, 0, 0)
	tr.SetModel("claude-haiku-4-5")
	tr.Add(1_000_000, 0, 0, 0) // adds $1 at haiku tier
	snap := tr.Snapshot()
	if want := 4.0; absf(snap.USD-want) > 1e-9 {
		t.Errorf("USD across model switch = %f, want %f", snap.USD, want)
	}
	if snap.Model != "claude-haiku-4-5" {
		t.Errorf("model not switched: %s", snap.Model)
	}
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
