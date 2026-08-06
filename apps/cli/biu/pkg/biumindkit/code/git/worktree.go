// worktree.go —— 任务 worktree 的生命周期(M4-E)，覆盖 create / merge / remove / diff_stats
// 四个 worktree 命令。
//
// BiuMind 现有 Dart 实现把 worktree 放 ~/.biumind/code/worktrees/<taskId>(不在项目内),
// 故这里不做"路径必须在项目内"的校验 —— 改由 git 自身把关(worktree remove 只接受本仓
// 已注册的 worktree)。worktreePath 由调用方(Dart)给定,Go 只负责 git 操作。

package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorktreeCreated 是 CreateWorktree 的返回。镜像 Dart WorkspaceRef 所需字段。
type WorktreeCreated struct {
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"base_branch"`
	BaseCommit   string `json:"base_commit"`
}

// CreateWorktree 在 projectPath 仓库里新建一个 worktree:
//   - baseRef 空 → 用 HEAD;解析出 baseCommit(供 diff 基线)+ baseBranch(展示)
//   - 分支名取 preferredBranch,若已存在则加 -2..-19 后缀(对齐现有 Dart 行为)
//   - `git worktree add <worktreePath> -b <branch> <baseCommit>`
//
// worktreePath 已存在时报错(幂等 resume 由调用方先判存在再决定是否调本函数)。
func CreateWorktree(ctx context.Context, projectPath, worktreePath, preferredBranch, baseRef string) (WorktreeCreated, error) {
	if strings.TrimSpace(worktreePath) == "" {
		return WorktreeCreated{}, fmt.Errorf("git: worktree path required")
	}
	if strings.TrimSpace(preferredBranch) == "" {
		return WorktreeCreated{}, fmt.Errorf("git: preferred branch required")
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return WorktreeCreated{}, fmt.Errorf("git: worktree path already exists: %s", worktreePath)
	}
	run := dirRunner(projectPath)

	ref := baseRef
	if strings.TrimSpace(ref) == "" {
		ref = "HEAD"
	}
	bc, err := run(ctx, "rev-parse", ref)
	if err != nil {
		return WorktreeCreated{}, err
	}
	baseCommit := strings.TrimSpace(bc)
	bb, _ := run(ctx, "rev-parse", "--abbrev-ref", ref)
	baseBranch := strings.TrimSpace(bb)

	// 分支冲突 → 加 -N 后缀。
	branch := preferredBranch
	for attempt := 2; attempt < 20; attempt++ {
		if !branchExists(ctx, run, branch) {
			break
		}
		branch = fmt.Sprintf("%s-%d", preferredBranch, attempt)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return WorktreeCreated{}, fmt.Errorf("git: mkdir worktrees parent: %w", err)
	}

	if _, err := run(ctx, "worktree", "add", worktreePath, "-b", branch, baseCommit); err != nil {
		return WorktreeCreated{}, err
	}
	return WorktreeCreated{
		WorktreePath: worktreePath,
		Branch:       branch,
		BaseBranch:   baseBranch,
		BaseCommit:   baseCommit,
	}, nil
}

// RemoveWorktree 强制移除 worktree(含未提交改动)并可选删分支。git 自身只接受本仓
// 已注册的 worktree,故无需额外路径校验。branch 空时只移除不删分支。
func RemoveWorktree(ctx context.Context, projectPath, worktreePath, branch string) error {
	if strings.TrimSpace(worktreePath) == "" {
		return fmt.Errorf("git: worktree path required")
	}
	run := dirRunner(projectPath)
	if _, err := run(ctx, "worktree", "remove", "--force", worktreePath); err != nil {
		return err
	}
	if strings.TrimSpace(branch) != "" {
		// -D:允许删未合并分支(丢弃语义)。
		if _, err := run(ctx, "branch", "-D", branch); err != nil {
			return err
		}
	}
	return nil
}

// WorktreeDiffStats 计算 worktree 工作树(含未提交 + 未跟踪)相对 base 与 HEAD 的
// merge-base 的 +/− 行数。
type WorktreeDiffStats struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

// WorktreeDiffStatsOf 计算上述统计。用 merge-base 而非 baseBranch 本身,避免主仓 base
// 推进后把别人的提交算进来。
func WorktreeDiffStatsOf(ctx context.Context, worktreePath, baseBranch string) (WorktreeDiffStats, error) {
	if strings.TrimSpace(baseBranch) == "" {
		return WorktreeDiffStats{}, fmt.Errorf("git: base branch required")
	}
	run := dirRunner(worktreePath)
	var stats WorktreeDiffStats

	mb, err := run(ctx, "merge-base", baseBranch, "HEAD")
	if err != nil {
		return WorktreeDiffStats{}, err
	}
	mergeBase := strings.TrimSpace(mb)
	if mergeBase != "" {
		num, err := run(ctx, "diff", "--numstat", mergeBase)
		if err != nil {
			return WorktreeDiffStats{}, err
		}
		a, d := accumulateNumstat(num)
		stats.Additions += a
		stats.Deletions += d
	}

	// 未跟踪文件:git diff 不列,逐个与 /dev/null 比。
	ls, err := run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return stats, nil // 未跟踪枚举失败不致命,返回已知统计
	}
	for _, rel := range strings.Split(ls, "\x00") {
		if rel == "" {
			continue
		}
		abs := filepath.Join(worktreePath, rel)
		// --no-index 有差异时退出码 1 → dirRunner 当 error;忽略 err 取 stdout。
		out, _ := run(ctx, "diff", "--numstat", "--no-index", os.DevNull, abs)
		a, d := accumulateNumstat(out)
		stats.Additions += a
		stats.Deletions += d
	}
	return stats, nil
}

// MergeWorktree 把 worktree 分支并回 base:
//   - worktree 有未提交改动 → 拒绝(防丢工作进度)
//   - 主仓正在 base 上 → git merge --no-ff <branch>
//   - 否则 → git fetch . <branch>:<base>(仅允许 fast-forward,不动主仓 HEAD)
//
// 返回 git 输出 / 说明。Dart 的 Workspace 接口当前不调用它,但 design §5.2 列了,补全。
func MergeWorktree(ctx context.Context, projectPath, worktreePath, branch, baseBranch string) (string, error) {
	if strings.TrimSpace(branch) == "" || strings.TrimSpace(baseBranch) == "" {
		return "", fmt.Errorf("git: branch and base branch required")
	}
	wtRun := dirRunner(worktreePath)
	st, err := wtRun(ctx, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(st) != "" {
		return "", fmt.Errorf("git: worktree has uncommitted changes; commit or stash before merging")
	}

	run := dirRunner(projectPath)
	cur, err := run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cur) == baseBranch {
		out, err := run(ctx, "merge", "--no-ff", branch)
		if err != nil {
			return "", fmt.Errorf("git: merge failed (main repo on %q; resolve manually): %s", baseBranch, out)
		}
		return strings.TrimSpace(out), nil
	}
	// 主仓不在 base:fetch . <branch>:<base> 把分支 ff 到 base ref,不切主仓 HEAD。
	refspec := branch + ":" + baseBranch
	if _, err := run(ctx, "fetch", ".", refspec); err != nil {
		return "", fmt.Errorf("git: cannot fast-forward %q (worktree diverged from base); merge manually: %w", baseBranch, err)
	}
	return fmt.Sprintf("fast-forwarded %q to %q", baseBranch, branch), nil
}

// ─── helpers ─────────────────────────────────────────────────

func branchExists(ctx context.Context, run runner, name string) bool {
	_, err := run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// runner 是 dirRunner 返回类型的别名(gitassist.Runner),便于 helper 签名简洁。
type runner = func(ctx context.Context, args ...string) (string, error)

// accumulateNumstat 累加 `git diff --numstat` 输出的增删行。二进制行(add/del 为 "-")按 0。
func accumulateNumstat(out string) (additions, deletions int) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		additions += a
		deletions += d
	}
	return
}
