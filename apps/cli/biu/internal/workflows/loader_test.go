package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.All(); len(got) != 0 {
		t.Errorf("empty registry should yield no workflows; got %v", got)
	}
}

func TestLoadUserWorkflow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeWorkflow(t, filepath.Join(home, ".biumind", "workflows"), "ship.md",
		"---\n"+
			"description: Plan, implement, review, verify\n"+
			"requires: git_repo\n"+
			"args: feature\n"+
			"---\n"+
			"## Steps\n\n"+
			"1. /ultraplan implement: $ARGUMENTS\n")
	r, _ := Load(t.TempDir())
	w, ok := r.Lookup("ship")
	if !ok {
		t.Fatal("ship not registered")
	}
	if w.Description != "Plan, implement, review, verify" {
		t.Errorf("description: got %q", w.Description)
	}
	if len(w.Requires) != 1 || w.Requires[0] != "git_repo" {
		t.Errorf("requires: got %v", w.Requires)
	}
	if len(w.Args) != 1 || w.Args[0].Name != "feature" {
		t.Errorf("args: got %v", w.Args)
	}
	if w.Source != SrcUser {
		t.Errorf("source: got %q", w.Source)
	}
}

// Project workflows shadow user workflows of the same name.
func TestLoadProjectShadowsUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeWorkflow(t, filepath.Join(home, ".biumind", "workflows"), "ship.md",
		"---\ndescription: USER\n---\nuser body\n")
	cwd := t.TempDir()
	writeWorkflow(t, filepath.Join(cwd, ".biumind", "workflows"), "ship.md",
		"---\ndescription: PROJECT\n---\nproject body\n")
	r, _ := Load(cwd)
	w, _ := r.Lookup("ship")
	if w.Source != SrcProject {
		t.Errorf("source: got %q, want project", w.Source)
	}
	if !strings.Contains(w.Body, "project body") {
		t.Errorf("body should be project's; got %q", w.Body)
	}
}

// Loader reads .biumind/workflows/ only. A stray .claude/ directory
// (from a former Claude Code install) must NOT be picked up.
func TestLoadIgnoresClaudeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeWorkflow(t, filepath.Join(home, ".claude", "workflows"), "stale.md",
		"stale body\n")
	r, _ := Load(t.TempDir())
	if _, ok := r.Lookup("stale"); ok {
		t.Errorf(".claude workflow must NOT be loaded; got %+v", r.All())
	}
}

// Render substitutes the standard placeholders.
func TestRenderSubstitutes(t *testing.T) {
	w := &Workflow{Body: "Args: $ARGUMENTS\nCwd: $CWD\nDate: $DATE\n"}
	got := w.Render("foo bar")
	if !strings.Contains(got, "Args: foo bar") {
		t.Errorf("$ARGUMENTS not substituted: %q", got)
	}
	if !strings.Contains(got, "Cwd: ") {
		t.Errorf("$CWD not substituted: %q", got)
	}
	if !strings.Contains(got, "Date: 20") {
		t.Errorf("$DATE not substituted: %q", got)
	}
}

// Verify with no requires returns nil.
func TestVerifyNoRequiresOK(t *testing.T) {
	w := &Workflow{}
	if err := w.Verify(t.TempDir()); err != nil {
		t.Errorf("no requires should not error; got %v", err)
	}
}

// Unknown check name fails fast.
func TestVerifyUnknownCheckRejects(t *testing.T) {
	w := &Workflow{Requires: []string{"not_a_real_check"}}
	err := w.Verify(t.TempDir())
	if err == nil {
		t.Errorf("unknown check should fail")
	}
	if !strings.Contains(err.Error(), "unknown check") {
		t.Errorf("error should mention unknown check; got %v", err)
	}
}

// git_repo check on a non-git directory must fail.
func TestVerifyGitRepoFailsOutsideRepo(t *testing.T) {
	w := &Workflow{Requires: []string{"git_repo"}}
	err := w.Verify(t.TempDir())
	if err == nil {
		t.Errorf("git_repo on tempdir should fail")
	}
	if !strings.Contains(err.Error(), "git work tree") {
		t.Errorf("error should mention git; got %v", err)
	}
}

// git_repo check on an actual repo passes. Builds a tiny repo
// inline so the test doesn't depend on the host's biu repo
// layout.
func TestVerifyGitRepoPassesInRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t"},
		{"git", "config", "user.name", "t"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
	}
	w := &Workflow{Requires: []string{"git_repo"}}
	if err := w.Verify(dir); err != nil {
		t.Errorf("git_repo should pass in real repo; got %v", err)
	}
}

// clean_tree with an uncommitted file fails.
func TestVerifyCleanTreeFailsWithDirtyFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, c := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t"},
		{"git", "config", "user.name", "t"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workflow{Requires: []string{"clean_tree"}}
	err := w.Verify(dir)
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("clean_tree should fail; got %v", err)
	}
}

// validWorkflowName covers every code path so future name
// conventions don't regress.
func TestValidWorkflowName(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"ship":        true,
		"my-flow":     true,
		"my_flow":     true,
		"with space":  false,
		"1starts-num": false,
		"-leading":    false,
		"has.dot":     false,
		strings.Repeat("x", 33): false,
	}
	for in, want := range cases {
		if got := validWorkflowName(in); got != want {
			t.Errorf("validWorkflowName(%q) = %v, want %v", in, got, want)
		}
	}
}

// All() returns workflows sorted by name.
func TestAllSortedByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biumind", "workflows")
	for _, n := range []string{"zebra", "alpha", "mango"} {
		writeWorkflow(t, dir, n+".md", "x")
	}
	r, _ := Load(t.TempDir())
	got := r.All()
	want := []string{"alpha", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("count: %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("position %d: %q vs %q", i, got[i].Name, want[i])
		}
	}
}

// args parsing supports both bracket form `[a, b]` and bare form
// `a, b` — plus the simpler one-name form `feature`.
func TestArgsListParsing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		argsField string
		want      []string
	}{
		{"feature", []string{"feature"}},
		{"a, b, c", []string{"a", "b", "c"}},
		{"[a, b]", []string{"a", "b"}},
		{"", nil},
	}
	for i, c := range cases {
		name := "wf" + stringDigit(i)
		writeWorkflow(t, filepath.Join(home, ".biumind", "workflows"), name+".md",
			"---\nargs: "+c.argsField+"\n---\nbody")
		r, _ := Load(t.TempDir())
		w, _ := r.Lookup(name)
		got := []string{}
		for _, a := range w.Args {
			got = append(got, a.Name)
		}
		if len(got) != len(c.want) {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
			continue
		}
		for j := range got {
			if got[j] != c.want[j] {
				t.Errorf("case %d position %d: got %q want %q", i, j, got[j], c.want[j])
			}
		}
	}
}

func stringDigit(n int) string { return string(rune('a' + n)) }
