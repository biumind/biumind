package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEncodeProjectDir(t *testing.T) {
	cases := map[string]string{
		"/Users/didi/workspaces/biumind": "-Users-didi-workspaces-biumind",
		"/tmp/a_b.c":                     "-tmp-a-b-c",
		"already-dashed":                 "already-dashed",
		"/x/y-z/1":                       "-x-y-z-1",
	}
	for in, want := range cases {
		if got := EncodeProjectDir(in); got != want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseClaudeAssistant_TextToolCost(t *testing.T) {
	line := []byte(`{"type":"assistant","timestamp":"2026-06-26T01:02:03Z","message":{` +
		`"content":[` +
		`{"type":"thinking","thinking":"hmm"},` +
		`{"type":"text","text":"我来读文件"},` +
		`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"a.dart"}}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":120,"output_tokens":8}}}`)
	ev := parseClaudeLine(line)
	// 期望:text_delta + tool_use_start + cost_update(thinking 跳过)
	if len(ev) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(ev), ev)
	}
	if ev[0]["type"] != "text_delta" || ev[0]["text"] != "我来读文件" {
		t.Errorf("event0 wrong: %+v", ev[0])
	}
	if ev[1]["type"] != "tool_use_start" || ev[1]["tool_id"] != "toolu_1" || ev[1]["name"] != "Read" {
		t.Errorf("event1 wrong: %+v", ev[1])
	}
	args, ok := ev[1]["args"].(map[string]any)
	if !ok || args["file_path"] != "a.dart" {
		t.Errorf("tool args not parsed: %+v", ev[1]["args"])
	}
	if ev[2]["type"] != "cost_update" || ev[2]["input_tokens"] != 120 || ev[2]["output_tokens"] != 8 {
		t.Errorf("event2 (cost) wrong: %+v", ev[2])
	}
	if ev[0]["ts"] != "2026-06-26T01:02:03Z" {
		t.Errorf("ts not propagated: %+v", ev[0]["ts"])
	}
}

func TestParseClaudeUser_ToolResult(t *testing.T) {
	// content 为字符串
	line := []byte(`{"type":"user","timestamp":"t","message":{"content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","content":"file body","is_error":false}]}}`)
	ev := parseClaudeLine(line)
	if len(ev) != 1 || ev[0]["type"] != "tool_use_result" {
		t.Fatalf("want 1 tool_use_result, got %+v", ev)
	}
	if ev[0]["tool_id"] != "toolu_1" || ev[0]["result"] != "file body" || ev[0]["is_error"] != false {
		t.Errorf("tool_result wrong: %+v", ev[0])
	}

	// content 为数组([{text},{image}])
	line2 := []byte(`{"type":"user","message":{"content":[` +
		`{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"L1"},{"type":"image"}],"is_error":true}]}}`)
	ev2 := parseClaudeLine(line2)
	if len(ev2) != 1 || ev2[0]["result"] != "L1\n[image]" || ev2[0]["is_error"] != true {
		t.Errorf("array tool_result wrong: %+v", ev2)
	}
}

func TestParseClaudeLine_IgnoresMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"type":"system","subtype":"hook"}`,
		`{"type":"file-history-snapshot","messageId":"x"}`,
		`{"type":"permission-mode","permissionMode":"default"}`,
		`not json at all`,
		`{}`,
	} {
		if ev := parseClaudeLine([]byte(raw)); ev != nil {
			t.Errorf("expected nil for %q, got %+v", raw, ev)
		}
	}
}

func TestTailReader_Incremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	tr := &tailReader{path: p}

	// 文件还不存在 → (nil,nil)
	if lines, err := tr.read(); err != nil || lines != nil {
		t.Fatalf("missing file: got %v,%v", lines, err)
	}

	os.WriteFile(p, []byte("line1\nline2\n"), 0o644)
	lines, _ := tr.read()
	if len(lines) != 2 || string(lines[0]) != "line1" || string(lines[1]) != "line2" {
		t.Fatalf("first read: %q", lines)
	}

	// 追加一个完整行 + 一个不完整行 → 只返回完整的,partial 留存
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("line3\npart")
	f.Close()
	lines, _ = tr.read()
	if len(lines) != 1 || string(lines[0]) != "line3" {
		t.Fatalf("incremental read: %q", lines)
	}

	// 补全 partial
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("ial\n")
	f.Close()
	lines, _ = tr.read()
	if len(lines) != 1 || string(lines[0]) != "partial" {
		t.Fatalf("partial completion: %q", lines)
	}
}

func TestWatchClaude_TailsLiveFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "live.jsonl")

	var mu sync.Mutex
	var got []map[string]any
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WatchClaude(ctx, p, func(ev []map[string]any) {
			mu.Lock()
			got = append(got, ev...)
			mu.Unlock()
		})
		close(done)
	}()

	// 文件晚于 watcher 出现,且分两次写。
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(p, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n"), 0o644)
	time.Sleep(250 * time.Millisecond)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]}}` + "\n")
	f.Close()
	time.Sleep(250 * time.Millisecond)

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("want 2 events tailed live, got %d: %+v", len(got), got)
	}
	if got[0]["type"] != "text_delta" || got[1]["type"] != "tool_use_result" {
		t.Errorf("wrong events: %+v", got)
	}
}

// 两条 assistant 消息各带 usage —— WatchClaude 应累加成累计 token(CORE-6),
// 而非每条独立(客户端 CostUpdate 是 replace 语义,需累计才对)。
func TestWatchClaude_AccumulatesTokens(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "live.jsonl")
	os.WriteFile(p, []byte(
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a"}],"usage":{"input_tokens":100,"output_tokens":10}}}`+"\n"+
			`{"type":"assistant","message":{"content":[{"type":"text","text":"b"}],"usage":{"input_tokens":50,"output_tokens":4}}}`+"\n",
	), 0o644)

	var mu sync.Mutex
	var costs []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		WatchClaude(ctx, p, func(ev []map[string]any) {
			mu.Lock()
			for _, e := range ev {
				if e["type"] == "cost_update" {
					costs = append(costs, e)
				}
			}
			mu.Unlock()
		})
		close(done)
	}()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(costs) != 2 {
		t.Fatalf("want 2 cost_update, got %d: %+v", len(costs), costs)
	}
	// 第一条:100/10;第二条:累计 150/14。
	if costs[0]["input_tokens"] != 100 || costs[0]["output_tokens"] != 10 {
		t.Errorf("first cost not 100/10: %+v", costs[0])
	}
	if costs[1]["input_tokens"] != 150 || costs[1]["output_tokens"] != 14 {
		t.Errorf("second cost should be cumulative 150/14: %+v", costs[1])
	}
}
