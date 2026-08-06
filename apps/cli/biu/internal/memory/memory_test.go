package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestLoadProjectAndLocal(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd) // isolate from real ~/.biumind
	write(t, cwd, "BIUMIND.md", "Project: use snake_case for API keys.")
	write(t, cwd, "BIUMIND.local.md", "Local: prefer 4-space indent.")

	loaded := Load(cwd)
	if len(loaded.Files) < 2 {
		t.Fatalf("expected ≥2 files, got %d: %+v", len(loaded.Files), loaded.Files)
	}
	got := loaded.SystemPrompt()
	if !strings.Contains(got, "snake_case") {
		t.Errorf("project content missing")
	}
	if !strings.Contains(got, "4-space") {
		t.Errorf("local content missing")
	}
	if !strings.Contains(got, Preamble) {
		t.Errorf("preamble missing")
	}
}

func TestIncludeDirective(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, "rules/lint.md", "RULE-LINT: gofmt before commit.")
	write(t, cwd, "BIUMIND.md", "## Rules\n@./rules/lint.md\n")

	loaded := Load(cwd)
	got := loaded.SystemPrompt()
	if !strings.Contains(got, "RULE-LINT") {
		t.Errorf("@include not expanded: %s", got)
	}
}

func TestCircularIncludeProtected(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, "BIUMIND.md", "@./BIUMIND.md\nA")

	// Should not infinite-loop and should still produce some content.
	loaded := Load(cwd)
	if loaded.SystemPrompt() == "" {
		t.Errorf("expected some output despite cycle")
	}
}

func TestTruncation(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	huge := strings.Repeat("x", MaxFileChars*2)
	write(t, cwd, "BIUMIND.md", huge)
	loaded := Load(cwd)
	if len(loaded.Files) == 0 {
		t.Fatal("expected file loaded")
	}
	if len(loaded.Files[0].Content) > MaxFileChars+50 { // tolerance for trailing marker
		t.Errorf("truncation cap not honored: %d", len(loaded.Files[0].Content))
	}
	if !strings.Contains(loaded.Files[0].Content, "truncated") {
		t.Errorf("truncation marker missing")
	}
}

func TestLoadWithExcludes(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, "BIUMIND.md", "USE THIS")
	write(t, cwd, "vendor/BIUMIND.md", "ignore me")

	got := LoadWithOptions(cwd, Options{Excludes: []string{"vendor"}})
	body := got.SystemPrompt()
	if !strings.Contains(body, "USE THIS") {
		t.Errorf("primary file missing: %s", body)
	}
	if strings.Contains(body, "ignore me") {
		t.Errorf("vendor/BIUMIND.md should be excluded: %s", body)
	}
}

func TestExcludeGlobPattern(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, "BIUMIND.md", "KEEP")
	write(t, cwd, "node_modules/pkg/BIUMIND.md", "DROP")

	got := LoadWithOptions(cwd, Options{Excludes: []string{"node_modules/**"}})
	body := got.SystemPrompt()
	if !strings.Contains(body, "KEEP") {
		t.Errorf("kept file missing")
	}
	if strings.Contains(body, "DROP") {
		t.Errorf("node_modules excluded but body has DROP: %s", body)
	}
}

func TestExcludeBaseName(t *testing.T) {
	if !matchOneExclude("BIUMIND.local.md", "/x/y/BIUMIND.local.md") {
		t.Errorf("basename match should hit")
	}
	if matchOneExclude("BIUMIND.local.md", "/x/BIUMIND.md") {
		t.Errorf("basename match must not over-match")
	}
}

func TestExcludeEmptyPatternsNoOp(t *testing.T) {
	if matchesAnyExclude("/anything", nil) {
		t.Errorf("empty patterns should not match")
	}
	if matchesAnyExclude("/anything", []string{""}) {
		t.Errorf("empty string in patterns should not match")
	}
}

func TestEmptyCwdLoadsUserOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	write(t, tmp, ".biumind/BIUMIND.md", "USER GLOBAL")
	loaded := Load("")
	if len(loaded.Files) != 1 {
		t.Fatalf("expected 1 user file, got %d", len(loaded.Files))
	}
	if loaded.Files[0].Source != SrcUser {
		t.Errorf("source = %v", loaded.Files[0].Source)
	}
}

func TestLoadWithOptions_ExtraDirs(t *testing.T) {
	cwd := t.TempDir()
	extra := t.TempDir()

	if err := os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), []byte("# main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extra, "BIUMIND.md"), []byte("# extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoadWithOptions(cwd, Options{ExtraDirs: []string{extra}})
	prompt := got.SystemPrompt()
	if !strings.Contains(prompt, "# main") {
		t.Errorf("missing main BIUMIND.md; got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# extra") {
		t.Errorf("missing extra BIUMIND.md; got:\n%s", prompt)
	}
}

func TestLoadWithOptions_ExtraDirs_DedupCwd(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), []byte("# main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadWithOptions(cwd, Options{ExtraDirs: []string{cwd}})
	// cwd file should appear only once even though it's in both lists.
	count := strings.Count(got.SystemPrompt(), "# main")
	if count != 1 {
		t.Errorf("dup BIUMIND.md count = %d; want 1", count)
	}
}
