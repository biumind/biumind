// Package code 是编码模块（BiuMind Code）在 biu CLI 内的能力内核 —— 把
// /v1/code/ws 上的 code_request（RPC 信封）分发到 PTY / Git / FS 子能力，
// 并持有跨连接共享的 PTY 管理器。
//
// 形状约定：
//   - Git/FS 是请求/响应 → 走 code_request{method} → Dispatch → code_response
//   - PTY 输出是高吞吐流 → pty.Manager 经连接级 PtyEmitter 把 code_pty_chunk /
//     code_pty_exit 帧写回打开它的那条 WS
//   - PTY 输入/尺寸是高频控制 → 独立帧 code_pty_input / code_pty_resize，由 ws
//     handler 直接调 Input/Resize，不走 RPC 信封
//
// ⚠️ 本包（经 git 子包）传递 import 了 internal/gitassist，故 **不可被 services/*
// import**（见 code/git/git.go 注释）。
package code

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/agent"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/fs"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/git"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/hooks"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/projcfg"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/pty"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/session"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/skillinstall"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/usage"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// PtyEmitter 是连接级回调，把某个 PTY 的输出/退出/会话事件写回打开它的那条 WS 连接。
// ws handler 实现它（写 code_pty_chunk / code_pty_exit / code_session_event 帧）。
type PtyEmitter interface {
	Chunk(ptyID string, data []byte)
	Exit(ptyID string, code int, errMsg string)
	// SessionEvent 推一条结构化会话事件(M3,从 agent JSONL 解析),按 taskID demux。
	SessionEvent(taskID string, event map[string]any)
}

// Service 是编码能力分发器。一个 biu serve 进程一个实例。
type Service struct {
	pty *pty.Manager
	// detect 解析 agent 二进制路径。默认 agent.DetectPath;测试可注入桩。
	detect func(agentType string) (string, error)
	// commitGen 是 AI commit msg 的 LLM 缝(走 model-relay,满足 I6)。daemon 起来时
	// 经 SetCommitGenerator 注入;未配 provider 时为 nil → git.generateCommitMessage
	// 返回明确错误而非静默。
	commitGen gitassist.Generator
}

// NewService 建带空 PTY 管理器的 Service。commitGen 默认 nil,由 bridge 经
// SetCommitGenerator 注入(测试不需要 AI commit 的可不设)。
func NewService() *Service {
	return &Service{pty: pty.NewManager(), detect: agent.DetectPath}
}

// InstallHooksOnStartup 在 serve 启动时幂等安装 agent hook(best-effort,异步,不阻塞
// 启动)。node 缺失/版本不够都不报错 —— UsableFor 会在任务启动时自行回退轮询。
func InstallHooksOnStartup() {
	go func() {
		if st := hooks.Install(); st.Error != "" {
			slog.Info("code: hook install (best-effort)", "err", st.Error)
		}
	}()
}

// SetCommitGenerator 注入 AI commit msg 的 LLM 缝。bridge 在 NewServer 时从
// model-relay provider 适配后调入。
func (s *Service) SetCommitGenerator(gen gitassist.Generator) {
	s.commitGen = gen
}

// Dispatch 处理一条 code_request，返回对应 code_response。pty.open 会用 emit
// 把后续输出/退出帧写回连接 —— git/fs 不用 emit。
func (s *Service) Dispatch(ctx context.Context, req *sdkproto.CodeRequest, emit PtyEmitter) *sdkproto.CodeResponse {
	switch req.Method {
	case "git.status":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.Status(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "git.statusFiles":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.StatusFiles(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Files []git.FileChange `json:"files"`
		}{Files: res})

	case "git.listBranches":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.ListBranches(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Branches []git.Branch `json:"branches"`
		}{Branches: res})

	case "git.createBranch":
		var p struct {
			Cwd      string `json:"cwd"`
			Name     string `json:"name"`
			Checkout bool   `json:"checkout"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.CreateBranch(ctx, p.Cwd, p.Name, p.Checkout); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.checkoutBranch":
		var p struct {
			Cwd  string `json:"cwd"`
			Name string `json:"name"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.CheckoutBranch(ctx, p.Cwd, p.Name); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.deleteBranch":
		var p struct {
			Cwd   string `json:"cwd"`
			Name  string `json:"name"`
			Force bool   `json:"force"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.DeleteBranch(ctx, p.Cwd, p.Name, p.Force); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.log":
		var p struct {
			Cwd   string `json:"cwd"`
			Limit int    `json:"limit"`
			Skip  int    `json:"skip"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.Log(ctx, p.Cwd, p.Limit, p.Skip)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Commits []git.Commit `json:"commits"`
		}{Commits: res})

	case "git.commitDetail":
		var p struct {
			Cwd  string `json:"cwd"`
			Hash string `json:"hash"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.CommitDetailOf(ctx, p.Cwd, p.Hash)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "git.showDiff":
		var p struct {
			Cwd  string `json:"cwd"`
			Hash string `json:"hash"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.ShowDiff(ctx, p.Cwd, p.Hash)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Diff string `json:"diff"`
		}{Diff: res})

	case "git.showFileDiff":
		var p struct {
			Cwd  string `json:"cwd"`
			Hash string `json:"hash"`
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.ShowFileDiff(ctx, p.Cwd, p.Hash, p.Path)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Diff string `json:"diff"`
		}{Diff: res})

	case "git.fileDiff":
		var p struct {
			Cwd    string `json:"cwd"`
			Path   string `json:"path"`
			Staged bool   `json:"staged"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.FileDiff(ctx, p.Cwd, p.Path, p.Staged)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Diff string `json:"diff"`
		}{Diff: res})

	case "git.stage":
		var p struct {
			Cwd   string   `json:"cwd"`
			Paths []string `json:"paths"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.Stage(ctx, p.Cwd, p.Paths); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.unstage":
		var p struct {
			Cwd   string   `json:"cwd"`
			Paths []string `json:"paths"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.Unstage(ctx, p.Cwd, p.Paths); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.stageAll":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.StageAll(ctx, p.Cwd); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.unstageAll":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.UnstageAll(ctx, p.Cwd); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.commit":
		var p struct {
			Cwd     string `json:"cwd"`
			Message string `json:"message"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		out, err := git.CommitChanges(ctx, p.Cwd, p.Message)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Output string `json:"output"`
		}{Output: out})

	case "git.discardFile":
		var p struct {
			Cwd       string `json:"cwd"`
			Path      string `json:"path"`
			Untracked bool   `json:"untracked"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.DiscardFile(ctx, p.Cwd, p.Path, p.Untracked); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.discardAll":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.DiscardAll(ctx, p.Cwd); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.push":
		var p struct {
			Cwd    string `json:"cwd"`
			Branch string `json:"branch"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		out, err := git.Push(ctx, p.Cwd, p.Branch)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Output string `json:"output"`
		}{Output: out})

	case "git.pull":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		out, err := git.Pull(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Output string `json:"output"`
		}{Output: out})

	case "git.remoteCounts":
		var p struct {
			Cwd    string `json:"cwd"`
			Branch string `json:"branch"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.RemoteCountsOf(ctx, p.Cwd, p.Branch)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "git.generateCommitMessage":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if s.commitGen == nil {
			return errResp(req.RequestID, "code: AI commit message unavailable — daemon has no model-relay provider configured")
		}
		msg, err := git.GenerateCommitMessage(ctx, p.Cwd, s.commitGen)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Message string `json:"message"`
		}{Message: msg})

	case "agent.generateName":
		var p struct {
			Prompt string `json:"prompt"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if s.commitGen == nil {
			return errResp(req.RequestID, "code: AI task naming unavailable — daemon has no model-relay provider configured")
		}
		name, err := agent.GenerateName(ctx, s.commitGen, p.Prompt)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Name string `json:"name"`
		}{Name: name})

	case "git.createWorktree":
		var p struct {
			ProjectPath     string `json:"project_path"`
			WorktreePath    string `json:"worktree_path"`
			PreferredBranch string `json:"preferred_branch"`
			BaseRef         string `json:"base_ref"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.CreateWorktree(ctx, p.ProjectPath, p.WorktreePath, p.PreferredBranch, p.BaseRef)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "git.removeWorktree":
		var p struct {
			ProjectPath  string `json:"project_path"`
			WorktreePath string `json:"worktree_path"`
			Branch       string `json:"branch"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := git.RemoveWorktree(ctx, p.ProjectPath, p.WorktreePath, p.Branch); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "git.worktreeDiffStats":
		var p struct {
			WorktreePath string `json:"worktree_path"`
			BaseBranch   string `json:"base_branch"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.WorktreeDiffStatsOf(ctx, p.WorktreePath, p.BaseBranch)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "git.mergeWorktree":
		var p struct {
			ProjectPath  string `json:"project_path"`
			WorktreePath string `json:"worktree_path"`
			Branch       string `json:"branch"`
			BaseBranch   string `json:"base_branch"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		out, err := git.MergeWorktree(ctx, p.ProjectPath, p.WorktreePath, p.Branch, p.BaseBranch)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Output string `json:"output"`
		}{Output: out})

	case "git.repoRoot":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		root, err := git.RepoRoot(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Root string `json:"root"`
		}{Root: root})

	case "git.changedFiles":
		var p struct {
			Cwd     string `json:"cwd"`
			BaseRef string `json:"base_ref"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.ChangedFiles(ctx, p.Cwd, p.BaseRef)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Files []git.NameStatus `json:"files"`
		}{Files: res})

	case "git.listUntracked":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.ListUntracked(ctx, p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Files []string `json:"files"`
		}{Files: res})

	case "git.rangeFileDiff":
		var p struct {
			Cwd     string `json:"cwd"`
			BaseRef string `json:"base_ref"`
			Path    string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := git.RangeFileDiff(ctx, p.Cwd, p.BaseRef, p.Path)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Diff string `json:"diff"`
		}{Diff: res})

	case "fs.read":
		var p struct {
			Path     string `json:"path"`
			MaxBytes int    `json:"max_bytes"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := fs.Read(p.Path, p.MaxBytes)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "fs.list":
		var p struct {
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := fs.List(p.Path)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "fs.write":
		var p struct {
			Root    string `json:"root"`
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := fs.Write(p.Root, p.Path, p.Content); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "fs.writeBytes":
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
			Data string `json:"data"` // base64
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		raw, derr := base64.StdEncoding.DecodeString(p.Data)
		if derr != nil {
			return errResp(req.RequestID, "fs.writeBytes: invalid base64: "+derr.Error())
		}
		if err := fs.WriteBytes(p.Root, p.Path, raw); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "fs.createFile":
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := fs.CreateFile(p.Root, p.Path); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "fs.createDirectory":
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := fs.CreateDirectory(p.Root, p.Path); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "fs.delete":
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := fs.Delete(p.Root, p.Path); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "fs.imagePreview":
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := fs.ImagePreviewOf(p.Root, p.Path)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, res)

	case "fs.listProjectFiles":
		var p struct {
			Root string `json:"root"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := fs.ListProjectFiles(ctx, p.Root)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Files []string `json:"files"`
		}{Files: res})

	case "fs.search":
		var p struct {
			Root       string   `json:"root"`
			Query      string   `json:"query"`
			Extensions []string `json:"extensions"`
			Limit      int      `json:"limit"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		res, err := fs.SearchFiles(ctx, p.Root, p.Query, p.Extensions, p.Limit)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Results []fs.SearchResult `json:"results"`
		}{Results: res})

	case "usage.read":
		// Claude(订阅 5h/7d)+ Codex(app-server RPC)用量快照。按需、无缓存,
		// 任一源失败落 unavailable 不影响另一源。详见 code/usage 包。
		return okResp(req.RequestID, usage.Read(ctx))

	case "agent.detect":
		// 自动检测 claude/codex/biu 的二进制路径 + 版本(扫 PATH + 候选目录)。
		// 设置面板「自动检测」用。
		return okResp(req.RequestID, struct {
			Agents map[string]agent.DetectResult `json:"agents"`
		}{Agents: agent.DetectAll(ctx)})

	case "hooks.status":
		// 当前 hook 安装状态(node 路径 / 脚本 / claude+codex 是否已注入)。
		return okResp(req.RequestID, hooks.Status())

	case "hooks.readiness":
		// 每个 agent 的就绪态(node + 安装 + 版本门槛),供任务创建页/设置页提示。
		return okResp(req.RequestID, struct {
			Agents []hooks.AgentReadiness `json:"agents"`
		}{Agents: hooks.Readiness()})

	case "hooks.install":
		// 幂等安装:写脚本 + claude-settings.json + 注入 ~/.codex/config.toml marker 区。
		return okResp(req.RequestID, hooks.Install())

	case "hooks.uninstall":
		if err := hooks.Uninstall(); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			OK bool `json:"ok"`
		}{OK: true})

	case "config.read":
		// 读项目级配置 .biu/config.toml(缺失/坏 → 默认)。PERI-2。
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		cfg, _ := projcfg.Read(p.Cwd)
		return okResp(req.RequestID, cfg)

	case "config.write":
		var p struct {
			Cwd    string         `json:"cwd"`
			Config projcfg.Config `json:"config"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := projcfg.Write(p.Cwd, p.Config); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			OK bool `json:"ok"`
		}{OK: true})

	case "skills.listHub":
		// 列 ~/.biumind/skills 下可安装的 skill(PERI-3)。
		list, err := skillinstall.ListHub()
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Skills []skillinstall.HubSkill `json:"skills"`
		}{Skills: list})

	case "skills.installations":
		// 扫项目 .claude/skills + .codex/skills 反推已安装的 skill 及健康度。
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		ins, err := skillinstall.Installations(p.Cwd)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			Installations []skillinstall.Installation `json:"installations"`
		}{Installations: ins})

	case "skills.install":
		// 把 hub skill symlink 进项目 <agent>/skills/<name>。
		var p struct {
			Cwd   string `json:"cwd"`
			Name  string `json:"name"`
			Agent string `json:"agent"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := skillinstall.Install(p.Cwd, p.Name, p.Agent); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			OK bool `json:"ok"`
		}{OK: true})

	case "skills.uninstall":
		var p struct {
			Cwd   string `json:"cwd"`
			Name  string `json:"name"`
			Agent string `json:"agent"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := skillinstall.Uninstall(p.Cwd, p.Name, p.Agent); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			OK bool `json:"ok"`
		}{OK: true})

	case "code.runTask":
		return s.runTask(req, emit)

	case "pty.open":
		return s.ptyOpen(req, emit)

	case "pty.kill":
		var p struct {
			PtyID string `json:"pty_id"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		if err := s.pty.Kill(p.PtyID); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct{}{})

	case "pty.active":
		// 返回当前活跃 PTY 的 id 列表。
		// Flutter 启动时拉一次做对账：status=running/pending 但不在此集的任务
		// 标 interrupted —— 存盘 status 跨 daemon 重启会过时，活着的 PTY 才是真相。
		return okResp(req.RequestID, struct {
			PtyIDs []string `json:"pty_ids"`
		}{PtyIDs: s.pty.Active()})

	case "pty.replayLog":
		// 回放某任务落盘的 PTY 历史(base64,取尾部 ≤2MiB)。重开终端时喂进 xterm,
		// 让原始终端跨重启存活。无日志返回空。
		var p struct {
			PtyID string `json:"pty_id"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return errResp(req.RequestID, err.Error())
		}
		data, err := readPTYLog(p.PtyID)
		if err != nil {
			return errResp(req.RequestID, err.Error())
		}
		return okResp(req.RequestID, struct {
			DataB64 string `json:"data_b64"`
		}{DataB64: base64.StdEncoding.EncodeToString(data)})

	case "pty.reattach":
		return s.reattachTask(req, emit)

	default:
		return errResp(req.RequestID, fmt.Sprintf("code: unknown method %q", req.Method))
	}
}

// runTask 拉起一个外部编码 agent(claude/codex)在 PTY 里跑,pty_id = task_id
// (便于 resume + 启动对账)。biu agent 不走这(进程内 SDK Protocol)。
func (s *Service) runTask(req *sdkproto.CodeRequest, emit PtyEmitter) *sdkproto.CodeResponse {
	if emit == nil {
		return errResp(req.RequestID, "code: runTask requires a live connection")
	}
	var p struct {
		TaskID         string `json:"task_id"`
		AgentType      string `json:"agent_type"`
		PermissionMode string `json:"permission_mode"`
		Prompt         string `json:"prompt"`
		Model          string `json:"model"`
		Cwd            string `json:"cwd"`
		Cols           uint16 `json:"cols"`
		Rows           uint16 `json:"rows"`
		// G5 续跑:resume=true 且 session_id 非空 → 用 --resume 续上已有会话
		// (不重发 prompt),而非新建。仅 claude 走通(我们启动时生成并持有 id)。
		Resume    bool   `json:"resume"`
		SessionID string `json:"session_id"`
	}
	if err := unmarshalParams(req.Params, &p); err != nil {
		return errResp(req.RequestID, err.Error())
	}
	if p.TaskID == "" {
		return errResp(req.RequestID, "code: runTask requires task_id")
	}
	// 项目级 prompt 前缀(PERI-2):.biu/config.toml 的 [agent].prompt_prefix 拼到 prompt
	// 最前(后跟空行),让某仓库每个任务都带上固定约束。服务端施加,与附件注入(客户端)叠加。
	if p.Cwd != "" {
		if cfg, cerr := projcfg.Read(p.Cwd); cerr == nil && cfg.Agent.PromptPrefix != "" {
			p.Prompt = cfg.Agent.PromptPrefix + "\n\n" + p.Prompt
		}
	}
	path, err := s.detect(p.AgentType)
	if err != nil {
		return errResp(req.RequestID, err.Error())
	}
	// 续跑(G5):带已知 session id 用 --resume 续会话;否则新建。
	// claude≥2.1.87 新建时预生成 session id → --session-id,使会话 JSONL 路径确定,供
	// 会话发现 watcher 精确定位。不支持则空,回退落盘发现。
	resume := p.Resume && p.SessionID != ""
	sessionID := ""
	switch {
	case resume:
		sessionID = p.SessionID
	case agent.SupportsSessionID(p.AgentType, path):
		sessionID = agent.NewSessionID()
	}
	var spec agent.LaunchSpec
	if resume {
		spec, err = agent.BuildResume(p.AgentType, path, p.PermissionMode, sessionID, p.Model)
	} else {
		spec, err = agent.BuildLaunch(p.AgentType, path, p.PermissionMode, p.Prompt, sessionID, p.Model)
	}
	if err != nil {
		return errResp(req.RequestID, err.Error())
	}
	// claude 启动前把 workspace 标记为已信任,跳过 folder-trust 交互询问(用户在
	// BiuMind 里选定 workspace 即视为信任)。只跳信任、不动逐工具审批。失败仅记日志、
	// 不阻塞任务 —— 最坏退回 claude 自弹询问,与不做时等价。
	if agent.Type(p.AgentType) == agent.Claude && p.Cwd != "" {
		if terr := agent.EnsureClaudeTrust(p.Cwd); terr != nil {
			slog.Warn("code: ensure claude folder trust failed", "cwd", p.Cwd, "err", terr)
		}
	}
	// Hook 可用(node + 已安装 + 版本达标)→ 给进程注入 BIU_TASK_ID/EVENT_DIR/AGENT,
	// 让 biu-hook.mjs 把生命周期事件写进 ~/.biu/events/<task>/events.jsonl(PERI-1)。
	// Claude 还需 --settings 指向自有 hook 配置(不碰用户 ~/.claude/settings.json);
	// Codex 的 hook 在 ~/.codex/config.toml 已由启动期 Install 注入,只需环境变量。
	hookUsable := hooks.UsableFor(p.AgentType)
	eventsDir := ""
	if hookUsable {
		if d, derr := hooks.EventsDirFor(p.TaskID); derr == nil {
			eventsDir = d
			_ = os.RemoveAll(eventsDir) // 清同 task_id 陈旧事件,防 tail offset=0 重放
			spec.Env = append(spec.Env,
				"BIU_TASK_ID="+p.TaskID,
				"BIU_EVENT_DIR="+eventsDir,
				"BIU_AGENT="+p.AgentType,
			)
			if agent.Type(p.AgentType) == agent.Claude {
				if sp, serr := hooks.ClaudeSettingsPath(); serr == nil {
					// --settings 须在 positional prompt 之前 → 前插。
					spec.Args = append([]string{"--settings", sp}, spec.Args...)
				}
			}
		} else {
			hookUsable = false
		}
	}
	// 会话 watcher 的生命周期:进程退出(onExit)→ 取消 → WatchClaude 收尾 drain 后退出。
	// 先建好 cancel(在 onExit 闭包之前),消除「进程秒退于 watcher 启动前」的竞态:
	// 那种情况下 ctx 已 cancel,WatchClaude 一启动即收尾返回,不泄漏。
	watchCtx, stopWatcher := context.WithCancel(context.Background())
	// PTY 输出落盘:tee 每帧到 ~/.biumind/code/pty-logs/<task>.log,供 pty.replayLog
	// 重开回放(终端唯一视图持久化)。resume 续跑 append 保留前段,新跑 truncate。
	logw := newPTYLogWriter(p.TaskID, resume)
	chunkSink := func(id string, data []byte) {
		logw.write(data)
		emit.Chunk(id, data)
	}
	onExit := func(id string, code int, e error) {
		stopWatcher()
		logw.close()
		msg := ""
		if e != nil {
			msg = e.Error()
		}
		emit.Exit(id, code, msg)
	}
	openErr := s.pty.Open(p.TaskID, pty.OpenSpec{
		Cmd: spec.Path, Args: spec.Args, Cwd: p.Cwd, Env: spec.Env, Cols: p.Cols, Rows: p.Rows,
	}, chunkSink, onExit)
	if openErr != nil {
		stopWatcher()
		logw.close()
		return errResp(req.RequestID, openErr.Error())
	}

	// 起会话 watcher,事件按 task_id 推回。两条线:
	//   内容 watcher(text/tool/cost 结构化回放)—— Claude 用 --session-id 预算路径精确
	//     tail;Codex 在 hook 不可用时落盘发现 rollout(CORE-4),hook 可用时由 SessionStart
	//     的 transcript_path 启动(避免双开重复事件)。
	//   状态 watcher(hook)—— 可靠 running↔input_required + 精确会话发现(PERI-1)。
	// biu 内置 agent 不走这(进程内 SDK Protocol)。失败仅停 watcher,不影响任务运行。
	taskID := p.TaskID
	cwd := p.Cwd
	emitSession := func(events []map[string]any) {
		for _, ev := range events {
			emit.SessionEvent(taskID, ev)
		}
	}
	watchStarted := false

	switch {
	case sessionID != "":
		if sessFile, ferr := session.ClaudeSessionFile(cwd, sessionID); ferr == nil {
			go session.WatchClaude(watchCtx, sessFile, emitSession)
			watchStarted = true
		}
	case agent.Type(p.AgentType) == agent.Codex && !hookUsable:
		go session.WatchCodex(watchCtx, cwd, emitSession)
		watchStarted = true
	}

	if hookUsable {
		// Codex 内容路径来自 hook 的 transcript_path;Claude 内容已由上面的预算路径接管,
		// onSession 留空避免重复 tail。状态信号统一发 agent_status 帧。
		var onSession session.HookSessionFn
		if agent.Type(p.AgentType) == agent.Codex {
			onSession = func(_, transcriptPath string) {
				if transcriptPath != "" {
					go session.WatchCodexFile(watchCtx, transcriptPath, cwd, emitSession)
				}
			}
		}
		onStatus := func(status string) {
			emit.SessionEvent(taskID, map[string]any{"type": "agent_status", "status": status})
		}
		go session.WatchHookEvents(watchCtx, eventsDir, onStatus, onSession)
		watchStarted = true
	}

	if !watchStarted {
		stopWatcher()
	}

	// 把会话 id 回传客户端持久化(进 events,供 G5 续跑用 --resume)。claude 的 id
	// 启动时即确定;codex 的 rollout id 靠落盘发现,续跑暂未支持,故只发 claude。
	if agent.Type(p.AgentType) == agent.Claude && sessionID != "" {
		emit.SessionEvent(taskID, map[string]any{
			"type": "session_info", "agent": p.AgentType, "session_id": sessionID,
		})
	}

	return okResp(req.RequestID, struct {
		PtyID string `json:"pty_id"`
	}{PtyID: p.TaskID})
}

// reattachTask 把一个**仍在跑**的 PTY(daemon 幸存、客户端换了新连接,如热重启/崩溃
// 重连)的输出/退出重绑到当前连接 —— 不 spawn 新进程。
// 输入(pty.Input)按 id 写、与连接无关,故无需重绑。alive=false 表示该 PTY 已不在
// (进程已退),客户端应退回「恢复(--resume)」起新会话。
//
// 注:断线前 runTask 的 logw / 会话 watcher 仍以旧连接的 emit 运行(写已关闭的 WS,
// best-effort 忽略),其 ctx 无句柄可在此取消 → 残留到 daemon 退出。B 场景(仅 daemon
// 幸存时触发)罕见,可接受;真·全退出场景 daemon 已被 kill,不走这条路。
func (s *Service) reattachTask(req *sdkproto.CodeRequest, emit PtyEmitter) *sdkproto.CodeResponse {
	if emit == nil {
		return errResp(req.RequestID, "code: reattachTask requires a live connection")
	}
	var p struct {
		TaskID    string `json:"task_id"`
		AgentType string `json:"agent_type"`
		Cwd       string `json:"cwd"`
		SessionID string `json:"session_id"`
	}
	if err := unmarshalParams(req.Params, &p); err != nil {
		return errResp(req.RequestID, err.Error())
	}

	// 续写落盘日志(append 保留断线前历史),会话 watcher 推回当前连接。
	logw := newPTYLogWriter(p.TaskID, true)
	watchCtx, stopWatcher := context.WithCancel(context.Background())
	chunkSink := func(id string, data []byte) {
		logw.write(data)
		emit.Chunk(id, data)
	}
	onExit := func(id string, code int, e error) {
		stopWatcher()
		logw.close()
		msg := ""
		if e != nil {
			msg = e.Error()
		}
		emit.Exit(id, code, msg)
	}

	if !s.pty.Reattach(p.TaskID, chunkSink, onExit) {
		stopWatcher()
		logw.close()
		return okResp(req.RequestID, struct {
			Alive bool `json:"alive"`
		}{Alive: false})
	}

	// 重挂 Claude 内容 watcher(结构化事件推回新连接);codex 续 watcher 暂略。
	taskID := p.TaskID
	emitSession := func(events []map[string]any) {
		for _, ev := range events {
			emit.SessionEvent(taskID, ev)
		}
	}
	watchStarted := false
	if p.SessionID != "" {
		if sessFile, ferr := session.ClaudeSessionFile(p.Cwd, p.SessionID); ferr == nil {
			go session.WatchClaude(watchCtx, sessFile, emitSession)
			watchStarted = true
		}
		// 回传 session_info,供续跑用 --resume。
		emit.SessionEvent(taskID, map[string]any{
			"type": "session_info", "agent": p.AgentType, "session_id": p.SessionID,
		})
	}
	if !watchStarted {
		stopWatcher()
	}

	return okResp(req.RequestID, struct {
		Alive bool   `json:"alive"`
		PtyID string `json:"pty_id"`
	}{Alive: true, PtyID: p.TaskID})
}

func (s *Service) ptyOpen(req *sdkproto.CodeRequest, emit PtyEmitter) *sdkproto.CodeResponse {
	if emit == nil {
		return errResp(req.RequestID, "code: pty.open requires a live connection")
	}
	var p struct {
		PtyID string   `json:"pty_id"`
		Cmd   string   `json:"cmd"`
		Args  []string `json:"args"`
		Cwd   string   `json:"cwd"`
		Env   []string `json:"env"`
		Cols  uint16   `json:"cols"`
		Rows  uint16   `json:"rows"`
	}
	if err := unmarshalParams(req.Params, &p); err != nil {
		return errResp(req.RequestID, err.Error())
	}
	if p.Cmd == "" {
		return errResp(req.RequestID, "code: pty.open requires cmd")
	}
	ptyID := p.PtyID
	if ptyID == "" {
		ptyID = newID()
	}
	spec := pty.OpenSpec{
		Cmd: p.Cmd, Args: p.Args, Cwd: p.Cwd, Env: p.Env, Cols: p.Cols, Rows: p.Rows,
	}
	onChunk := func(id string, data []byte) { emit.Chunk(id, data) }
	onExit := func(id string, code int, err error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		emit.Exit(id, code, msg)
	}
	if err := s.pty.Open(ptyID, spec, onChunk, onExit); err != nil {
		return errResp(req.RequestID, err.Error())
	}
	return okResp(req.RequestID, struct {
		PtyID string `json:"pty_id"`
	}{PtyID: ptyID})
}

// Input 把字节写入指定 PTY（来自 code_pty_input 帧）。
func (s *Service) Input(ptyID string, data []byte) error {
	return s.pty.Input(ptyID, data)
}

// Resize 调整指定 PTY 尺寸（来自 code_pty_resize 帧）。
func (s *Service) Resize(ptyID string, cols, rows uint16) error {
	return s.pty.Resize(ptyID, cols, rows)
}

// CloseAll 杀掉所有 PTY —— daemon 关停时调用。
func (s *Service) CloseAll() {
	s.pty.CloseAll()
}

// ─── helpers ─────────────────────────────────────────────

func unmarshalParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil // 无参数：留 v 为零值
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("code: bad params: %w", err)
	}
	return nil
}

func okResp(reqID string, result any) *sdkproto.CodeResponse {
	raw, err := json.Marshal(result)
	if err != nil {
		return errResp(reqID, "code: marshal result: "+err.Error())
	}
	return &sdkproto.CodeResponse{
		Type:      sdkproto.TypeCodeResponse,
		RequestID: reqID,
		OK:        true,
		Result:    raw,
	}
}

func errResp(reqID, msg string) *sdkproto.CodeResponse {
	return &sdkproto.CodeResponse{
		Type:      sdkproto.TypeCodeResponse,
		RequestID: reqID,
		OK:        false,
		Error:     msg,
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
