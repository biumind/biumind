package repl

import (
	"strings"
	"testing"
)

func TestSlashRename_emptyShowsNoTitle(t *testing.T) {
	m, note := model{}.handleRename([]string{"/rename"})
	if m.sessionTitle != "" {
		t.Errorf("title should remain empty: %q", m.sessionTitle)
	}
	if !strings.Contains(note, "no title set") {
		t.Errorf("note: %s", note)
	}
}

func TestSlashRename_emptyShowsCurrent(t *testing.T) {
	m, note := model{sessionTitle: "auth refactor"}.handleRename([]string{"/rename"})
	if m.sessionTitle != "auth refactor" {
		t.Errorf("bare /rename should not mutate")
	}
	if !strings.Contains(note, "auth refactor") {
		t.Errorf("note should echo current: %s", note)
	}
}

func TestSlashRename_setsTitle(t *testing.T) {
	m, note := model{}.handleRename([]string{"/rename", "auth", "refactor"})
	if m.sessionTitle != "auth refactor" {
		t.Errorf("title = %q, want 'auth refactor'", m.sessionTitle)
	}
	if !strings.Contains(note, "auth refactor") {
		t.Errorf("note: %s", note)
	}
}

func TestSlashRename_clearsTitle(t *testing.T) {
	m, note := model{sessionTitle: "old"}.handleRename([]string{"/rename", "clear"})
	if m.sessionTitle != "" {
		t.Errorf("title should be cleared: %q", m.sessionTitle)
	}
	if !strings.Contains(note, "cleared") {
		t.Errorf("note: %s", note)
	}
}

func TestSlashRename_clearsCaseInsensitive(t *testing.T) {
	m, _ := model{sessionTitle: "old"}.handleRename([]string{"/rename", "CLEAR"})
	if m.sessionTitle != "" {
		t.Errorf("CLEAR should also clear: %q", m.sessionTitle)
	}
}

func TestSlashRename_capsLongTitle(t *testing.T) {
	long := strings.Repeat("a", 500)
	m, _ := model{}.handleRename([]string{"/rename", long})
	if len(m.sessionTitle) != 200 {
		t.Errorf("title length = %d, want 200", len(m.sessionTitle))
	}
}

func TestSlashRename_emptyAfterTrim(t *testing.T) {
	// "/rename  " (trailing spaces only) — parts[1:] would be empty
	// strings; joined → "" → "empty title" hint.
	m, note := model{}.handleRename([]string{"/rename", "   "})
	if m.sessionTitle != "" {
		t.Errorf("whitespace-only title should not set: %q", m.sessionTitle)
	}
	if !strings.Contains(note, "empty title") {
		t.Errorf("note: %s", note)
	}
}

func TestSlashRename_nilLogIsSafe(t *testing.T) {
	// When sessionLog is nil (e.g. no-log mode), /rename still works
	// in-memory.
	m := model{}
	if m.sessionLog != nil {
		t.Skip("expected nil sessionLog for this test")
	}
	out, note := m.handleRename([]string{"/rename", "x"})
	if out.sessionTitle != "x" {
		t.Errorf("title not set: %q", out.sessionTitle)
	}
	if !strings.Contains(note, "x") {
		t.Errorf("note: %s", note)
	}
}
