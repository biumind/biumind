package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

// ─── /summary ────────────────────────────────────────────────

func TestSlashSummary_emptyHistory(t *testing.T) {
	got := model{}.handleSummary([]string{"/summary"})
	if !strings.Contains(got, "no history yet") {
		t.Errorf("empty history should be reported: %s", got)
	}
}

func TestSlashSummary_basicShape(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "user", Content: "first prompt"},
			{Role: "assistant", Content: "an answer"},
			{Role: "user", Content: "follow up"},
			{Role: "assistant", Content: "another answer"},
		},
	}
	got := m.handleSummary([]string{"/summary"})
	for _, want := range []string{
		"this session", "Started with: first prompt",
		"4 turns total (2 user, 2 assistant)",
		"another answer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestOneLineSummary(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"single line", 100, "single line"},
		{"line one\nline two", 100, "line one"},
		{"abcdefghij", 5, "abcd…"},
		{"  whitespace  ", 100, "whitespace"},
	}
	for _, tc := range cases {
		if got := oneLineSummary(tc.in, tc.maxLen); got != tc.want {
			t.Errorf("oneLineSummary(%q,%d) = %q, want %q", tc.in, tc.maxLen, got, tc.want)
		}
	}
}

// ─── /onboarding ─────────────────────────────────────────────

func TestSlashOnboarding_includesEssentials(t *testing.T) {
	got := model{}.handleOnboarding([]string{"/onboarding"})
	for _, want := range []string{
		"welcome to biu", "/help", "/doctor", "/effort",
		"~/.biumind/", "DISABLE_COMPACT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("onboarding missing %q", want)
		}
	}
}

// ─── /theme ──────────────────────────────────────────────────

func TestSlashTheme_bareShowsCurrent(t *testing.T) {
	got, note := model{theme: "dark"}.handleTheme([]string{"/theme"})
	if got.theme != "dark" {
		t.Errorf("bare /theme should not change theme")
	}
	if !strings.Contains(note, "current = dark") {
		t.Errorf("note should show current: %s", note)
	}
}

func TestSlashTheme_emptyShowsSystemDefault(t *testing.T) {
	_, note := model{theme: ""}.handleTheme([]string{"/theme"})
	if !strings.Contains(note, "current = system") {
		t.Errorf("unset theme should report system: %s", note)
	}
}

func TestSlashTheme_switches(t *testing.T) {
	for _, target := range []string{"dark", "light", "system"} {
		got, note := model{}.handleTheme([]string{"/theme", target})
		if got.theme != target {
			t.Errorf("/theme %s → got %q", target, got.theme)
		}
		if !strings.Contains(note, "switched to "+target) {
			t.Errorf("note: %s", note)
		}
	}
}

func TestSlashTheme_unknownRejected(t *testing.T) {
	got, note := model{theme: "dark"}.handleTheme([]string{"/theme", "purple"})
	if got.theme != "dark" {
		t.Errorf("unknown should not mutate: %q", got.theme)
	}
	if !strings.Contains(note, "unknown theme") {
		t.Errorf("note: %s", note)
	}
}

func TestSlashTheme_caseInsensitive(t *testing.T) {
	got, _ := model{}.handleTheme([]string{"/theme", "DARK"})
	if got.theme != "dark" {
		t.Errorf("case-insensitive failed: %q", got.theme)
	}
}

func TestDetectSystemTheme_returnsKnown(t *testing.T) {
	got := detectSystemTheme()
	if got != "dark" && got != "light" {
		t.Errorf("must return dark or light, got %q", got)
	}
}
