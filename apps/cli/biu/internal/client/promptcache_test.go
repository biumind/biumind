package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestSplitSystemNoBoundary(t *testing.T) {
	got := splitSystem("You are biu. Use snake_case.")
	if len(got) != 1 {
		t.Fatalf("expected 1 block, got %d", len(got))
	}
	if got[0].CacheControl == nil || got[0].CacheControl.Type != "ephemeral" {
		t.Errorf("single block should be cacheable: %+v", got[0])
	}
}

func TestSplitSystemWithBoundary(t *testing.T) {
	prompt := "You are biu.\n" + SystemDynamicBoundary + "\ncwd: /tmp/abc"
	got := splitSystem(prompt)
	if len(got) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "You are biu") {
		t.Errorf("block 0 should be the static prefix: %q", got[0].Text)
	}
	if got[0].CacheControl == nil {
		t.Errorf("block 0 should be cacheable")
	}
	if !strings.Contains(got[1].Text, "cwd: /tmp/abc") {
		t.Errorf("block 1 should be the dynamic suffix: %q", got[1].Text)
	}
	if got[1].CacheControl != nil {
		t.Errorf("block 1 must NOT be cacheable: %+v", got[1])
	}
}

func TestSplitSystemEmpty(t *testing.T) {
	if got := splitSystem(""); got != nil {
		t.Errorf("empty input should return nil; got %+v", got)
	}
	if got := splitSystem("   \n\n  "); got != nil {
		t.Errorf("whitespace-only should return nil; got %+v", got)
	}
}

func TestMarkLastMessageStringContent(t *testing.T) {
	msgs := []anthropicReqMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
	}
	markLastMessageForCache(msgs)
	if _, ok := msgs[0].Content.(string); !ok {
		t.Errorf("earlier message should remain string-form")
	}
	last, ok := msgs[1].Content.([]map[string]any)
	if !ok {
		t.Fatalf("last should be promoted to block array; got %T", msgs[1].Content)
	}
	if len(last) != 1 {
		t.Fatalf("expected 1 block, got %d", len(last))
	}
	cc, ok := last[0]["cache_control"].(map[string]string)
	if !ok || cc["type"] != "ephemeral" {
		t.Errorf("missing cache_control: %+v", last[0])
	}
}

func TestMarkLastMessageBlockArray(t *testing.T) {
	msgs := []anthropicReqMessage{{
		Role: "user",
		Content: []map[string]any{
			{"type": "text", "text": "hello"},
			{"type": "text", "text": "world"},
		},
	}}
	markLastMessageForCache(msgs)
	blocks := msgs[0].Content.([]map[string]any)
	if _, hasCC := blocks[0]["cache_control"]; hasCC {
		t.Errorf("only the last block should be marked; first leaked: %+v", blocks[0])
	}
	if _, hasCC := blocks[1]["cache_control"]; !hasCC {
		t.Errorf("last block should be marked: %+v", blocks[1])
	}
}

func TestMarkLastMessageEmpty(t *testing.T) {
	// No-op should not panic.
	markLastMessageForCache(nil)
	markLastMessageForCache([]anthropicReqMessage{})
}

func TestTranslateToolsStableOrder(t *testing.T) {
	// Same tools, different input order — output must be byte-identical.
	a := []engine.ToolSpec{
		{Name: "Zebra", Description: "z"},
		{Name: "Alpha", Description: "a"},
		{Name: "Mango", Description: "m"},
	}
	b := []engine.ToolSpec{
		{Name: "Mango", Description: "m"},
		{Name: "Alpha", Description: "a"},
		{Name: "Zebra", Description: "z"},
	}
	ja, _ := json.Marshal(translateTools(a))
	jb, _ := json.Marshal(translateTools(b))
	if string(ja) != string(jb) {
		t.Errorf("tool ordering not stable:\n a=%s\n b=%s", ja, jb)
	}
}

func TestTranslateToolsDoesNotEmitCacheControl(t *testing.T) {
	got := translateTools([]engine.ToolSpec{
		{Name: "Read", Description: "r", InputSchema: map[string]any{"type": "object"}},
	})
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("tools must not carry cache_control; got: %s", body)
	}
}

func TestRequestEncodesAsExpected(t *testing.T) {
	// Minimal end-to-end encoding test — confirms the wire format
	// matches what Anthropic Messages API expects.
	req := anthropicReq{
		Model: "claude-test",
		System: splitSystem(
			"You are biu.\n" + SystemDynamicBoundary + "\ncwd: /tmp"),
		Messages: []anthropicReqMessage{{Role: "user", Content: "hi"}},
		Tools: translateTools([]engine.ToolSpec{
			{Name: "Echo", Description: "echo"},
		}),
		MaxTokens: 64,
		Stream:    true,
	}
	markLastMessageForCache(req.Messages)

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `"system":[`) {
		t.Errorf("system should serialise as array: %s", s)
	}
	// Static block has cache_control; dynamic does not.
	if strings.Count(s, "ephemeral") != 2 {
		// 1 in system static, 1 in last message — tools stripped.
		t.Errorf("expected 2 cache_control markers, got %d in: %s",
			strings.Count(s, "ephemeral"), s)
	}
	if strings.Contains(strings.SplitN(s, `"messages"`, 2)[0], "cwd: /tmp") {
		// dynamic part should be present but in its own block, not cached
	}
}
