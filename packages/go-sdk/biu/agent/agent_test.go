package agent

import (
	"strings"
	"testing"
)

func collect(ch <-chan Event) []Event {
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

// ─── Claude Code parser ───────────────────────────────────

func TestParseClaudeStream_TextAndToolUse(t *testing.T) {
	jsonl := `
{"type":"system","session_id":"s1","model":"claude-opus-4-7","tools":["read","bash"]}
{"type":"assistant","session_id":"s1","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Hello"}]}}
{"type":"assistant","session_id":"s1","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"bash","input":{"command":"ls"}}]}}
{"type":"user","session_id":"s1","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file1\nfile2"}]}}
{"type":"assistant","session_id":"s1","message":{"content":[{"type":"text","text":" world"}]}}
{"type":"result","session_id":"s1","result":"ok"}
`
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		parseClaudeStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)

	wantTypes := []string{
		EventSystem,
		EventThinking,
		EventText,
		EventToolUse,
		EventToolResult,
		EventText,
		EventDone,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("want %d events, got %d: %+v", len(wantTypes), len(events), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	// Spot-check tool_use and tool_result content
	if events[3].Tool == nil || events[3].Tool.Name != "bash" {
		t.Errorf("tool_use bad: %+v", events[3])
	}
	if events[4].Tool == nil || events[4].Tool.Output != "file1\nfile2" {
		t.Errorf("tool_result output = %q", events[4].Tool.Output)
	}
}

func TestParseClaudeStream_ToolResultArrayContent(t *testing.T) {
	// content array form: [{"type":"text","text":"…"}, ...]
	jsonl := `{"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}}` + "\n"
	out := make(chan Event, 4)
	go func() {
		defer close(out)
		parseClaudeStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)
	if len(events) != 1 || events[0].Tool == nil || events[0].Tool.Output != "ab" {
		t.Errorf("array tool_result content combine: %+v", events)
	}
}

func TestParseClaudeStream_UnknownTypeBecomesRaw(t *testing.T) {
	jsonl := `{"type":"experimental_thing","session_id":"s","payload":{"x":1}}` + "\n"
	out := make(chan Event, 4)
	go func() {
		defer close(out)
		parseClaudeStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)
	if len(events) != 1 || events[0].Type != EventRaw {
		t.Errorf("unknown frame should fall through as raw: %+v", events)
	}
}

// ─── Codex parser ──────────────────────────────────────────

func TestParseCodexStream(t *testing.T) {
	jsonl := `
{"type":"agent_thinking","content":"plan"}
{"type":"agent_message","content":"running ls"}
{"type":"shell_command","command":["bash","-c","ls"]}
{"type":"shell_output","output":"a\nb","exit_code":0}
{"type":"task_complete","reason":"done"}
`
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		parseCodexStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)
	want := []string{EventThinking, EventText, EventCommand, EventToolResult, EventDone}
	if len(events) != len(want) {
		t.Fatalf("want %d, got %d: %+v", len(want), len(events), events)
	}
	for i, w := range want {
		if events[i].Type != w {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, w)
		}
	}
	if events[2].Content != "bash -c ls" {
		t.Errorf("shell_command joined = %q, want 'bash -c ls'", events[2].Content)
	}
}

// ─── biu CLI parser ────────────────────────────────────────

func TestParseBiuStream_AssemblesToolCallArgs(t *testing.T) {
	// AG-UI splits tool args across multiple frames; the adapter should
	// rejoin them at TOOL_CALL_END.
	jsonl := `
{"type":"TOOL_CALL_START","tool_call_id":"t1","tool_call_name":"read","thread_id":"th"}
{"type":"TOOL_CALL_ARGS","tool_call_id":"t1","delta":"{\"path\":\""}
{"type":"TOOL_CALL_ARGS","tool_call_id":"t1","delta":"foo.txt\"}"}
{"type":"TOOL_CALL_END","tool_call_id":"t1","thread_id":"th"}
{"type":"biumind.tool_result","tool_call_id":"t1","output":"hi"}
{"type":"RUN_FINISHED","thread_id":"th"}
`
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		parseBiuStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)
	if len(events) != 3 {
		t.Fatalf("want 3 events (use+result+done), got %d: %+v", len(events), events)
	}
	if events[0].Type != EventToolUse || events[0].Tool.Name != "read" ||
		events[0].Tool.Input["path"] != "foo.txt" {
		t.Errorf("tool_use rejoin: %+v", events[0].Tool)
	}
	if events[1].Type != EventToolResult || events[1].Tool.Output != "hi" {
		t.Errorf("tool_result: %+v", events[1].Tool)
	}
	if events[2].Type != EventDone {
		t.Errorf("done: %+v", events[2])
	}
}

func TestParseBiuStream_RunError(t *testing.T) {
	jsonl := `{"type":"RUN_ERROR","thread_id":"th","message":"boom"}` + "\n"
	out := make(chan Event, 4)
	go func() {
		defer close(out)
		parseBiuStream(strings.NewReader(jsonl), out)
	}()
	events := collect(out)
	if len(events) != 1 || events[0].Type != EventError || events[0].Content != "boom" {
		t.Errorf("error: %+v", events)
	}
}
