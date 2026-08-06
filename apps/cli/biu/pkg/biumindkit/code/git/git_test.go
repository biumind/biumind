package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tempRepo 建一个带初始提交的临时 git 仓库,返回其路径。git 不可用时 skip。
func tempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "user.email", "test@biumind.dev")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "README.md", "# hello\n")
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "chore: initial commit")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusFiles_StagedAndUntracked(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	// 改已跟踪文件(未暂存) + 新增未跟踪文件。
	writeFile(t, dir, "README.md", "# hello\nmore\n")
	writeFile(t, dir, "new.txt", "fresh\n")

	files, err := StatusFiles(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if fc, ok := byPath["README.md"]; !ok || fc.Staged || fc.Status != "M" {
		t.Errorf("README.md: want unstaged M, got %+v (ok=%v)", fc, ok)
	}
	if fc, ok := byPath["new.txt"]; !ok || fc.Status != "?" {
		t.Errorf("new.txt: want untracked ?, got %+v (ok=%v)", fc, ok)
	}

	// 暂存 README,应转为 staged。
	if err := Stage(ctx, dir, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	files, _ = StatusFiles(ctx, dir)
	var sawStaged bool
	for _, f := range files {
		if f.Path == "README.md" && f.Staged && f.Status == "M" {
			sawStaged = true
		}
	}
	if !sawStaged {
		t.Errorf("after stage, README.md not reported staged: %+v", files)
	}
}

func TestStageCommitLog(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	writeFile(t, dir, "a.go", "package a\n")
	if err := Stage(ctx, dir, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitChanges(ctx, dir, "feat: add a"); err != nil {
		t.Fatal(err)
	}
	commits, err := Log(ctx, dir, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("want 2 commits, got %d: %+v", len(commits), commits)
	}
	if commits[0].Message != "feat: add a" {
		t.Errorf("newest commit message = %q", commits[0].Message)
	}
	if commits[0].ShortHash == "" || commits[0].Author != "Test" {
		t.Errorf("commit meta incomplete: %+v", commits[0])
	}

	// commitDetail 的 numstat。
	d, err := CommitDetailOf(ctx, dir, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalAdditions != 1 || len(d.Files) != 1 || d.Files[0].Path != "a.go" {
		t.Errorf("commit detail mismatch: %+v", d)
	}
}

func TestUnstage(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()
	writeFile(t, dir, "b.txt", "x\n")
	if err := Stage(ctx, dir, []string{"b.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(ctx, dir, []string{"b.txt"}); err != nil {
		t.Fatal(err)
	}
	files, _ := StatusFiles(ctx, dir)
	for _, f := range files {
		if f.Path == "b.txt" && f.Staged {
			t.Errorf("b.txt still staged after unstage")
		}
	}
}

func TestFileDiff_TrackedAndUntracked(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	// 已跟踪改动。
	writeFile(t, dir, "README.md", "# hello\nchanged\n")
	d, err := FileDiff(ctx, dir, "README.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+changed") {
		t.Errorf("tracked diff missing +changed:\n%s", d)
	}

	// 未跟踪新文件 → --no-index 回退,整文件当新增。
	writeFile(t, dir, "fresh.txt", "brand new\n")
	d2, err := FileDiff(ctx, dir, "fresh.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d2, "brand new") {
		t.Errorf("untracked diff missing content:\n%s", d2)
	}
}

func TestDiscardFile(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	// 已跟踪改动 → restore 还原。
	writeFile(t, dir, "README.md", "# hello\nlocal edit\n")
	if err := DiscardFile(ctx, dir, "README.md", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if string(got) != "# hello\n" {
		t.Errorf("README not restored, got %q", got)
	}

	// 未跟踪 → clean 删除。
	writeFile(t, dir, "junk.txt", "trash\n")
	if err := DiscardFile(ctx, dir, "junk.txt", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.txt")); !os.IsNotExist(err) {
		t.Errorf("junk.txt not removed")
	}
}

func TestBranches(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	if err := CreateBranch(ctx, dir, "feature-x", true); err != nil {
		t.Fatal(err)
	}
	branches, err := ListBranches(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawCurrent bool
	for _, b := range branches {
		if b.Name == "feature-x" && b.Current {
			sawCurrent = true
		}
	}
	if !sawCurrent {
		t.Errorf("feature-x not current after checkout -b: %+v", branches)
	}
}

func TestRemoteCounts_NoUpstream(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()
	rc, err := RemoteCountsOf(ctx, dir, "")
	if err != nil {
		t.Fatalf("remoteCounts should not error without upstream: %v", err)
	}
	if rc.Ahead != 0 || rc.Behind != 0 {
		t.Errorf("want 0/0 without upstream, got %+v", rc)
	}
	if rc.Branch == "" {
		t.Errorf("branch name should be resolved, got empty")
	}
}

func TestRepoRootAndChangedFiles(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	root, err := RepoRoot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// macOS /var → /private/var 等 symlink:用 suffix 容差比较 basename。
	if !strings.HasSuffix(root, dir[strings.LastIndex(dir, "/"):]) && root != dir {
		t.Logf("repo root %q vs dir %q (symlink-normalized, acceptable)", root, dir)
	}

	// 记录 base,提交一笔改动,changedFiles 应列出。
	base, _ := dirRunner(dir)(ctx, "rev-parse", "HEAD")
	base = strings.TrimSpace(base)
	writeFile(t, dir, "added.go", "package x\n")
	writeFile(t, dir, "README.md", "# hello\nmore\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "feat: changes")

	changed, err := ChangedFiles(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, c := range changed {
		byPath[c.Path] = c.Status
	}
	if byPath["added.go"] != "A" || byPath["README.md"] != "M" {
		t.Errorf("changed files mismatch: %+v", changed)
	}
}

func TestListUntrackedAndRangeFileDiff(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()
	base, _ := dirRunner(dir)(ctx, "rev-parse", "HEAD")
	base = strings.TrimSpace(base)

	// 提交一个文件改动(进入 base..HEAD 范围)。
	writeFile(t, dir, "README.md", "# hello\nline2\n")
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "edit")
	// 加未跟踪文件。
	writeFile(t, dir, "untracked.txt", "u\n")

	un, err := ListUntracked(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0] != "untracked.txt" {
		t.Errorf("untracked mismatch: %+v", un)
	}

	d, err := RangeFileDiff(ctx, dir, base, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+line2") {
		t.Errorf("range file diff missing change:\n%s", d)
	}
}

func TestGenerateCommitMessage_FallsBackToUnstaged(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()

	// 仅有未暂存改动(不 git add)。
	writeFile(t, dir, "README.md", "# hello\nunstaged edit\n")

	var capturedDiff string
	gen := func(ctx context.Context, prompt string) (string, error) {
		capturedDiff = prompt
		return "feat: edit readme", nil
	}
	msg, err := GenerateCommitMessage(ctx, dir, gen)
	if err != nil {
		t.Fatalf("should fall back to unstaged diff, got err: %v", err)
	}
	if msg != "feat: edit readme" {
		t.Errorf("msg = %q", msg)
	}
	if !strings.Contains(capturedDiff, "unstaged edit") {
		t.Errorf("prompt should contain unstaged diff:\n%s", capturedDiff)
	}
}

func TestGenerateCommitMessage_EmptyWhenClean(t *testing.T) {
	dir := tempRepo(t)
	ctx := context.Background()
	gen := func(ctx context.Context, prompt string) (string, error) {
		return "should not be called", nil
	}
	if _, err := GenerateCommitMessage(ctx, dir, gen); err == nil {
		t.Errorf("expected error on clean tree")
	}
}

func TestParsePorcelainZ_RenameSkipsOrigPath(t *testing.T) {
	// R  old.txt\x00new.txt\x00 —— 重命名项后跟原路径段,应只产出 new.txt。
	raw := "R  new.txt\x00old.txt\x00 M tracked.txt\x00?? untracked.txt\x00"
	got := parsePorcelainZ(raw)
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
	}
	if !paths["new.txt"] || paths["old.txt"] {
		t.Errorf("rename parse wrong: %+v", got)
	}
	if !paths["tracked.txt"] || !paths["untracked.txt"] {
		t.Errorf("missing other entries: %+v", got)
	}
}
