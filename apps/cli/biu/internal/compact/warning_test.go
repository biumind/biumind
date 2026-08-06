package compact

import "testing"

func TestWarningState_belowInfo(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	if _, fire := w.Maybe(40_000); fire {
		t.Error("40% should not fire any warning")
	}
}

func TestWarningState_infoFiresOnce(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	level, fire := w.Maybe(60_000) // 60% > LevelInfo (50%)
	if !fire || level != LevelInfo {
		t.Errorf("first call: level=%v fire=%v, want (LevelInfo, true)", level, fire)
	}
	if _, fire := w.Maybe(65_000); fire {
		t.Error("re-fire at same level should NOT emit again")
	}
}

func TestWarningState_urgentSupersedesInfo(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	// Jump straight to urgent (90%) — info should NOT fire afterwards
	// because crossing urgent means user already sees the situation.
	level, fire := w.Maybe(90_000)
	if !fire || level != LevelUrgent {
		t.Errorf("got (%v,%v), want (LevelUrgent, true)", level, fire)
	}
	if _, fire := w.Maybe(60_000); fire {
		t.Error("info should be auto-acked when urgent already fired")
	}
}

func TestWarningState_resetReArms(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	_, _ = w.Maybe(60_000) // info fires
	w.Reset(0)
	if _, fire := w.Maybe(60_000); !fire {
		t.Error("after Reset, info should fire again")
	}
}

// Watermark: Reset with usedTokens=N means subsequent Maybe(M)
// computes the cycle delta as M-N. So Maybe(N+1000) at MaxTokens
// 100_000 produces ratio 0.01 — well below LevelInfo (0.50) — and
// must not fire even though raw M crossed the threshold long ago.
func TestWarningState_watermarkSubtracts(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	w.Reset(60_000) // simulate post-compact watermark
	if _, fire := w.Maybe(61_000); fire {
		t.Error("delta of 1000 should not fire any warning")
	}
	// New cycle accumulates: at 60_000 + 51_000 = 111_000, delta is
	// 51_000 → above LevelInfo threshold (50_000).
	if _, fire := w.Maybe(111_000); !fire {
		t.Error("delta crossing 50% should fire info")
	}
}

// Watermark drift defence: if cumulative usage somehow goes BELOW
// the watermark (cost tracker reset, weird provider race), Maybe
// must not panic or report a negative ratio. Treat as fresh cycle.
func TestWarningState_watermarkDriftSafe(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 100_000})
	w.Reset(50_000)
	// Cumulative drops below watermark — defensive fallback.
	if _, fire := w.Maybe(10_000); fire {
		t.Errorf("usage below watermark with no threshold cross → should not fire")
	}
	// And a high reading post-drift fires normally.
	if _, fire := w.Maybe(60_000); !fire {
		t.Error("60% should fire even after drift")
	}
}

func TestWarningState_customRatios(t *testing.T) {
	w := NewWarningState(WarningOptions{
		MaxTokens:        100_000,
		LevelInfoRatio:   0.30,
		LevelUrgentRatio: 0.95,
	})
	// 35% would not fire at default ratio (0.50) but does at 0.30.
	level, fire := w.Maybe(35_000)
	if !fire || level != LevelInfo {
		t.Errorf("got (%v,%v), want (LevelInfo, true)", level, fire)
	}
}

func TestWarningState_zeroMaxIsNoOp(t *testing.T) {
	w := NewWarningState(WarningOptions{MaxTokens: 0})
	if _, fire := w.Maybe(99_999); fire {
		t.Error("MaxTokens=0 should never fire")
	}
}

func TestWarningState_nilSafe(t *testing.T) {
	var w *WarningState
	if _, fire := w.Maybe(99_999); fire {
		t.Error("nil receiver should never fire")
	}
	w.Reset(0) // must not panic
	if got := w.MaxTokens(); got != 0 {
		t.Errorf("nil MaxTokens = %d, want 0", got)
	}
}

func TestWarningLevel_String(t *testing.T) {
	if LevelInfo.String() != "info" {
		t.Error("Info string")
	}
	if LevelUrgent.String() != "urgent" {
		t.Error("Urgent string")
	}
	if WarningLevel(99).String() != "unknown" {
		t.Error("Unknown string")
	}
}
