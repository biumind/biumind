package sessionmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := PathFor("test-session-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".biumind", "sessionMemory", "test-session-1.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPathFor_rejectsTraversal(t *testing.T) {
	for _, id := range []string{"", "../escape", "a/b", `a\b`, "a.md"} {
		if _, err := PathFor(id); err == nil {
			t.Errorf("PathFor(%q) should error", id)
		}
	}
}

func TestLoad_freshSessionGetsTemplate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, err := Load("new-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(mem.Sections) < 5 {
		t.Errorf("template should have ≥5 sections, got %d", len(mem.Sections))
	}
	if _, ok := mem.FindSection("Current State"); !ok {
		t.Error("default template should have 'Current State' section")
	}
	if _, ok := mem.FindSection("Task specification"); !ok {
		t.Error("default template should have 'Task specification' section")
	}
}

func TestSaveAndReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mem, _ := Load("save-session")
	mem.SetSection("Current State", "Refactoring auth middleware.\nNext: tests.")
	if err := mem.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := Load("save-session")
	if err != nil {
		t.Fatal(err)
	}
	sec, ok := again.FindSection("Current State")
	if !ok {
		t.Fatal("Current State lost on reload")
	}
	if !strings.Contains(sec.Body, "Refactoring auth middleware") {
		t.Errorf("body = %q", sec.Body)
	}
}

func TestSetSection_addsNew(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("x")

	before := len(mem.Sections)
	mem.SetSection("Custom", "body line")
	if len(mem.Sections) != before+1 {
		t.Errorf("section count = %d, want %d", len(mem.Sections), before+1)
	}
	sec, ok := mem.FindSection("Custom")
	if !ok || !strings.Contains(sec.Body, "body line") {
		t.Errorf("Custom section missing or wrong body: %+v", sec)
	}
}

func TestAppendToSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("x")
	mem.SetSection("Errors & Corrections", "first")
	mem.AppendToSection("Errors & Corrections", "second")
	sec, _ := mem.FindSection("Errors & Corrections")
	if !strings.Contains(sec.Body, "first") || !strings.Contains(sec.Body, "second") {
		t.Errorf("append lost content: %s", sec.Body)
	}
}

func TestTruncate_trimsLargeSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("trunc")
	huge := strings.Repeat("line of text\n", 1000) // ~12K chars > MaxSectionTokens*4
	mem.SetSection("Workflow", huge)
	mem.Truncate()
	sec, _ := mem.FindSection("Workflow")
	if !strings.Contains(sec.Body, "earlier content truncated") {
		t.Error("truncated section should carry the marker")
	}
	if len(sec.Body) > MaxSectionTokens*4+200 { // marker adds a few chars
		t.Errorf("section body = %d bytes, beyond cap", len(sec.Body))
	}
}

func TestTruncate_protectsCurrentStateAndTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("guard")
	mem.SetSection("Session Title", "important title")
	mem.SetSection("Current State", "important state")
	// Push total over budget by adding many bulk sections.
	for i := 0; i < 30; i++ {
		mem.SetSection("Bulk-"+string(rune('a'+i)), strings.Repeat("x", 4096))
	}
	mem.Truncate()
	if _, ok := mem.FindSection("Session Title"); !ok {
		t.Error("Session Title must survive truncate")
	}
	if _, ok := mem.FindSection("Current State"); !ok {
		t.Error("Current State must survive truncate")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("rt")
	mem.SetSection("Current State", "exact content here")
	rendered := mem.Render()
	if !strings.Contains(rendered, "# Current State") {
		t.Errorf("rendered missing header: %s", rendered)
	}
	if !strings.Contains(rendered, "exact content here") {
		t.Error("rendered missing body")
	}
}

func TestParseSections_handlesPreambleAndSubheaders(t *testing.T) {
	body := "preamble line\n\n# A\nbody A\n## sub\nstill A\n# B\nbody B\n"
	mem := &SessionMemory{Sections: parseSections(body)}
	if len(mem.Sections) < 3 {
		t.Errorf("want preamble + 2 sections, got %d", len(mem.Sections))
	}
	// Sub-header doesn't open a new top-level section here because
	// our regex matches any `#{1,6}`. Verify we don't lose the body.
	a, ok := mem.FindSection("a")
	if !ok || !strings.Contains(a.Body, "body A") {
		t.Errorf("section A missing body: %+v", a)
	}
}

func TestSave_atomicNoTmpLeftover(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("atomic")
	mem.SetSection("Current State", "x")
	if err := mem.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mem.Path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file lingered: %v", err)
	}
}
