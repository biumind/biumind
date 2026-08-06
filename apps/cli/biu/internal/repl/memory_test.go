// Tests for /memory list + /memory reload helpers.
//
// reloadMemory needs an engine; we stub a minimal one via the public
// engine.QueryEngine API + a no-op provider. The point of these
// tests is the helper logic (file discovery, system-prompt assembly,
// status formatting), not the streaming pipeline.

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
)

// memoryStatusNote with no BIUMIND.md files and no auto-memory dir
// should still announce that the auto-memory primer is active so
// the user knows the model can create one.
func TestMemoryStatusEmptyState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	got := m.memoryStatusNote()
	if !strings.Contains(got, "BIUMIND.md: none loaded") {
		t.Errorf("expected BIUMIND.md empty-state line; got %q", got)
	}
	if !strings.Contains(got, "primer active") {
		t.Errorf("auto-memory primer should be advertised; got %q", got)
	}
}

func TestMemoryStatusListsLoadedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{
		memoryFiles: []memory.File{
			{Path: "/tmp/proj/BIUMIND.md", Source: memory.SrcProject, Content: "P body"},
			{Path: "/tmp/.biumind/BIUMIND.md", Source: memory.SrcUser, Content: "U body x"},
		},
	}
	got := m.memoryStatusNote()
	if !strings.Contains(got, "[project] /tmp/proj/BIUMIND.md") {
		t.Errorf("project file row missing: %q", got)
	}
	if !strings.Contains(got, "[user] /tmp/.biumind/BIUMIND.md") {
		t.Errorf("user file row missing: %q", got)
	}
	// Char counts must reflect actual content lengths.
	if !strings.Contains(got, "(6 chars)") || !strings.Contains(got, "(8 chars)") {
		t.Errorf("char counts wrong: %q", got)
	}
}

func TestMemoryStatusSurfacesAutoMemoryIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biumind", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "- [Lang preference](language.md) — user wants Chinese replies\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := model{}
	got := m.memoryStatusNote()
	if !strings.Contains(got, filepath.Join(dir, "MEMORY.md")) {
		t.Errorf("MEMORY.md path should appear: %q", got)
	}
	if strings.Contains(got, "primer active") {
		t.Errorf("once index exists, status should not say primer-active fallback: %q", got)
	}
}

// reloadMemory without an engine returns the no-engine guard string.
func TestReloadMemoryWithoutEngine(t *testing.T) {
	m := &model{}
	got := m.reloadMemory()
	if !strings.Contains(got, "requires engine") {
		t.Errorf("guard message wrong: %q", got)
	}
}
