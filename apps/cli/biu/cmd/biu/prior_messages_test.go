package main

import (
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/agentplane"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func TestPriorMessagesFromHistory(t *testing.T) {
	// 空 → nil(单轮,向后兼容)。
	if got := priorMessagesFromHistory(nil); got != nil {
		t.Fatalf("空历史应返回 nil, got %v", got)
	}

	in := []agentplane.ChatTurn{
		{Role: "user", Content: "当前目录有啥"},
		{Role: "assistant", Content: "有 3 个文件"},
		{Role: "user", Content: ""},            // 空内容 → 跳过
		{Role: "tool", Content: "should skip"}, // 非 user/assistant → 跳过(P1 文本级)
		{Role: "user", Content: "我刚才问的啥"},
	}
	got := priorMessagesFromHistory(in)
	if len(got) != 3 {
		t.Fatalf("应保留 3 条(跳过空 + tool), got %d", len(got))
	}
	want := []struct {
		role string
		text string
	}{
		{"user", "当前目录有啥"},
		{"assistant", "有 3 个文件"},
		{"user", "我刚才问的啥"},
	}
	for i, w := range want {
		if got[i].Role != w.role {
			t.Errorf("msg[%d].Role = %q, want %q", i, got[i].Role, w.role)
		}
		if len(got[i].Content) != 1 || got[i].Content[0].Type != biumindkit.ContentText {
			t.Fatalf("msg[%d] 应为单个 text ContentBlock", i)
		}
		if got[i].Content[0].Text != w.text {
			t.Errorf("msg[%d].text = %q, want %q", i, got[i].Content[0].Text, w.text)
		}
	}
}
