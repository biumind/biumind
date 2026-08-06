package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// findEvent 返回 events 中第一个 type==t 的事件,无则 nil。
func findEvent(events []map[string]any, t string) map[string]any {
	for _, e := range events {
		if e["type"] == t {
			return e
		}
	}
	return nil
}

func TestParseCodex_TextToolCost(t *testing.T) {
	conf := newCodexConfirm("/proj")

	// 助手文本(response_item/message,output_text 块)。
	out := parseCodexLine([]byte(`{"type":"response_item","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}`), conf)
	if ev := findEvent(out, "text_delta"); ev == nil || ev["text"] != "hello" {
		t.Fatalf("expected text_delta hello, got %+v", out)
	}

	// 工具调用(只读 ls)→ tool_use_start,且不需确认 → 无 permission_ask。
	out = parseCodexLine([]byte(`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"ls -la\"}"}}`), conf)
	if ev := findEvent(out, "tool_use_start"); ev == nil || ev["name"] != "exec_command" || ev["tool_id"] != "c1" {
		t.Fatalf("expected tool_use_start exec_command, got %+v", out)
	}
	if findEvent(out, "permission_ask") != nil {
		t.Fatalf("read-only ls should not require confirmation: %+v", out)
	}

	// 工具结果 → tool_use_result。
	out = parseCodexLine([]byte(`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"file list"}}`), conf)
	if ev := findEvent(out, "tool_use_result"); ev == nil || ev["result"] != "file list" {
		t.Fatalf("expected tool_use_result, got %+v", out)
	}

	// token_count(累计)→ cost_update,直接透传(非累加)。
	out = parseCodexLine([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}}`), conf)
	ev := findEvent(out, "cost_update")
	if ev == nil || ev["input_tokens"] != 100 || ev["output_tokens"] != 20 {
		t.Fatalf("expected cost_update 100/20, got %+v", out)
	}
}

func TestParseCodex_ConfirmationRisingEdge(t *testing.T) {
	conf := newCodexConfirm("/proj")

	// 非只读 exec(rm)→ 需确认 → pending 加 call_id → 上升沿 emit permission_ask。
	out := parseCodexLine([]byte(`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c9","arguments":"{\"cmd\":\"rm -rf build\"}"}}`), conf)
	if findEvent(out, "permission_ask") == nil {
		t.Fatalf("rm should require confirmation (permission_ask): %+v", out)
	}
	if !conf.waiting {
		t.Fatalf("conf.waiting should be true after confirmable call")
	}

	// 解除(function_call_output 同 call_id)→ pending 清空 → 不再 emit 新 permission_ask。
	out = parseCodexLine([]byte(`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c9","output":"done"}}`), conf)
	if findEvent(out, "permission_ask") != nil {
		t.Fatalf("falling edge should not emit permission_ask: %+v", out)
	}
	if conf.waiting {
		t.Fatalf("conf.waiting should be false after output resolves pending")
	}
}

func TestLooksLikeReadOnlyCommand(t *testing.T) {
	cases := map[string]bool{
		"ls -la":            true,
		"pwd":               true,
		"git status":        true,
		"cat a | grep b":    true,
		"sed -n '1,5p' f":   true,
		"rm -rf x":          false,
		"git push":          false,
		"echo hi > out.txt": false,
		"sed -i 's/a/b/' f": false,
		"find . -delete":    false,
	}
	for cmd, want := range cases {
		if got := looksLikeReadOnlyCommand(cmd); got != want {
			t.Errorf("looksLikeReadOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestApplyPatchConfirmation(t *testing.T) {
	cwd := "/proj"
	// 改项目内文件(相对/项目内绝对路径)→ 不需确认。
	in := "*** Begin Patch\n*** Update File: /proj/main.go\n+x\n*** End Patch"
	if applyPatchRequiresConfirmation(in, cwd) {
		t.Errorf("patch within project should not require confirmation")
	}
	// 改项目外绝对路径 → 需确认。
	out := "*** Begin Patch\n*** Add File: /etc/evil.conf\n+x\n*** End Patch"
	if !applyPatchRequiresConfirmation(out, cwd) {
		t.Errorf("patch outside project should require confirmation")
	}
}

func TestDiscoverCodexRollout_NewFileOnly(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, ".codex", "sessions", "2026", "01")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(root, ".codex", "sessions")}

	// 启动前已存在一个旧 rollout → 进快照,不应被选中。
	old := filepath.Join(sessions, "rollout-2026-01-01-old.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	startSet := map[string]bool{}
	for _, p := range collectRollouts(roots) {
		startSet[p] = true
	}

	// 异步创建新 rollout,discover 应发现它(而非旧的)。
	want := filepath.Join(sessions, "rollout-2026-01-01-new.jsonl")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(want, []byte("{}\n"), 0o644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := discoverCodexRollout(ctx, roots, startSet)
	if got != want {
		t.Fatalf("discover = %q, want %q", got, want)
	}
}
