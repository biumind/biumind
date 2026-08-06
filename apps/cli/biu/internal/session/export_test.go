package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestSession dumps `events` to a temp .jsonl file and returns
// the path. Centralised so each test stays focused on assertions.
func writeTestSession(t *testing.T, events []Event) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-120000-aaaa.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range events {
		if ev.TS.IsZero() {
			ev.TS = time.Date(2026, 1, 1, 12, 0, i, 0, time.UTC)
			events[i] = ev
		}
		body, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(body)
		f.Write([]byte{'\n'})
	}
	f.Close()
	return path
}

func TestExportMarkdownIncludesUserAndAssistantTurns(t *testing.T) {
	path := writeTestSession(t, []Event{
		{Type: "user_message", Content: "what does main.go do?"},
		{Type: "tool_use", Name: "Read", CallID: "tu_1", Args: map[string]any{"path": "main.go"}},
		{Type: "tool_result", CallID: "tu_1", Output: "package main\nfunc main() {}"},
		{Type: "assistant_message", Content: "It's the program entry point."},
		{Type: "end", Reason: "end_turn"},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{Format: FormatMarkdown, IncludeToolOutput: true}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"# Session 20260101-120000-aaaa",
		"👤 User",
		"what does main.go do?",
		"⏺ Read",
		"`tu_1`",
		"package main",
		"🤖 Assistant",
		"It's the program entry point.",
		"Session ended (reason: end_turn)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestExportMarkdownExcludeToolOutput(t *testing.T) {
	path := writeTestSession(t, []Event{
		{Type: "user_message", Content: "hi"},
		{Type: "tool_use", Name: "Read", CallID: "tu_1"},
		{Type: "tool_result", CallID: "tu_1", Output: "secret payload should not appear"},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{Format: FormatMarkdown, IncludeToolOutput: false}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "secret payload") {
		t.Errorf("output suppressed but body still leaked:\n%s", got)
	}
	if !strings.Contains(got, "tool_result `tu_1` (32 bytes, hidden)") {
		t.Errorf("expected hidden marker; got:\n%s", got)
	}
}

func TestExportJSONCoalescesTurns(t *testing.T) {
	path := writeTestSession(t, []Event{
		{Type: "user_message", Content: "first"},
		{Type: "tool_use", Name: "Read", CallID: "tu_1", Args: map[string]any{"path": "/x"}},
		{Type: "tool_result", CallID: "tu_1", Output: "ok"},
		{Type: "assistant_message", Content: "done"},
		{Type: "user_message", Content: "second"},
		{Type: "assistant_message", Content: "still here"},
		{Type: "end", Reason: "end_turn"},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{Format: FormatJSON, IncludeToolOutput: true}); err != nil {
		t.Fatal(err)
	}
	var got JSONExport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Turns) != 2 {
		t.Fatalf("expected 2 turns; got %d", len(got.Turns))
	}
	if got.Turns[0].UserPrompt != "first" || got.Turns[0].AssistantText != "done" {
		t.Errorf("turn 0 wrong: %+v", got.Turns[0])
	}
	if len(got.Turns[0].ToolCalls) != 1 || got.Turns[0].ToolCalls[0].Name != "Read" {
		t.Errorf("turn 0 missing tool call: %+v", got.Turns[0])
	}
	if got.Turns[0].ToolCalls[0].Output != "ok" {
		t.Errorf("turn 0 tool output wrong: %q", got.Turns[0].ToolCalls[0].Output)
	}
	if got.Turns[1].UserPrompt != "second" || got.Turns[1].AssistantText != "still here" {
		t.Errorf("turn 1 wrong: %+v", got.Turns[1])
	}
}

func TestExportAnthropicReplayShape(t *testing.T) {
	path := writeTestSession(t, []Event{
		{Type: "user_message", Content: "hi"},
		{Type: "tool_use", Name: "Read", CallID: "tu_1", Args: map[string]any{"path": "x"}},
		{Type: "tool_result", CallID: "tu_1", Output: "ok"},
		{Type: "assistant_message", Content: "done"},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{Format: FormatAnthropicReplay, IncludeToolOutput: true}); err != nil {
		t.Fatal(err)
	}
	var got AnthropicReplay
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Expect: user(text=hi) → assistant(tool_use) → user(tool_result) → assistant(text=done)
	if len(got.Messages) != 4 {
		t.Fatalf("expected 4 messages; got %d (%+v)", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content[0].Text != "hi" {
		t.Errorf("msg 0 wrong: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content[0].Type != "tool_use" {
		t.Errorf("msg 1 wrong: %+v", got.Messages[1])
	}
	if got.Messages[2].Role != "user" || got.Messages[2].Content[0].Type != "tool_result" {
		t.Errorf("msg 2 wrong: %+v", got.Messages[2])
	}
	if got.Messages[3].Role != "assistant" || got.Messages[3].Content[0].Text != "done" {
		t.Errorf("msg 3 wrong: %+v", got.Messages[3])
	}
}

func TestExportRedactsSecretsInArgs(t *testing.T) {
	path := writeTestSession(t, []Event{
		{Type: "user_message", Content: "use my key sk-ant-abc1234567890def1234567890ghij1234"},
		{Type: "tool_use", Name: "Bash", CallID: "tu_1",
			Args: map[string]any{"api_key": "sk-ant-very-secret-key-payload-aaaaaaaaaaaaaaaa", "command": "ls"}},
		{Type: "tool_result", CallID: "tu_1", Output: "Authorization: Bearer abcdef1234567890abcdef1234567890"},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{Format: FormatMarkdown, IncludeToolOutput: true}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "very-secret-key-payload") {
		t.Errorf("api_key arg leaked:\n%s", got)
	}
	if strings.Contains(got, "abcdef1234567890abcdef1234567890") {
		t.Errorf("Bearer token leaked:\n%s", got)
	}
	// The user-typed `sk-ant-…` should be redacted in content too.
	if strings.Contains(got, "sk-ant-abc1234567890def1234567890ghij1234") {
		t.Errorf("user-content sk-ant key leaked:\n%s", got)
	}
}

func TestExportTruncatesLongToolOutput(t *testing.T) {
	long := strings.Repeat("x", 10000)
	path := writeTestSession(t, []Event{
		{Type: "tool_use", Name: "Bash", CallID: "tu_1"},
		{Type: "tool_result", CallID: "tu_1", Output: long},
	})
	var buf bytes.Buffer
	if _, err := Export(path, &buf, ExportOptions{
		Format: FormatMarkdown, IncludeToolOutput: true,
		MaxToolOutputBytes: 100,
	}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("expected truncation marker; got:\n%s", got)
	}
	if strings.Count(got, "x") > 200 { // 100 cap + slack for surrounding markup
		t.Errorf("output not truncated; saw %d xs", strings.Count(got, "x"))
	}
}

func TestExportUnknownFormatErrors(t *testing.T) {
	path := writeTestSession(t, []Event{{Type: "user_message", Content: "hi"}})
	var buf bytes.Buffer
	_, err := Export(path, &buf, ExportOptions{Format: "yaml"})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}
