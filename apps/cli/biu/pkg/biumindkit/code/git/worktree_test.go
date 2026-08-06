package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndRemoveWorktree(t *testing.T) {
	repo := tempRepo(t)
	ctx := context.Background()
	wtPath := filepath.Join(t.TempDir(), "wt-task1")

	wc, err := CreateWorktree(ctx, repo, wtPath, "biu/task-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if wc.Branch != "biu/task-1" || wc.WorktreePath != wtPath {
		t.Errorf("unexpected created: %+v", wc)
	}
	if wc.BaseCommit == "" {
		t.Errorf("base commit not resolved")
	}
	// worktree 目录真的建出来了 + 有 README(从 HEAD checkout)。
	if _, err := os.Stat(filepath.Join(wtPath, "README.md")); err != nil {
		t.Errorf("worktree missing checked-out file: %v", err)
	}

	// 重复路径 → 报已存在。
	if _, err := CreateWorktree(ctx, repo, wtPath, "biu/task-1b", ""); err == nil {
		t.Errorf("expected already-exists error")
	}

	// 移除 + 删分支。
	if err := RemoveWorktree(ctx, repo, wtPath, wc.Branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir not removed")
	}
	if branchExists(ctx, dirRunner(repo), "biu/task-1") {
		t.Errorf("branch not deleted")
	}
}

func TestCreateWorktree_BranchConflictSuffix(t *testing.T) {
	repo := tempRepo(t)
	ctx := context.Background()
	// 先占用 biu/dup 分支名。
	mustGit(t, repo, "branch", "biu/dup")

	wtPath := filepath.Join(t.TempDir(), "wt-dup")
	wc, err := CreateWorktree(ctx, repo, wtPath, "biu/dup", "")
	if err != nil {
		t.Fatal(err)
	}
	if wc.Branch != "biu/dup-2" {
		t.Errorf("expected suffixed branch biu/dup-2, got %q", wc.Branch)
	}
}

func TestWorktreeDiffStats(t *testing.T) {
	repo := tempRepo(t)
	ctx := context.Background()
	wtPath := filepath.Join(t.TempDir(), "wt-stats")

	wc, err := CreateWorktree(ctx, repo, wtPath, "biu/stats", "")
	if err != nil {
		t.Fatal(err)
	}

	// 在 worktree 里:改已跟踪 + 加未跟踪 + 提交一次(让 HEAD 前进)。
	if err := os.WriteFile(filepath.Join(wtPath, "README.md"), []byte("# hello\nnew line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "added.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := WorktreeDiffStatsOf(ctx, wtPath, wc.BaseBranch)
	if err != nil {
		t.Fatal(err)
	}
	// README +1 行(改),added.txt +2 行(未跟踪)。共 +3。
	if stats.Additions != 3 {
		t.Errorf("want 3 additions, got %+v", stats)
	}
}

func TestMergeWorktree_FastForwardOntoBase(t *testing.T) {
	repo := tempRepo(t)
	ctx := context.Background()
	// 当前分支名(tempRepo init 默认 main 或 master)。
	cur, _ := dirRunner(repo)(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	base := strings.TrimSpace(cur)

	wtPath := filepath.Join(t.TempDir(), "wt-merge")
	wc, err := CreateWorktree(ctx, repo, wtPath, "biu/merge", "")
	if err != nil {
		t.Fatal(err)
	}

	// worktree 里提交一笔。
	if err := os.WriteFile(filepath.Join(wtPath, "feature.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wtPath, "add", "feature.txt")
	mustGit(t, wtPath, "commit", "-m", "feat: feature")

	// 主仓在 base 上 → merge --no-ff。
	out, err := MergeWorktree(ctx, repo, wtPath, wc.Branch, base)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	_ = out
	// 主仓 base 上应能看到 feature.txt。
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Errorf("merge didn't bring feature.txt into main repo: %v", err)
	}
}

func TestMergeWorktree_RefusesDirtyWorktree(t *testing.T) {
	repo := tempRepo(t)
	ctx := context.Background()
	cur, _ := dirRunner(repo)(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	base := strings.TrimSpace(cur)

	wtPath := filepath.Join(t.TempDir(), "wt-dirty")
	wc, err := CreateWorktree(ctx, repo, wtPath, "biu/dirty", "")
	if err != nil {
		t.Fatal(err)
	}
	// 未提交改动。
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeWorktree(ctx, repo, wtPath, wc.Branch, base); err == nil {
		t.Errorf("expected refusal on dirty worktree")
	}
}
