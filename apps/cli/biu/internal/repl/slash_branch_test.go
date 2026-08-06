package repl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// /branch on a non-git directory falls back gracefully.
func TestSlashBranch_outsideGitTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	got := model{}.handleBranch([]string{"/branch"})
	if !strings.Contains(got, "not inside a git work tree") {
		t.Errorf("non-repo cwd should report not-in-tree, got %q", got)
	}
}

// /branch in a fresh repo — no upstream, no commits, clean.
func TestSlashBranch_freshRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	mustGitInit(t, dir)

	got := model{}.handleBranch([]string{"/branch"})
	if !strings.Contains(got, "branch:") {
		t.Errorf("missing branch line: %s", got)
	}
	if !strings.Contains(got, "working tree") {
		t.Errorf("missing working tree line: %s", got)
	}
	// Fresh repo has no commits → "no commits yet" rather than the
	// usual upstream tracking pair.
	if !strings.Contains(got, "no commits yet") {
		t.Errorf("fresh repo should be marked 'no commits yet': %s", got)
	}
}

// /branch shows dirty file count when working tree has changes.
func TestSlashBranch_dirtyTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	mustGitInit(t, dir)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644)

	got := model{}.handleBranch([]string{"/branch"})
	if !strings.Contains(got, "1 file(s) modified") {
		t.Errorf("dirty tree should be reported, got %q", got)
	}
}

// /branch never panics even when called rapidly. Exercises the
// timeout path indirectly.
func TestSlashBranch_neverPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("/branch panicked: %v", r)
		}
	}()
	_ = model{}.handleBranch([]string{"/branch"})
}

func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
