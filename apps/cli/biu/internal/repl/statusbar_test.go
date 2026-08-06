package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

func TestModeBadgeDefaultEmpty(t *testing.T) {
	if got := modeBadge(permissions.ModeDefault); got != "" {
		t.Errorf("default mode should not render a badge; got %q", got)
	}
	if got := modeBadge(permissions.Mode("")); got != "" {
		t.Errorf("empty mode should not render a badge")
	}
}

func TestModeBadgeContainsLabel(t *testing.T) {
	cases := []struct {
		mode  permissions.Mode
		label string
	}{
		{permissions.ModePlan, "Plan"},
		{permissions.ModeAcceptEdits, "Accept"},
		{permissions.ModeBypass, "Bypass"},
		{permissions.ModeDontAsk, "DontAsk"},
	}
	for _, c := range cases {
		got := modeBadge(c.mode)
		if !strings.Contains(got, c.label) {
			t.Errorf("badge for %v missing label %q: %q", c.mode, c.label, got)
		}
	}
}

func TestModeBadgeUsesSymbol(t *testing.T) {
	got := modeBadge(permissions.ModePlan)
	if !strings.Contains(got, "❙❙") {
		t.Errorf("plan badge missing pause symbol: %q", got)
	}
	got = modeBadge(permissions.ModeBypass)
	if !strings.Contains(got, "⏵⏵") {
		t.Errorf("bypass badge missing fast-forward symbol: %q", got)
	}
}
