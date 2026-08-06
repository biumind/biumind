// Package hooks 注入/卸载 BiuMind 的 agent 生命周期 hook —— 让 Claude/Codex 主动上报
// SessionStart / Notification|PermissionRequest / Stop / PostToolUse / UserPromptSubmit,
// 取代纯 JSONL 轮询反推任务状态。
//
// 机制(看代码看不出、易踩雷):
//   - 共享脚本 ~/.biu/hooks/biu-hook.mjs(embed 进 biu 二进制,启动期落盘)。agent 每个
//     生命周期点用 stdin 喂它 JSON,脚本归一化后追加一行到 ~/.biu/events/<task>/events.jsonl。
//     脚本靠 BIU_TASK_ID+BIU_EVENT_DIR 守卫:用户手动跑 agent 时这俩不存在 → 立即 exit 0,
//     零副作用。
//   - Claude:写自有 ~/.biu/hooks/claude-settings.json,启动时 `--settings <该文件>` 传入,
//     完全不碰用户 ~/.claude/settings.json。
//   - Codex:无 --settings 等价物,只能注入用户全局 ~/.codex/config.toml,用 marker 注释
//     区域包裹(卸载即删该区,区域外用户内容按字符串切片完整保留)。
//   - 版本门槛:Claude≥2.1.87(--settings hook 可信)、Codex≥0.131.0(--dangerously-bypass-
//     hook-trust)。不足 → UsableFor 返回 false,调用方回退现有轮询 watcher。
package hooks

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/agent"
)

//go:embed biu-hook.mjs
var hookScript string

// 版本门槛。
var (
	claudeHookMin = [3]int{2, 1, 87}
	codexHookMin  = [3]int{0, 131, 0}
)

const (
	codexBegin = "# >>> biu-managed-begin (do not edit; managed by BiuMind) >>>"
	codexEnd   = "# <<< biu-managed-end <<<"
)

// claudeEvents / codexEvents 是各 agent 要挂的生命周期事件。
var (
	claudeEvents = []string{"SessionStart", "UserPromptSubmit", "Notification", "PostToolUse", "Stop", "SubagentStop"}
	codexEvents  = []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "PostToolUse", "Stop", "SubagentStop"}
)

// InstallStatus 是安装结果 / 当前状态(展示给 UI)。
type InstallStatus struct {
	NodePath        string `json:"node_path"`
	ScriptPath      string `json:"script_path"`
	ClaudeInstalled bool   `json:"claude_installed"`
	CodexInstalled  bool   `json:"codex_installed"`
	Error           string `json:"error,omitempty"`
}

// AgentReadiness 是单 agent 的 hook 就绪状态(供任务创建页 / 设置页展示)。
type AgentReadiness struct {
	Agent           string `json:"agent"`
	Usable          bool   `json:"usable"`
	Reason          string `json:"reason"` // ok | no_node | not_installed | version_too_low | not_found
	DetectedVersion string `json:"detected_version"`
	MinVersion      string `json:"min_version"`
}

// ─── 路径 ─────────────────────────────────────────────────────────────────────

func home() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("hooks: cannot find home dir: %w", err)
	}
	return h, nil
}

// HooksDir 返回 ~/.biu/hooks。
func HooksDir() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".biu", "hooks"), nil
}

// ScriptPath 返回 hook 脚本落盘路径 ~/.biu/hooks/biu-hook.mjs。
func ScriptPath() (string, error) {
	d, err := HooksDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "biu-hook.mjs"), nil
}

// EventsRoot 返回 ~/.biu/events。
func EventsRoot() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".biu", "events"), nil
}

// EventsDirFor 返回某任务的事件目录 ~/.biu/events/<taskID>。
func EventsDirFor(taskID string) (string, error) {
	root, err := EventsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, taskID), nil
}

func claudeSettingsPath() (string, error) {
	d, err := HooksDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "claude-settings.json"), nil
}

// ClaudeSettingsPath 暴露自有 Claude settings 文件路径,供 runTask 在版本支持时
// `--settings <此路径>` 传入。
func ClaudeSettingsPath() (string, error) { return claudeSettingsPath() }

func codexConfigPath() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".codex", "config.toml"), nil
}

// ─── node 检测 ─────────────────────────────────────────────────────────────────

// DetectNode 检测可用 node 解释器路径(走 daemon 继承的 login-shell PATH),
// 失败返回 ""。Unix 上 realpath 解析绕开 nvm/asdf shim。
func DetectNode() string {
	raw, err := exec.LookPath("node")
	if err != nil || raw == "" {
		return ""
	}
	if real, rerr := filepath.EvalSymlinks(raw); rerr == nil {
		return real
	}
	return raw
}

// ─── 脚本 + Claude settings 生成 ───────────────────────────────────────────────

func atomicWrite(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeHookScript 把 embed 的 biu-hook.mjs 落盘到 ~/.biu/hooks。
func writeHookScript() (string, error) {
	p, err := ScriptPath()
	if err != nil {
		return "", err
	}
	if err := atomicWrite(p, hookScript); err != nil {
		return "", fmt.Errorf("hooks: write script: %w", err)
	}
	return p, nil
}

// hookCommand 构造跨 shell 安全的调用命令:裸 `node "<script>"`(首 token 是裸 node,
// cmd.exe/PowerShell/sh 都解析成「调 PATH 上的 node」;脚本路径双引号容纳空格)。
func hookCommand(script string) string {
	return fmt.Sprintf("node %q", script)
}

// buildClaudeSettings 构造 Claude settings JSON(仅 hooks 字段;force_default_tui 是 app
// 级设置,不在此)。Claude 跨来源 merge hooks 并按 command 去重,不覆盖用户配置。
func buildClaudeSettings(script string) ([]byte, error) {
	entry := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": hookCommand(script)}},
	}
	hooksMap := map[string]any{}
	for _, ev := range claudeEvents {
		hooksMap[ev] = []any{entry}
	}
	return json.MarshalIndent(map[string]any{"hooks": hooksMap}, "", "  ")
}

func writeClaudeSettings(script string) error {
	p, err := claudeSettingsPath()
	if err != nil {
		return err
	}
	raw, err := buildClaudeSettings(script)
	if err != nil {
		return err
	}
	return atomicWrite(p, string(raw))
}

// ─── Codex config.toml marker 注入/卸载 ────────────────────────────────────────

func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04X`, c))
			} else {
				b.WriteRune(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func buildCodexBlock(script string) string {
	var b strings.Builder
	b.WriteString(codexBegin)
	b.WriteByte('\n')
	for _, ev := range codexEvents {
		fmt.Fprintf(&b, "[[hooks.%s]]\n", ev)
		fmt.Fprintf(&b, "[[hooks.%s.hooks]]\n", ev)
		b.WriteString("type = \"command\"\n")
		fmt.Fprintf(&b, "command = %s\n", tomlQuote(hookCommand(script)))
		b.WriteByte('\n')
	}
	b.WriteString(codexEnd)
	b.WriteByte('\n')
	return b.String()
}

// injectCodexText 把 biu 块写入(或更新)既有 TOML 文本。已有 marker → 整段替换;
// 否则追加文件末尾。区域外用户内容按字符串切片完整保留。
func injectCodexText(existing, script string) string {
	block := buildCodexBlock(script)
	begin := strings.Index(existing, codexBegin)
	end := strings.Index(existing, codexEnd)
	if begin >= 0 && end >= 0 && begin < end {
		endLineEnd := end + len(codexEnd)
		if nl := strings.IndexByte(existing[end:], '\n'); nl >= 0 {
			endLineEnd = end + nl + 1
		} else {
			endLineEnd = len(existing)
		}
		before := existing[:begin]
		after := existing[endLineEnd:]
		var b strings.Builder
		b.WriteString(before)
		if before != "" && !strings.HasSuffix(before, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(block)
		if after != "" && !strings.HasPrefix(after, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(after)
		return b.String()
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteByte('\n')
	}
	if existing != "" {
		b.WriteByte('\n')
	}
	b.WriteString(block)
	return b.String()
}

// uninjectCodexText 从 TOML 文本移除 biu 块。无 marker → 原样返回。
func uninjectCodexText(existing string) string {
	begin := strings.Index(existing, codexBegin)
	end := strings.Index(existing, codexEnd)
	if begin < 0 || end < 0 || begin >= end {
		return existing
	}
	endLineEnd := len(existing)
	if nl := strings.IndexByte(existing[end:], '\n'); nl >= 0 {
		endLineEnd = end + nl + 1
	}
	before := existing[:begin]
	after := existing[endLineEnd:]
	out := before
	for strings.HasSuffix(out, "\n\n") {
		out = out[:len(out)-1]
	}
	if after != "" {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += strings.TrimLeft(after, "\n")
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
	} else if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func injectCodexConfig(script string) error {
	p, err := codexConfigPath()
	if err != nil {
		return err
	}
	existing := ""
	if data, rerr := os.ReadFile(p); rerr == nil {
		existing = string(data)
	} else if !os.IsNotExist(rerr) {
		return rerr
	}
	updated := injectCodexText(existing, script)
	// 校验注入后仍是合法 TOML,避免写坏用户配置。
	var probe map[string]any
	if err := toml.Unmarshal([]byte(updated), &probe); err != nil {
		return fmt.Errorf("hooks: biu-injected TOML invalid: %w", err)
	}
	return atomicWrite(p, updated)
}

func uninjectCodexConfig() error {
	p, err := codexConfigPath()
	if err != nil {
		return err
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil
		}
		return rerr
	}
	updated := uninjectCodexText(string(data))
	if updated == string(data) {
		return nil
	}
	return atomicWrite(p, updated)
}

// ─── 安装状态查询 ───────────────────────────────────────────────────────────────

func claudeSettingsHasHooks() bool {
	p, err := claudeSettingsPath()
	if err != nil {
		return false
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		return false
	}
	var v struct {
		Hooks map[string]any `json:"hooks"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return len(v.Hooks) > 0
}

func codexConfigHasBiu() bool {
	p, err := codexConfigPath()
	if err != nil {
		return false
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, codexBegin) && strings.Contains(s, codexEnd)
}

// ─── 对外入口 ───────────────────────────────────────────────────────────────────

// Install 一次性安装(幂等)。失败不阻塞,返回带 Error 的状态。
func Install() InstallStatus {
	var st InstallStatus
	node := DetectNode()
	if node == "" {
		st.Error = "node not found in PATH"
		return st
	}
	st.NodePath = node

	script, err := writeHookScript()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.ScriptPath = script

	// 仅给机器上实际存在的 agent 注入,避免为没装的工具凭空创建配置文件
	// (尤其 Codex 注入的是用户全局 ~/.codex/config.toml)。
	if _, derr := agent.DetectPath("claude"); derr == nil {
		if err := writeClaudeSettings(script); err != nil {
			st.Error = fmt.Sprintf("claude settings: %v", err)
		} else {
			st.ClaudeInstalled = true
		}
	}
	if _, derr := agent.DetectPath("codex"); derr == nil {
		if err := injectCodexConfig(script); err != nil {
			if st.Error == "" {
				st.Error = fmt.Sprintf("codex config: %v", err)
			} else {
				st.Error = fmt.Sprintf("%s; codex config: %v", st.Error, err)
			}
		} else {
			st.CodexInstalled = true
		}
	}
	return st
}

// Uninstall 卸载注入(不删脚本本身):删 claude-settings.json + 移除 codex config.toml 的 biu 区。
func Uninstall() error {
	if p, err := claudeSettingsPath(); err == nil {
		if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
			return rerr
		}
	}
	return uninjectCodexConfig()
}

// Status 报告当前安装状态(供 UI)。
func Status() InstallStatus {
	st := InstallStatus{NodePath: DetectNode()}
	if p, err := ScriptPath(); err == nil {
		if _, serr := os.Stat(p); serr == nil {
			st.ScriptPath = p
		}
	}
	st.ClaudeInstalled = claudeSettingsHasHooks()
	st.CodexInstalled = codexConfigHasBiu()
	return st
}

// Readiness 返回 claude / codex 两个 agent 的 hook 就绪状态(node + 安装 + 版本门槛)。
func Readiness() []AgentReadiness {
	st := Status()
	return []AgentReadiness{
		readinessFor("claude", st),
		readinessFor("codex", st),
	}
}

func minVersionStr(v [3]int) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func readinessFor(agentType string, st InstallStatus) AgentReadiness {
	var installed bool
	var min [3]int
	if agentType == "codex" {
		installed, min = st.CodexInstalled, codexHookMin
	} else {
		installed, min = st.ClaudeInstalled, claudeHookMin
	}
	r := AgentReadiness{Agent: agentType, MinVersion: minVersionStr(min)}

	path, perr := agent.DetectPath(agentType)
	if perr != nil {
		r.Reason = "not_found"
		return r
	}
	if v, ok := agent.DetectedVersion(path); ok {
		r.DetectedVersion = minVersionStr(v)
	}
	versionOK := agent.VersionGTE(path, min)

	switch {
	case st.NodePath == "":
		r.Reason = "no_node"
	case !installed:
		r.Reason = "not_installed"
	case !versionOK:
		r.Reason = "version_too_low"
	default:
		r.Reason = "ok"
		r.Usable = true
	}
	return r
}

// UsableFor 判定某 agent 的 hook 链路是否可信、可替代轮询:node 可用 + 已安装 + 版本达标。
// runTask 在启动任务时调用决定走 hook watcher 还是回退轮询。任一不满足 → false。
func UsableFor(agentType string) bool {
	if DetectNode() == "" {
		return false
	}
	var installed bool
	var min [3]int
	if agentType == "codex" {
		installed, min = codexConfigHasBiu(), codexHookMin
	} else {
		installed, min = claudeSettingsHasHooks(), claudeHookMin
	}
	if !installed {
		return false
	}
	path, err := agent.DetectPath(agentType)
	if err != nil {
		return false
	}
	return agent.VersionGTE(path, min)
}
