package repl

import (
	"strings"
	"testing"
)

// /doctor runs even when the engine isn't wired (chat-only / SDK
// modes). It surfaces what it can — runtime, home dir — and skips
// engine-dependent checks gracefully.
func TestSlashDoctor_runsWithoutEngine(t *testing.T) {
	got := model{}.handleDoctor([]string{"/doctor"})
	if !strings.Contains(got, "biu doctor") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "go runtime") {
		t.Errorf("runtime check should always run: %q", got)
	}
	// Engine path / mcp checks must not appear when engine is nil.
	if strings.Contains(got, "engine path") {
		t.Errorf("engine check should skip without wired engine: %q", got)
	}
}

func TestSlashDoctor_overallVerdictWhenAllHealthy(t *testing.T) {
	got := model{}.handleDoctor([]string{"/doctor"})
	// The base set of checks — go runtime / home / config dir /
	// shell — should all pass in a normal test env. git + ripgrep
	// may warn on bare CI, but the verdict still says "healthy"
	// only when zero failures.
	if !strings.Contains(got, "overall:") {
		t.Errorf("verdict line missing: %q", got)
	}
}

// Empty model is fine — no panic when no engine, no MCP.
func TestSlashDoctor_neverPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("/doctor panicked on empty model: %v", r)
		}
	}()
	_ = model{}.handleDoctor([]string{"/doctor"})
}

// /doctor groups results by status — failures / warnings / OKs are
// in dedicated blocks. Verify the headers appear when applicable.
func TestSlashDoctor_groupsByStatus(t *testing.T) {
	got := model{}.handleDoctor([]string{"/doctor"})
	// At least the OK header should appear (go runtime always
	// passes).
	if !strings.Contains(got, "OK") {
		t.Errorf("OK group header should appear: %q", got)
	}
}
