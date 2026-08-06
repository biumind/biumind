// Package git 是编码模块的 Git 能力薄封装 —— 复用 internal/gitassist 的 git-shape
// 逻辑，按工作目录（git -C <cwd>）跑命令。
//
// ⚠️ 本包 import apps/cli/biu/internal/gitassist（Go internal 规则允许，因二者同在
// apps/cli/biu/ 下）。**因此 biumindkit/code/* 不可被 services/* import**（services
// 不在 apps/cli/biu/ 下，会触发 internal 导入禁止）。Code 模块是桌面 bridge 专属，
// 这一约束符合预期（远控走 brain agent-plane，不直接 import 本包）。
//
// M0 只实现 git.status；M4 补齐工作区/分支/历史/diff/stage/commit/
// push/pull/remoteCounts + AI commit msg（走 model-relay，I6）。worktree CRUD 在 M4-E
// 与 Dart 迁移一起补。
//
// 形状约定：JSON 字段一律 snake_case（与 fs.go 一致），Dart 侧 fromJson 对齐。
package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
)

// ─── 工作区状态（逐文件，UI 用）────────────────────────────────

// FileChange 是工作区里一个文件的变更项。
// 同一文件若既有暂存改动又有未暂存改动，会出现两条（staged=true / false 各一）。
type FileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // 单字符：M/A/D/R/C/?/...
	Staged bool   `json:"staged"`
}

// StatusFiles 返回逐文件变更列表（porcelain=v1 -z，含全部未跟踪文件）。
// 空仓 / 干净工作区返回空切片。
func StatusFiles(ctx context.Context, cwd string) ([]FileChange, error) {
	run := dirRunner(cwd)
	// -z 用 NUL 分隔，core.quotePath=false 保非 ASCII 路径原样，避免引号转义。
	out, err := run(ctx, "-c", "core.quotePath=false", "status",
		"--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parsePorcelainZ(out), nil
}

// parsePorcelainZ 解析 `git status --porcelain=v1 -z` 的 NUL 分隔输出。
// 重命名/复制（R/C）项后跟一个额外的 NUL 段（原路径），跳过。
func parsePorcelainZ(raw string) []FileChange {
	var out []FileChange
	entries := strings.Split(raw, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 || e[2] != ' ' {
			continue
		}
		x, y := e[0], e[1]
		path := e[3:]
		// R/C 项消费下一个 NUL 段（原路径），不展示。
		if x == 'R' || x == 'C' {
			i++
		}
		if x == '?' && y == '?' {
			out = append(out, FileChange{Path: path, Status: "?", Staged: false})
			continue
		}
		if x != ' ' && x != '?' {
			out = append(out, FileChange{Path: path, Status: string(x), Staged: true})
		}
		if y != ' ' && y != '?' {
			out = append(out, FileChange{Path: path, Status: string(y), Staged: false})
		}
	}
	return out
}

// ─── 旧版聚合状态（M0 git.status 保留）─────────────────────────

// StatusResult 是 git.status 的返回结构，镜像 gitassist.Status 并补 branch / clean。
type StatusResult struct {
	Branch    string   `json:"branch"`
	Staged    []string `json:"staged"`
	Modified  []string `json:"modified"`
	Untracked []string `json:"untracked"`
	Conflicts []string `json:"conflicts"`
	Clean     bool     `json:"clean"`
}

// Status 返回 cwd 仓库的聚合工作区状态（M0 兼容；UI 用 StatusFiles）。
func Status(ctx context.Context, cwd string) (StatusResult, error) {
	run := dirRunner(cwd)
	st, err := gitassist.GetStatus(ctx, run)
	if err != nil {
		return StatusResult{}, err
	}
	branch, _ := gitassist.CurrentBranch(ctx, run)
	return StatusResult{
		Branch:    branch,
		Staged:    st.Staged,
		Modified:  st.Modified,
		Untracked: st.Untracked,
		Conflicts: st.Conflicts,
		Clean:     st.Empty(),
	}, nil
}

// ─── 分支 ────────────────────────────────────────────────────

// Branch 是一条分支（本地或远程）。
type Branch struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Remote  string `json:"remote"` // 远程分支的 remote 名（如 "origin"）；本地分支空
}

// ListBranches 列出本地 + 远程分支（git branch -a）。跳过 HEAD 指针行。
func ListBranches(ctx context.Context, cwd string) ([]Branch, error) {
	run := dirRunner(cwd)
	out, err := run(ctx, "branch", "-a")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		current := strings.HasPrefix(line, "* ")
		raw := strings.TrimSpace(line[2:])
		if strings.Contains(raw, " -> ") { // remotes/origin/HEAD -> origin/main
			continue
		}
		if raw == "" {
			continue
		}
		if rem := strings.TrimPrefix(raw, "remotes/"); rem != raw {
			name := rem
			remote := ""
			if i := strings.Index(name, "/"); i >= 0 {
				remote = name[:i]
			}
			branches = append(branches, Branch{Name: name, Current: current, Remote: remote})
		} else {
			branches = append(branches, Branch{Name: raw, Current: current})
		}
	}
	return branches, nil
}

// CreateBranch 新建分支。checkout=true 时同时切过去（git checkout -b）。
func CreateBranch(ctx context.Context, cwd, name string, checkout bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("git: empty branch name")
	}
	run := dirRunner(cwd)
	if checkout {
		_, err := run(ctx, "checkout", "-b", name)
		return err
	}
	_, err := run(ctx, "branch", name)
	return err
}

// CheckoutBranch 切到已有分支。
func CheckoutBranch(ctx context.Context, cwd, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("git: empty branch name")
	}
	_, err := dirRunner(cwd)(ctx, "checkout", name)
	return err
}

// DeleteBranch 删除本地分支。force=true 用 -D（丢未合并提交），否则 -d。
func DeleteBranch(ctx context.Context, cwd, name string, force bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("git: empty branch name")
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := dirRunner(cwd)(ctx, "branch", flag, name)
	return err
}

// ─── 历史 / 提交详情 ──────────────────────────────────────────

// Commit 是历史里的一条提交摘要。
type Commit struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"short_hash"`
	Author    string   `json:"author"`
	Date      string   `json:"date"` // 相对时间（%ar）
	Message   string   `json:"message"`
	Refs      []string `json:"refs"`
}

// 用 NUL + RS 分隔，避免提交信息里的换行/分隔符歧义。
// %x00 = NUL（字段分隔），%x1e = RS（记录分隔）。
const logFormat = "%H%x00%h%x00%an%x00%ar%x00%s%x00%D%x1e"

// Log 返回提交历史，limit 条、跳过 skip 条（分页）。limit<=0 默认 50。
func Log(ctx context.Context, cwd string, limit, skip int) ([]Commit, error) {
	if limit <= 0 {
		limit = 50
	}
	run := dirRunner(cwd)
	args := []string{"log", "--pretty=format:" + logFormat,
		"-n", strconv.Itoa(limit)}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	out, err := run(ctx, args...)
	if err != nil {
		// 空仓（无 HEAD）git log 报错 —— 当成空历史而非失败。
		if strings.Contains(err.Error(), "does not have any commits") ||
			strings.Contains(err.Error(), "bad default revision") {
			return []Commit{}, nil
		}
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x00")
		if len(f) < 6 {
			continue
		}
		c := Commit{Hash: f[0], ShortHash: f[1], Author: f[2], Date: f[3], Message: f[4]}
		if refs := strings.TrimSpace(f[5]); refs != "" {
			for _, r := range strings.Split(refs, ",") {
				if r = strings.TrimSpace(r); r != "" {
					c.Refs = append(c.Refs, r)
				}
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// CommitFile 是某次提交里改动的一个文件 + 增删行数。
type CommitFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// CommitDetail 是单次提交的完整元数据 + 文件级 numstat。
type CommitDetail struct {
	Hash           string       `json:"hash"`
	ShortHash      string       `json:"short_hash"`
	Author         string       `json:"author"`
	Date           string       `json:"date"`
	Message        string       `json:"message"`
	Files          []CommitFile `json:"files"`
	TotalAdditions int          `json:"total_additions"`
	TotalDeletions int          `json:"total_deletions"`
}

// CommitDetailOf 返回某 hash 的提交详情（元数据 + 文件 numstat）。
func CommitDetailOf(ctx context.Context, cwd, hash string) (CommitDetail, error) {
	if strings.TrimSpace(hash) == "" {
		return CommitDetail{}, fmt.Errorf("git: empty commit hash")
	}
	run := dirRunner(cwd)
	info, err := run(ctx, "show", "--no-patch",
		"--format=HASH:%H%nSHORT:%h%nAUTHOR:%an%nDATE:%ar%nSUBJECT:%s", hash)
	if err != nil {
		return CommitDetail{}, err
	}
	d := CommitDetail{}
	for _, line := range strings.Split(info, "\n") {
		switch {
		case strings.HasPrefix(line, "HASH:"):
			d.Hash = strings.TrimPrefix(line, "HASH:")
		case strings.HasPrefix(line, "SHORT:"):
			d.ShortHash = strings.TrimPrefix(line, "SHORT:")
		case strings.HasPrefix(line, "AUTHOR:"):
			d.Author = strings.TrimPrefix(line, "AUTHOR:")
		case strings.HasPrefix(line, "DATE:"):
			d.Date = strings.TrimPrefix(line, "DATE:")
		case strings.HasPrefix(line, "SUBJECT:"):
			d.Message = strings.TrimPrefix(line, "SUBJECT:")
		}
	}
	// numstat：每行 "<add>\t<del>\t<path>"，二进制文件 add/del 为 "-"。
	ns, err := run(ctx, "diff-tree", "--no-commit-id", "--numstat", "-r", hash)
	if err != nil {
		return CommitDetail{}, err
	}
	for _, line := range strings.Split(ns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0]) // "-"（二进制）→ 0
		del, _ := strconv.Atoi(parts[1])
		d.Files = append(d.Files, CommitFile{Path: parts[2], Additions: add, Deletions: del})
		d.TotalAdditions += add
		d.TotalDeletions += del
	}
	return d, nil
}

// ─── Diff ────────────────────────────────────────────────────

// diffByteLimit 是工作区/单提交 diff 的字节上限（200KB / 500KB）。
const (
	fileDiffLimit = 200 << 10
	showDiffLimit = 500 << 10
)

// ShowDiff 返回整个提交的 diff（git show <hash>，不含 commit 头）。
func ShowDiff(ctx context.Context, cwd, hash string) (string, error) {
	if strings.TrimSpace(hash) == "" {
		return "", fmt.Errorf("git: empty commit hash")
	}
	out, err := dirRunner(cwd)(ctx, "show", "--format=", "--no-color", hash)
	if err != nil {
		return "", err
	}
	return truncate(out, showDiffLimit), nil
}

// ShowFileDiff 返回某提交里单个文件的 diff（git show <hash> -- <path>）。
func ShowFileDiff(ctx context.Context, cwd, hash, path string) (string, error) {
	if strings.TrimSpace(hash) == "" || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("git: hash and path required")
	}
	out, err := dirRunner(cwd)(ctx, "show", "--format=", "--no-color", hash, "--", path)
	if err != nil {
		return "", err
	}
	return truncate(out, showDiffLimit), nil
}

// FileDiff 返回工作区里单文件的 diff。staged=true 看暂存区 diff。
// 未跟踪文件（diff 为空且非 staged）回退 `diff --no-index /dev/null <abs>`，
// 让新文件也能显示全文为新增。
func FileDiff(ctx context.Context, cwd, path string, staged bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("git: empty file path")
	}
	run := dirRunner(cwd)
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	out, err := run(ctx, args...)
	if err != nil {
		return "", err
	}
	if out == "" && !staged {
		// 未跟踪新文件：与空文件比，整文件当新增显示。git diff --no-index 在有差异时
		// 退出码 1，dirRunner 会当 error 上抛 —— 但我们要的正是它的 stdout，故忽略该 err。
		abs := filepath.Join(cwd, path)
		fb, _ := run(ctx, "diff", "--no-color", "--no-index", os.DevNull, abs)
		return truncate(fb, fileDiffLimit), nil
	}
	return truncate(out, fileDiffLimit), nil
}

// ─── Stage / Unstage / Commit ─────────────────────────────────

// Stage 暂存指定文件（git add -- <paths>）。空列表 no-op。
func Stage(ctx context.Context, cwd string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := dirRunner(cwd)(ctx, append([]string{"add", "--"}, paths...)...)
	return err
}

// Unstage 取消暂存。有 HEAD 用 `restore --staged`，首提交前无 HEAD 用 `reset`。
func Unstage(ctx context.Context, cwd string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	run := dirRunner(cwd)
	base := []string{"restore", "--staged", "--"}
	if !hasHead(ctx, run) {
		base = []string{"reset", "--"}
	}
	_, err := run(ctx, append(base, paths...)...)
	return err
}

// StageAll 暂存全部改动（git add -A）。
func StageAll(ctx context.Context, cwd string) error {
	_, err := dirRunner(cwd)(ctx, "add", "-A")
	return err
}

// UnstageAll 取消全部暂存。无 HEAD 时退回 reset。
func UnstageAll(ctx context.Context, cwd string) error {
	run := dirRunner(cwd)
	if !hasHead(ctx, run) {
		_, err := run(ctx, "reset")
		return err
	}
	_, err := run(ctx, "restore", "--staged", ".")
	return err
}

// Commit 提交暂存区。message 经独立 arg 传递（exec 不过 shell，无注入风险）。
// 返回 git 的输出（含提交摘要）。
func CommitChanges(ctx context.Context, cwd, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("git: empty commit message")
	}
	return dirRunner(cwd)(ctx, "commit", "-m", message)
}

// ─── Discard ─────────────────────────────────────────────────

// DiscardFile 丢弃单文件改动。untracked=true 时删除未跟踪文件（git clean -f），
// 否则把已跟踪文件恢复到 HEAD（git restore）。
func DiscardFile(ctx context.Context, cwd, path string, untracked bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("git: empty file path")
	}
	run := dirRunner(cwd)
	if untracked {
		_, err := run(ctx, "clean", "-f", "--", path)
		return err
	}
	_, err := run(ctx, "restore", "--", path)
	return err
}

// DiscardAll 丢弃所有改动：已跟踪恢复 HEAD + 清未跟踪（git clean -fd）。
// 无 HEAD（首提交前）时把暂存的新增退回未跟踪再清。
func DiscardAll(ctx context.Context, cwd string) error {
	run := dirRunner(cwd)
	if hasHead(ctx, run) {
		if _, err := run(ctx, "restore", "--source=HEAD", "--staged", "--worktree", "."); err != nil {
			return err
		}
	} else {
		if _, err := run(ctx, "rm", "-r", "--cached", "--ignore-unmatch", "--", "."); err != nil {
			return err
		}
	}
	_, err := run(ctx, "clean", "-fd")
	return err
}

// ─── Push / Pull / RemoteCounts ───────────────────────────────

// Push 推送。branch 非空时推 `origin <branch>`，否则裸 `push`（用当前上游）。
// 返回 git 输出（成功/失败信息）。
func Push(ctx context.Context, cwd, branch string) (string, error) {
	args := []string{"push"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "origin", branch)
	}
	return dirRunner(cwd)(ctx, args...)
}

// Pull 拉取（git pull，含 merge/rebase 取决用户 git 配置）。
func Pull(ctx context.Context, cwd string) (string, error) {
	return dirRunner(cwd)(ctx, "pull")
}

// RemoteCounts 是相对上游的领先/落后提交数。
type RemoteCounts struct {
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	Branch string `json:"branch"`
}

// RemoteCountsOf 计算 branch（空则当前分支）相对 @{u} 的 ahead/behind。
// 无上游 / 无远程时返回 0/0（不报错，UI 据此隐藏推送计数）。
func RemoteCountsOf(ctx context.Context, cwd, branch string) (RemoteCounts, error) {
	run := dirRunner(cwd)
	if strings.TrimSpace(branch) == "" {
		b, err := run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return RemoteCounts{}, err
		}
		branch = strings.TrimSpace(b)
	}
	rc := RemoteCounts{Branch: branch}
	out, err := run(ctx, "rev-list", "--count", "--left-right", branch+"...@{u}")
	if err != nil {
		return rc, nil // 无上游：当 0/0，不致命
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) == 2 {
		rc.Ahead, _ = strconv.Atoi(parts[0])
		rc.Behind, _ = strconv.Atoi(parts[1])
	}
	return rc, nil
}

// ─── AI commit message（走 model-relay，I6）──────────────────

// GenerateCommitMessage 用暂存区 diff + 最近提交风格，经注入的 gen（model-relay
// 适配器）生成 Conventional Commits 消息。gen 为 nil（daemon 未配 provider）时报错。
// 暂存区为空时报错（只对 staged 生成）。
func GenerateCommitMessage(ctx context.Context, cwd string, gen gitassist.Generator) (string, error) {
	run := dirRunner(cwd)
	// 优先用暂存区 diff(将提交的内容);没暂存任何东西时回退工作区全部改动,方便
	// 用户「先生成信息再决定暂存」(很多人这么用)。两者都空才报错。
	diff, err := gitassist.Diff(ctx, run, true, 50<<10)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		diff, err = gitassist.Diff(ctx, run, false, 50<<10)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("git: no changes to generate a commit message for")
	}
	recent, _ := gitassist.RecentLog(ctx, run, 10)
	return gitassist.GenerateCommitMessage(ctx, gen, diff, recent)
}

// ─── worktree 迁移所需的读方法(M4-E)──────────────────────────

// RepoRoot 返回 cwd 所在 git 仓库的工作树根(git rev-parse --show-toplevel)。
// 非 git 目录 → error(调用方据此回退非隔离 workspace)。
func RepoRoot(ctx context.Context, cwd string) (string, error) {
	out, err := dirRunner(cwd)(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("git: empty toplevel")
	}
	return root, nil
}

// NameStatus 是 diff --name-status 的一项。重命名/复制只取目标路径。
type NameStatus struct {
	Status string `json:"status"` // 单字符 A/M/D/R/C...
	Path   string `json:"path"`
}

// ChangedFiles 返回 baseRef..HEAD 的 name-status 列表(供 worktree artifact 采集)。
// baseRef 空报错(无基线无从比)。
func ChangedFiles(ctx context.Context, cwd, baseRef string) ([]NameStatus, error) {
	if strings.TrimSpace(baseRef) == "" {
		return nil, fmt.Errorf("git: base ref required")
	}
	out, err := dirRunner(cwd)(ctx, "diff", "--name-status", baseRef+"..HEAD")
	if err != nil {
		return nil, err
	}
	var res []NameStatus
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 行形如 "M\tpath" 或 "R100\told\tnew" —— tab 分隔,取首字母 + 末段。
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		res = append(res, NameStatus{
			Status: string(parts[0][0]),
			Path:   parts[len(parts)-1],
		})
	}
	return res, nil
}

// ListUntracked 返回未跟踪且未被忽略的文件(git ls-files --others --exclude-standard)。
func ListUntracked(ctx context.Context, cwd string) ([]string, error) {
	out, err := dirRunner(cwd)(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var res []string
	for _, p := range strings.Split(out, "\x00") {
		if strings.TrimSpace(p) != "" {
			res = append(res, p)
		}
	}
	return res, nil
}

// RangeFileDiff 返回 baseRef..HEAD 范围内单文件的 diff(供 preview 生成)。
// 截断到 fileDiffLimit。baseRef 空报错。
func RangeFileDiff(ctx context.Context, cwd, baseRef, path string) (string, error) {
	if strings.TrimSpace(baseRef) == "" || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("git: base ref and path required")
	}
	out, err := dirRunner(cwd)(ctx, "diff", "--no-color", baseRef+"..HEAD", "--", path)
	if err != nil {
		return "", err
	}
	return truncate(out, fileDiffLimit), nil
}

// ─── helpers ─────────────────────────────────────────────────

// dirRunner 返回一个在指定工作目录下跑 git 的 gitassist.Runner —— 用 `git -C <cwd>`
// 而非 chdir，避免影响进程全局 cwd（多任务并发安全）。
func dirRunner(cwd string) gitassist.Runner {
	return func(ctx context.Context, args ...string) (string, error) {
		full := append([]string{"-C", cwd}, args...)
		return gitassist.DefaultRunner(ctx, full...)
	}
}

// hasHead 报告仓库是否已有 HEAD 提交（首次提交前为 false）。
func hasHead(ctx context.Context, run gitassist.Runner) bool {
	_, err := run(ctx, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// truncate 把超 limit 字节的输出截断并追加提示（避免巨型 diff 撑爆 WS 帧）。
func truncate(s string, limit int) string {
	if limit > 0 && len(s) > limit {
		return s[:limit] + "\n\n…(truncated)"
	}
	return s
}
