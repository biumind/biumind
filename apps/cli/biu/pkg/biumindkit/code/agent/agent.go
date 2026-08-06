// Package agent 构造外部编码 agent(Claude Code / Codex)的启动规格 —— 二进制
// 路径探测 + 权限模式到 CLI flag 的映射。
//
// biu agent 不在此:它进程内走 SDK Protocol、不 spawn(设计文档 §4.2),故只处理
// claude / codex 两种外部 binary。
//
// ⚠️ 路径探测:完整版含大量 Windows npm-vendor 边角(codex 内置二进制
// 解析)。本实现面向 macOS/Linux 桌面:LookPath 优先 + 常见目录兜底。Windows
// ConPTY/vendor 留后(与 pty 包的 Windows 注记一致)。
package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Type 是支持的外部 agent 种类。
type Type string

const (
	Claude Type = "claude"
	Codex  Type = "codex"
)

// LaunchSpec 是拉起 agent 的完整规格。Path 是二进制绝对路径;Args 是其后参数;
// Env 是要追加的环境变量(当前为空,预留 codex vendor PATH 等)。
type LaunchSpec struct {
	Path string
	Args []string
	Env  []string
}

// binaryName 返回某 agent 的可执行名。未知 agent 返回空。
func binaryName(agentType string) string {
	switch Type(agentType) {
	case Claude:
		return "claude"
	case Codex:
		return "codex"
	default:
		return ""
	}
}

// DetectPath 解析 agent 二进制路径:先 exec.LookPath(走 daemon 继承的 login-shell
// PATH),再扫常见安装位置。找不到返回 error(调用方应把错误透传给用户,让其装/配)。
func DetectPath(agentType string) (string, error) {
	bin := binaryName(agentType)
	if bin == "" {
		return "", fmt.Errorf("agent: unknown agent type %q", agentType)
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p, nil
	}
	for _, dir := range candidateDirs() {
		cand := filepath.Join(dir, bin)
		if isExecutableFile(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("agent: %q not found in PATH or common locations (~/.local/bin, homebrew, ~/.bun/bin, nvm)", bin)
}

// candidateDirs 是 LookPath 失败后的兜底搜索目录(macOS/Linux 常见位置)。
func candidateDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".deno", "bin"),
		)
		// nvm: ~/.nvm/versions/node/*/bin
		if matches, err := filepath.Glob(
			filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); err == nil {
			dirs = append(dirs, matches...)
		}
	}
	dirs = append(dirs,
		"/opt/homebrew/bin", // Apple Silicon brew
		"/usr/local/bin",    // Intel brew / 通用
		"/usr/bin",
	)
	return dirs
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// BuildLaunch 组装 LaunchSpec。path 来自 DetectPath;mode ∈ ask|auto_edit|full_access;
// prompt 空 → 不传 positional(进交互式 REPL)。flag 映射 1:1 。
//
// sessionID:非空且为 claude 时加 --session-id,使会话 JSONL 路径确定
// (~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl),供 M3 会话发现 watcher 精确
// 定位、跳过 /status 轮询发现。调用方负责仅在版本支持时(见 SupportsSessionID)生成
// uuid 传入;codex 忽略 sessionID(用落盘 rollout 文件发现定位会话)。
// model:非空时加 --model <id>(claude 接 Anthropic 系、codex 接 OpenAI 系);空 →
// 不传,回退 CLI 自有配置。由调用方/用户选择,本函数不校验命名空间。
func BuildLaunch(agentType, path, mode, prompt, sessionID, model string) (LaunchSpec, error) {
	switch Type(agentType) {
	case Claude:
		return buildClaude(path, mode, prompt, sessionID, model), nil
	case Codex:
		return buildCodex(path, mode, prompt, model), nil
	default:
		return LaunchSpec{}, fmt.Errorf("agent: %q not launchable via PTY (biu runs in-process)", agentType)
	}
}

// buildClaude:ask→--permission-mode default;auto_edit→acceptEdits;
// full_access→--dangerously-skip-permissions。sessionID 非空 → --session-id。
// prompt 作 positional。
func buildClaude(path, mode, prompt, sessionID, model string) LaunchSpec {
	args := claudePermArgs(mode)
	if model != "" {
		args = append(args, "--model", model)
	}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return LaunchSpec{Path: path, Args: args}
}

// claudePermArgs 把权限档映射成 Claude flag(launch / resume 共用)。
// 未知档不加 flag(保持与原 buildClaude 一致)。
func claudePermArgs(mode string) []string {
	switch mode {
	case "ask":
		return []string{"--permission-mode", "default"}
	case "auto_edit":
		return []string{"--permission-mode", "acceptEdits"}
	case "full_access":
		return []string{"--dangerously-skip-permissions"}
	default:
		return nil
	}
}

// BuildResume 组装「续跑已有会话」的 LaunchSpec。
// claude: --resume <sessionID>;codex: 权限 flag 后接 `resume <sessionID>`。
// 都不带 prompt —— resume 由 agent 读自身会话 JSONL 恢复上下文,不重发 prompt。
// 调用方仅在已持有该任务的 sessionID 时调用(claude 用启动时生成的 id)。
func BuildResume(agentType, path, mode, sessionID, model string) (LaunchSpec, error) {
	switch Type(agentType) {
	case Claude:
		return buildClaudeResume(path, mode, sessionID, model), nil
	case Codex:
		return buildCodexResume(path, mode, sessionID, model), nil
	default:
		return LaunchSpec{}, fmt.Errorf("agent: %q not resumable via PTY", agentType)
	}
}

func buildClaudeResume(path, mode, sessionID, model string) LaunchSpec {
	args := claudePermArgs(mode)
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "--resume", sessionID)
	return LaunchSpec{Path: path, Args: args}
}

func buildCodexResume(path, mode, sessionID, model string) LaunchSpec {
	args := codexPermArgs(mode)
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "resume", sessionID)
	return LaunchSpec{Path: path, Args: args}
}

// NewSessionID 生成一个 v4 UUID 作 claude --session-id。
func NewSessionID() string {
	return uuid.NewString()
}

// claudeSessionMinVersion 是支持 --session-id 的最低 Claude 版本。
var claudeSessionMinVersion = [3]int{2, 1, 87}

var (
	verCacheMu sync.Mutex
	verCache   = map[string][3]int{}
)

// SupportsSessionID 报告该 agent 是否支持 --session-id 精确会话定位。
// 仅 Claude ≥2.1.87 为真;codex 恒 false(用落盘发现)。带缓存,未命中时跑
// `path --version`(子进程,调用方应在非热路径调用)。探测失败 → 保守返回 false
// (回退到 codex 同款的落盘发现,不阻塞任务)。
func SupportsSessionID(agentType, path string) bool {
	if Type(agentType) != Claude {
		return false
	}
	v, ok := claudeVersion(path)
	if !ok {
		return false
	}
	return versionGTE(v, claudeSessionMinVersion)
}

func claudeVersion(path string) ([3]int, bool) {
	return probeSemver(path)
}

// DetectedVersion 跑 `<path> --version` 抽 major.minor.patch(带缓存)。claude/codex
// 同款前导 semver 输出。供 hooks 包做版本门槛判定(Claude≥2.1.87 / Codex≥0.131.0)。
func DetectedVersion(path string) ([3]int, bool) {
	return probeSemver(path)
}

// VersionGTE 报告 path 二进制版本是否 ≥ min。探测失败 → false(保守:回退轮询路径)。
func VersionGTE(path string, min [3]int) bool {
	v, ok := probeSemver(path)
	return ok && versionGTE(v, min)
}

func probeSemver(path string) ([3]int, bool) {
	verCacheMu.Lock()
	if v, ok := verCache[path]; ok {
		verCacheMu.Unlock()
		return v, true
	}
	verCacheMu.Unlock()

	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return [3]int{}, false
	}
	v, ok := parseLeadingSemver(string(out))
	if ok {
		verCacheMu.Lock()
		verCache[path] = v
		verCacheMu.Unlock()
	}
	return v, ok
}

// parseLeadingSemver 从 "2.1.185 (Claude Code)\n" 这类输出里抽前导 major.minor.patch。
func parseLeadingSemver(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	var v [3]int
	idx := 0
	for part := 0; part < 3; part++ {
		start := idx
		for idx < len(s) && s[idx] >= '0' && s[idx] <= '9' {
			idx++
		}
		if idx == start {
			return v, false // 该段无数字
		}
		n := 0
		for _, c := range s[start:idx] {
			n = n*10 + int(c-'0')
		}
		v[part] = n
		if part < 2 {
			if idx >= len(s) || s[idx] != '.' {
				return v, false // 缺 '.' 分隔
			}
			idx++ // 跳过 '.'
		}
	}
	return v, true
}

func versionGTE(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true // 相等
}

// buildCodex:auto_edit→--sandbox workspace-write -a on-request;
// full_access→--dangerously-bypass-approvals-and-sandbox。prompt 在 `--` 后。
func buildCodex(path, mode, prompt, model string) LaunchSpec {
	args := codexPermArgs(mode)
	// --model 须在 `--`(分隔 positional prompt)之前。
	if model != "" {
		args = append(args, "--model", model)
	}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	return LaunchSpec{Path: path, Args: args}
}

// codexPermArgs 把权限档映射成 Codex flag(launch / resume 共用)。
func codexPermArgs(mode string) []string {
	switch mode {
	case "auto_edit":
		return []string{"--sandbox", "workspace-write", "-a", "on-request"}
	case "full_access":
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	default:
		return nil
	}
}
