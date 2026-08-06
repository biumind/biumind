// Backend config — Runtime v3 Q3: 把外部 CLI（Claude Code / Codex）当 agent
// backend。BackendConfig 描述一个 CLI 怎么 spawn + 凭据隔离策略。对位 openclaw
// `src/agents/cli-backends.ts`（仅参考思路，未 fork）。
//
// R3 接线 Claude Code（D3 stream-json）；R8 接线 Codex（codex exec --json）。

package agent

import "os"

// BackendConfig 描述一个外部 CLI agent backend。
type BackendConfig struct {
	// ID 是 backend 标识（"claude-cli" | "codex-cli"），对应 WorkPayload.Backend。
	ID string
	// Command 是 CLI 可执行名（"claude" | "codex"）。
	Command string
	// ClearEnv 列出 spawn 前要从继承环境里删除的 key。Runtime v3 A1：删平台
	// ANTHROPIC_API_KEY 等，让 CLI 用用户自己的 ~/.claude 订阅。
	ClearEnv []string
	// Env 是要注入子进程的额外 env（Runtime v3 A2 注入点：ANTHROPIC_BASE_URL
	// 指向 model-relay。R3 留空——A2 后续接线）。
	Env map[string]string
	// ModelAliases 把 biumind 模型名规范化成 CLI 认的（"claude-opus-4-6"→"opus"）。
	// 空映射 → 原样透传给 --model。
	ModelAliases map[string]string
}

// ClaudeCodeBackend 是内置的 Claude Code CLI 配置（A1 默认）。
var ClaudeCodeBackend = BackendConfig{
	ID:      "claude-cli",
	Command: "claude",
	// A1：清平台 key，CLI 回落到 ~/.claude 订阅（保留 HOME 让 CLI 能定位）。
	ClearEnv: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY_OLD", "ANTHROPIC_AUTH_TOKEN"},
	ModelAliases: map[string]string{
		"claude-opus-4-8":   "opus",
		"claude-opus-4-6":   "opus",
		"claude-sonnet-4-6": "sonnet",
		"claude-haiku-4-5":  "haiku",
	},
}

// CodexBackend 是内置 Codex CLI 配置（R8 接线：codex.go adapter + cliRunnerBuilder）。
var CodexBackend = BackendConfig{
	ID:       "codex-cli",
	Command:  "codex",
	ClearEnv: []string{"OPENAI_API_KEY"},
}

// IsCliBackend 报告 id 是否指向一个外部 CLI backend（非内建 biumindkit）。
// 空 / "biumindkit" → false（走内建 loop）。
func IsCliBackend(id string) bool {
	switch id {
	case ClaudeCodeBackend.ID, CodexBackend.ID:
		return true
	}
	return false
}

// ResolveBackend 按 id 返回内置 BackendConfig。
func ResolveBackend(id string) (BackendConfig, bool) {
	switch id {
	case ClaudeCodeBackend.ID:
		return ClaudeCodeBackend, true
	case CodexBackend.ID:
		return CodexBackend, true
	}
	return BackendConfig{}, false
}

// ResolveModel 把 req.Model 经 ModelAliases 规范化；无映射则原样返回。
func (c BackendConfig) ResolveModel(model string) string {
	if alias, ok := c.ModelAliases[model]; ok {
		return alias
	}
	return model
}

// childEnv 构造子进程环境：继承 os.Environ → 删 clearEnv → merge extra。
// adapter（claude.go / codex.go）spawn 前调用，保证 PATH/HOME 等不丢（修复
// 原 claude.go 只塞 req.Env 导致子进程裸环境的 bug）。
func childEnv(clearEnv []string, extra map[string]string) []string {
	cleared := make(map[string]bool, len(clearEnv))
	for _, k := range clearEnv {
		cleared[k] = true
	}
	out := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		// kv 形如 "KEY=VALUE"；按第一个 '=' 切 key。
		key := kv
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				key = kv[:i]
				break
			}
		}
		if cleared[key] {
			continue
		}
		if _, override := extra[key]; override {
			continue // 由 extra 覆盖，下面统一加
		}
		out = append(out, kv)
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
