package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIsCliBackend(t *testing.T) {
	for _, id := range []string{"claude-cli", "codex-cli"} {
		if !IsCliBackend(id) {
			t.Errorf("IsCliBackend(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "biumindkit", "bogus"} {
		if IsCliBackend(id) {
			t.Errorf("IsCliBackend(%q) = true, want false", id)
		}
	}
}

func TestResolveBackend(t *testing.T) {
	c, ok := ResolveBackend("claude-cli")
	if !ok || c.Command != "claude" {
		t.Fatalf("ResolveBackend(claude-cli) = %+v, %v", c, ok)
	}
	if _, ok := ResolveBackend("biumindkit"); ok {
		t.Fatal("biumindkit must not resolve to a CLI backend")
	}
}

func TestResolveModelAlias(t *testing.T) {
	c := ClaudeCodeBackend
	if got := c.ResolveModel("claude-sonnet-4-6"); got != "sonnet" {
		t.Errorf("resolveModel alias = %q, want sonnet", got)
	}
	if got := c.ResolveModel("custom-model"); got != "custom-model" {
		t.Errorf("resolveModel passthrough = %q, want custom-model", got)
	}
}

// A1 核心：childEnv 必须继承 os.Environ、删 ClearEnv 列出的 key、merge extra。
func TestChildEnv_ClearsAndMerges(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "platform-secret")
	t.Setenv("PATH", "/usr/bin") // 确保 PATH 类基础 env 被保留

	env := childEnv([]string{"ANTHROPIC_API_KEY"}, map[string]string{"ANTHROPIC_BASE_URL": "http://relay"})

	var hasKey, hasPath, hasBase bool
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "ANTHROPIC_API_KEY="):
			hasKey = true
		case strings.HasPrefix(kv, "PATH="):
			hasPath = true
		case kv == "ANTHROPIC_BASE_URL=http://relay":
			hasBase = true
		}
	}
	if hasKey {
		t.Error("ClearEnv 未生效：ANTHROPIC_API_KEY 仍在子进程环境(A1 泄漏)")
	}
	if !hasPath {
		t.Error("childEnv 丢了 PATH —— 子进程会找不到可执行")
	}
	if !hasBase {
		t.Error("extra(ANTHROPIC_BASE_URL) 未注入")
	}
	_ = os.Environ
}
