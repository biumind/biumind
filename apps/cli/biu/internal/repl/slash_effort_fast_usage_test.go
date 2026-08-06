package repl

import (
	"strings"
	"testing"
)

// ─── /effort ──────────────────────────────────────────────────

func TestSlashEffort_bareShowsCurrent(t *testing.T) {
	m := model{modelID: "claude-sonnet-4-6"}
	got, note := m.handleEffort([]string{"/effort"})
	if got.modelID != "claude-sonnet-4-6" {
		t.Errorf("bare /effort should not change model, got %s", got.modelID)
	}
	if !strings.Contains(note, "claude-sonnet-4-6") {
		t.Errorf("note should show current model: %s", note)
	}
	if !strings.Contains(note, "high") || !strings.Contains(note, "medium") || !strings.Contains(note, "low") {
		t.Errorf("note should list tiers: %s", note)
	}
}

func TestSlashEffort_tierSwitch(t *testing.T) {
	cases := map[string]string{
		"high":   "claude-opus-4-7",
		"medium": "claude-sonnet-4-6",
		"low":    "claude-haiku-4-5",
	}
	for tier, wantModel := range cases {
		t.Run(tier, func(t *testing.T) {
			m := model{modelID: "starter"}
			got, note := m.handleEffort([]string{"/effort", tier})
			if got.modelID != wantModel {
				t.Errorf("tier %s → %s, want %s", tier, got.modelID, wantModel)
			}
			if !strings.Contains(note, wantModel) {
				t.Errorf("note missing model id: %s", note)
			}
		})
	}
}

func TestSlashEffort_explicitModelID(t *testing.T) {
	m := model{modelID: "starter"}
	got, note := m.handleEffort([]string{"/effort", "claude-haiku-4-5-20251001"})
	if got.modelID != "claude-haiku-4-5-20251001" {
		t.Errorf("explicit id should pass through, got %s", got.modelID)
	}
	if !strings.Contains(note, "custom id") {
		t.Errorf("note should mark custom id: %s", note)
	}
}

func TestSlashEffort_caseInsensitive(t *testing.T) {
	m := model{modelID: "x"}
	got, _ := m.handleEffort([]string{"/effort", "HIGH"})
	if got.modelID != "claude-opus-4-7" {
		t.Errorf("case-insensitive match expected, got %s", got.modelID)
	}
}

// ─── /fast ────────────────────────────────────────────────────

func TestSlashFast_switchesToHaiku(t *testing.T) {
	m := model{modelID: "claude-opus-4-7"}
	got, note := m.handleFast([]string{"/fast"})
	if got.modelID != "claude-haiku-4-5" {
		t.Errorf("/fast → %s, want claude-haiku-4-5", got.modelID)
	}
	if !strings.Contains(note, "Haiku") {
		t.Errorf("note should mention Haiku: %s", note)
	}
}

// ─── /usage ───────────────────────────────────────────────────

func TestSlashUsage_emptyLedger(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := model{}.handleUsage([]string{"/usage"})
	if !strings.Contains(got, "no records") {
		t.Errorf("empty ledger should report no records: %s", got)
	}
}

func TestSlashUsage_scopeArgRecognised(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, scope := range []string{"today", "week", "month", "all"} {
		got := model{}.handleUsage([]string{"/usage", scope})
		// All scopes should hit the no-records branch (empty
		// ledger). The point is they parse without crashing.
		if got == "" {
			t.Errorf("scope %s produced empty output", scope)
		}
	}
}

func TestHumanInt(t *testing.T) {
	cases := map[int]string{
		0:           "0",
		999:         "999",
		1000:        "1,000",
		12345:       "12,345",
		1000000:     "1,000,000",
		123456789:   "123,456,789",
	}
	for in, want := range cases {
		if got := humanInt(in); got != want {
			t.Errorf("humanInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 30); got != "short" {
		t.Errorf("non-truncated changed: %q", got)
	}
	if got := truncate("aaaaaaaaaa", 5); got != "aaaa…" {
		t.Errorf("truncate(10, 5) = %q", got)
	}
}
