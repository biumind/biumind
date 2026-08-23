package repoapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitMust runs git in dir, failing the test on error.
func gitMust(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// initSourceRepo creates a tiny local git repo with one commit and
// returns its path. CloneOrFetch works against local paths, which keeps
// the clone/fetch test hermetic (no network).
func initSourceRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := t.TempDir()
	gitMust(t, src, "init", "-b", "main")
	writeFile(t, src, "hello.txt", "v1\n")
	gitMust(t, src, "add", ".")
	gitMust(t, src, "commit", "-m", "v1")
	return src
}

func TestCloneThenFetch(t *testing.T) {
	src := initSourceRepo(t)
	dest := filepath.Join(t.TempDir(), "repo")
	ctx := context.Background()

	sha1, err := CloneOrFetch(ctx, src, "", dest)
	if err != nil {
		t.Fatalf("initial clone: %v", err)
	}
	if len(sha1) != 40 {
		t.Errorf("sha = %q, want full 40-char sha", sha1)
	}
	if _, err := os.Stat(filepath.Join(dest, "hello.txt")); err != nil {
		t.Error("cloned tree missing hello.txt")
	}

	// Advance the source; the second call must take the fetch+checkout
	// path and land on the new sha.
	gitMust(t, src, "commit", "--allow-empty", "-m", "v2")
	sha2, err := CloneOrFetch(ctx, src, "", dest)
	if err != nil {
		t.Fatalf("fetch update: %v", err)
	}
	if sha2 == sha1 {
		t.Error("fetch update should move HEAD to the new commit")
	}
	head, err := HeadSHA(ctx, dest)
	if err != nil || head != sha2 {
		t.Errorf("HeadSHA = %q,%v want %q", head, err, sha2)
	}
}
