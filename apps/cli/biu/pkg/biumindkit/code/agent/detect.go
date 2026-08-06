// agent 自动检测。给设置面板「自动检测」用:扫 PATH +
// 常见安装位置(复用 DetectPath 的候选目录),并跑 `--version` 拿版本串。
//
// 与逐字段 Test(Flutter 侧只解析用户已填路径)的区别:这里**主动扫描**
// nvm/brew/.local/bin 等位置,帮用户找到没填的 binary。检测逻辑落在 biu CLI
// (设计文档 §2.2「路径检测 → biu CLI」),桌面 + 远控共享同一份。

package agent

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectResult 是单个 agent 的检测结果。Found=false 表示 PATH 与候选位置都没找到;
// Version 为空表示找到了 binary 但 `--version` 探测失败(不影响 Found)。
type DetectResult struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Found   bool   `json:"found"`
}

// detectBinaryName 比 launch 用的 binaryName 多认 biu —— biu 仅用于检测/展示,
// 不经 PTY 启动(BuildLaunch 仍拒 biu)。
func detectBinaryName(agentType string) string {
	switch Type(agentType) {
	case Claude:
		return "claude"
	case Codex:
		return "codex"
	case "biu":
		return "biu"
	default:
		return ""
	}
}

// Detect 解析某 agent 的二进制路径 + 版本。agentType ∈ claude|codex|biu。
func Detect(ctx context.Context, agentType string) DetectResult {
	bin := detectBinaryName(agentType)
	if bin == "" {
		return DetectResult{}
	}
	path := resolveBinaryPath(bin)
	if path == "" {
		return DetectResult{Found: false}
	}
	return DetectResult{
		Path:    path,
		Version: probeVersion(ctx, agentType, path),
		Found:   true,
	}
}

// DetectAll 一次检测 claude / codex / biu 三种。
func DetectAll(ctx context.Context) map[string]DetectResult {
	return map[string]DetectResult{
		"claude": Detect(ctx, "claude"),
		"codex":  Detect(ctx, "codex"),
		"biu":    Detect(ctx, "biu"),
	}
}

// resolveBinaryPath 先 LookPath(走 daemon 继承的 login-shell PATH),再扫候选目录。
func resolveBinaryPath(bin string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	for _, dir := range candidateDirs() {
		cand := filepath.Join(dir, bin)
		if isExecutableFile(cand) {
			return cand
		}
	}
	return ""
}

// probeVersion 跑 `<path> --version`(claude/codex)或 `<path> version`(biu),
// 返回 trimmed 首行;失败返回 ""。带 ctx 超时,避免卡死设置面板。
func probeVersion(ctx context.Context, agentType, path string) string {
	arg := "--version"
	if Type(agentType) == "biu" {
		arg = "version"
	}
	out, err := exec.CommandContext(ctx, path, arg).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line
}
