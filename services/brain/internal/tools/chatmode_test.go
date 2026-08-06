package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// stubInvoke is a no-op cloud invoker so a tool counts as biumindkit-dispatchable.
func stubInvoke(context.Context, json.RawMessage) (any, error) { return "ok", nil }

func newCloudTool(name string) Tool {
	return Tool{
		Descriptor: Descriptor{Name: name, Runtime: RuntimeCloud},
		Invoke:     stubInvoke,
	}
}

// chat 模式默认拒绝：注册一个不在白名单里的 cloud 工具，AvailableForBiumindkit
// 带 DefaultChatToolAllowlist 时必须把它挡掉；带 nil 时全放行。
func TestAvailableForBiumindkit_ChatWhitelist(t *testing.T) {
	r := New()
	r.MustRegister(newCloudTool("websearch"))     // 白名单内
	r.MustRegister(newCloudTool("memory_recall")) // 白名单内
	r.MustRegister(newCloudTool("danger_exec"))   // 白名单外 —— 必须被拒

	// nil allow = 不限制：3 个都在。
	if got := len(r.AvailableForBiumindkit(nil)); got != 3 {
		t.Fatalf("nil allow: want 3 tools, got %d", got)
	}

	// chat 白名单：只剩 2 个，danger_exec 被默认拒绝。
	chat := r.AvailableForBiumindkit(DefaultChatToolAllowlist)
	if len(chat) != 2 {
		t.Fatalf("chat allow: want 2 tools, got %d", len(chat))
	}
	for _, tl := range chat {
		if tl.Name() == "danger_exec" {
			t.Fatalf("danger_exec leaked into chat mode despite not being whitelisted")
		}
	}
}

func TestFilterChatAllowed(t *testing.T) {
	ds := []Descriptor{
		{Name: "websearch"}, {Name: "danger_exec"}, {Name: "time_now"},
	}

	// nil allow → 原样返回。
	if got := FilterChatAllowed(ds, nil); len(got) != 3 {
		t.Fatalf("nil allow: want 3, got %d", len(got))
	}

	// 默认白名单 → danger_exec 被剔除，保序。
	got := FilterChatAllowed(ds, DefaultChatToolAllowlist)
	if len(got) != 2 || got[0].Name != "websearch" || got[1].Name != "time_now" {
		t.Fatalf("filtered = %+v, want [websearch time_now]", got)
	}
}

func TestChatAllows_NilMeansOpen(t *testing.T) {
	if !chatAllows(nil, "anything") {
		t.Fatal("nil allowlist must permit every tool")
	}
	if chatAllows(DefaultChatToolAllowlist, "danger_exec") {
		t.Fatal("danger_exec must be denied under default allowlist")
	}
	if !chatAllows(DefaultChatToolAllowlist, "wiki_search") {
		t.Fatal("wiki_search must be permitted under default allowlist")
	}
}
