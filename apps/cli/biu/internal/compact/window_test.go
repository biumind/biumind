package compact

import (
	"testing"
)

// ─── ModelContextWindow ──────────────────────────────────────

func TestModelContextWindow(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"claude-opus-4-7", 200_000},
		{"claude-opus-4-6-20251015", 200_000},
		{"claude-sonnet-4-6", 200_000},
		{"claude-sonnet-4-5-20250929", 200_000},
		{"claude-haiku-4-5", 200_000},
		{"claude-sonnet-3-5-20241022", 200_000},
		{"unknown-model", 200_000}, // safe fallback
	}
	for _, tc := range cases {
		if got := ModelContextWindow(tc.model); got != tc.want {
			t.Errorf("ModelContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// ─── EffectiveContextWindow ──────────────────────────────────

func TestEffectiveContextWindow_subtractsOutputReservation(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	got := EffectiveContextWindow("claude-opus-4-7")
	want := 200_000 - MaxOutputTokensForSummary
	if got != want {
		t.Errorf("EffectiveContextWindow = %d, want %d", got, want)
	}
}

func TestEffectiveContextWindow_envOverrideClamps(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "100000")
	got := EffectiveContextWindow("claude-opus-4-7")
	want := 100_000 - MaxOutputTokensForSummary
	if got != want {
		t.Errorf("override-clamped = %d, want %d", got, want)
	}
}

func TestEffectiveContextWindow_envOverrideAboveCapIgnored(t *testing.T) {
	// Override that's higher than the model's nominal — the env
	// var only CLAMPS, never expands. (Otherwise users could
	// trick biu into sending bigger requests than the model
	// accepts, which fails server-side.)
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "999999999")
	got := EffectiveContextWindow("claude-opus-4-7")
	want := 200_000 - MaxOutputTokensForSummary
	if got != want {
		t.Errorf("over-cap override should be ignored: got %d, want %d", got, want)
	}
}

func TestEffectiveContextWindow_envOverrideZeroIgnored(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "0")
	got := EffectiveContextWindow("claude-opus-4-7")
	want := 200_000 - MaxOutputTokensForSummary
	if got != want {
		t.Errorf("zero override should be ignored: got %d", got)
	}
}

// ─── AutoCompactThreshold ────────────────────────────────────

func TestAutoCompactThreshold_defaultBufferBased(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "")

	got := AutoCompactThreshold("claude-opus-4-7")
	want := EffectiveContextWindow("claude-opus-4-7") - AutoCompactBufferTokens
	if got != want {
		t.Errorf("AutoCompactThreshold = %d, want %d", got, want)
	}
}

func TestAutoCompactThreshold_pctOverrideTightens(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "50") // 50% of effective

	eff := EffectiveContextWindow("claude-opus-4-7")
	got := AutoCompactThreshold("claude-opus-4-7")
	want := eff / 2
	if got != want {
		t.Errorf("50%% override = %d, want %d", got, want)
	}
}

func TestAutoCompactThreshold_pctOverrideAboveBufferIgnored(t *testing.T) {
	// 99% would put threshold ABOVE the buffer-based default.
	// Override should clamp to the buffer-based default —
	// percentage override may only TIGHTEN, never relax.
	t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "99")

	got := AutoCompactThreshold("claude-opus-4-7")
	want := EffectiveContextWindow("claude-opus-4-7") - AutoCompactBufferTokens
	if got != want {
		t.Errorf("99%% override should clamp to buffer-based: got %d, want %d", got, want)
	}
}

func TestAutoCompactThreshold_invalidPctIgnored(t *testing.T) {
	for _, v := range []string{"0", "-5", "200", "abc", ""} {
		t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", v)
		got := AutoCompactThreshold("claude-opus-4-7")
		want := EffectiveContextWindow("claude-opus-4-7") - AutoCompactBufferTokens
		if got != want {
			t.Errorf("invalid override %q changed threshold: got %d, want %d", v, got, want)
		}
	}
}

// ─── IsAutoCompactEnabled ────────────────────────────────────

func TestIsAutoCompactEnabled(t *testing.T) {
	t.Setenv("DISABLE_COMPACT", "")
	if !IsAutoCompactEnabled() {
		t.Error("default should be enabled")
	}
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv("DISABLE_COMPACT", v)
		if IsAutoCompactEnabled() {
			t.Errorf("DISABLE_COMPACT=%q should disable", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", ""} {
		t.Setenv("DISABLE_COMPACT", v)
		if !IsAutoCompactEnabled() {
			t.Errorf("DISABLE_COMPACT=%q should leave enabled", v)
		}
	}
}

// ─── CalculateWarning ────────────────────────────────────────

func TestCalculateWarning_freshSession(t *testing.T) {
	t.Setenv("DISABLE_COMPACT", "")
	t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "")
	t.Setenv("CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE", "")

	w := CalculateWarning(1_000, "claude-opus-4-7")
	if !nearly(w.PercentLeft, 99) {
		t.Errorf("PercentLeft = %d, want ~99", w.PercentLeft)
	}
	if w.AboveWarningThreshold || w.AboveErrorThreshold ||
		w.AboveAutoCompactThreshold || w.AtBlockingLimit {
		t.Errorf("fresh session should clear all flags, got %+v", w)
	}
}

func TestCalculateWarning_atAutoCompactTrigger(t *testing.T) {
	t.Setenv("DISABLE_COMPACT", "")
	t.Setenv("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "")

	usage := AutoCompactThreshold("claude-opus-4-7")
	w := CalculateWarning(usage, "claude-opus-4-7")
	if !w.AboveAutoCompactThreshold {
		t.Error("should flag autocompact at threshold")
	}
	if !w.AboveWarningThreshold || !w.AboveErrorThreshold {
		t.Errorf("auto-compact threshold implies warning + error: %+v", w)
	}
}

func TestCalculateWarning_blockingLimit(t *testing.T) {
	t.Setenv("DISABLE_COMPACT", "")

	eff := EffectiveContextWindow("claude-opus-4-7")
	w := CalculateWarning(eff-1, "claude-opus-4-7") // 1 token under effective window
	if !w.AtBlockingLimit {
		t.Errorf("at effective-window-1, should be at blocking limit: %+v", w)
	}
}

func TestCalculateWarning_blockingOverride(t *testing.T) {
	t.Setenv("CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE", "50000")
	w := CalculateWarning(60_000, "claude-opus-4-7")
	if !w.AtBlockingLimit {
		t.Errorf("60K > 50K override → blocking, got %+v", w)
	}
	w = CalculateWarning(40_000, "claude-opus-4-7")
	if w.AtBlockingLimit {
		t.Errorf("40K < 50K override → not blocking, got %+v", w)
	}
}

func TestCalculateWarning_disabledCompactStillCalibrates(t *testing.T) {
	t.Setenv("DISABLE_COMPACT", "1")

	eff := EffectiveContextWindow("claude-opus-4-7")
	// At eff/2, no thresholds tripped; AutoCompactThreshold flag is
	// always false when compact is disabled.
	w := CalculateWarning(eff/2, "claude-opus-4-7")
	if w.AboveAutoCompactThreshold {
		t.Error("DISABLE_COMPACT should never set AboveAutoCompactThreshold")
	}
}

func nearly(got, want int) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= 1
}
