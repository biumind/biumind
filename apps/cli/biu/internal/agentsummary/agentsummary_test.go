package agentsummary

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

type stubSummer struct {
	calls    int
	lastInst string
	lastMsgs []state.Message
	resp     string
	err      error
}

func (s *stubSummer) Summarise(ctx context.Context, msgs []state.Message, inst string) (string, error) {
	s.calls++
	s.lastInst = inst
	s.lastMsgs = msgs
	if s.err != nil {
		return "", s.err
	}
	return s.resp, nil
}

// ─── GenerateToolBatch ───────────────────────────────────────

func TestGenerateToolBatch_emptyToolsNoOp(t *testing.T) {
	s := &stubSummer{}
	got, err := GenerateToolBatch(context.Background(), s, nil, "")
	if err != nil || got != "" {
		t.Errorf("empty tools should be silent no-op, got %q / %v", got, err)
	}
	if s.calls != 0 {
		t.Errorf("summariser should not be called on empty input")
	}
}

func TestGenerateToolBatch_includesPrimaryArg(t *testing.T) {
	s := &stubSummer{resp: "Read config.json"}
	tools := []ToolCall{
		{Name: "Read", Input: map[string]any{"file_path": "config.json"}},
	}
	got, err := GenerateToolBatch(context.Background(), s, tools, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Read config.json" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(s.lastInst, `file_path="config.json"`) {
		t.Errorf("prompt should include primary arg: %s", s.lastInst)
	}
}

func TestGenerateToolBatch_clipsLongResponse(t *testing.T) {
	s := &stubSummer{resp: strings.Repeat("a", 100)}
	got, _ := GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "X"}}, "")
	if len([]rune(got)) > MaxSummaryChars {
		t.Errorf("not clipped: %d chars", len([]rune(got)))
	}
}

func TestGenerateToolBatch_stripsWrappingQuotes(t *testing.T) {
	s := &stubSummer{resp: `"Searched in auth/"`}
	got, _ := GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "Grep"}}, "")
	if got != "Searched in auth/" {
		t.Errorf("quotes not stripped: %q", got)
	}
}

func TestGenerateToolBatch_stripsBoldMarkdown(t *testing.T) {
	s := &stubSummer{resp: "**Read config.json**"}
	got, _ := GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "Read"}}, "")
	if got != "Read config.json" {
		t.Errorf("bold markdown not stripped: %q", got)
	}
}

func TestGenerateToolBatch_errorPropagates(t *testing.T) {
	s := &stubSummer{err: errors.New("api down")}
	_, err := GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "X"}}, "")
	if err == nil {
		t.Error("error should propagate")
	}
}

func TestGenerateToolBatch_includesErrorFlag(t *testing.T) {
	s := &stubSummer{resp: "Failed"}
	_, _ = GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "Bash", Input: map[string]any{"command": "false"}, IsError: true}}, "")
	if !strings.Contains(s.lastInst, "[ERROR]") {
		t.Errorf("prompt should mark error tools: %s", s.lastInst)
	}
}

func TestGenerateToolBatch_includesLastAssistant(t *testing.T) {
	s := &stubSummer{resp: "Read"}
	_, _ = GenerateToolBatch(context.Background(), s,
		[]ToolCall{{Name: "Read"}}, "now I'll edit it")
	if !strings.Contains(s.lastInst, "Assistant said next") {
		t.Errorf("prompt should include lastAssistant: %s", s.lastInst)
	}
}

// ─── GenerateAgentTick ────────────────────────────────────────

func TestGenerateAgentTick_basic(t *testing.T) {
	s := &stubSummer{resp: "Reading runAgent.ts"}
	got, err := GenerateAgentTick(context.Background(), s,
		[]state.Message{{Role: state.RoleUser, Content: []state.ContentBlock{
			{Type: state.ContentText, Text: "explore"},
		}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Reading runAgent.ts" {
		t.Errorf("got %q", got)
	}
}

func TestGenerateAgentTick_includesPreviousInPrompt(t *testing.T) {
	s := &stubSummer{resp: "Fixing null check"}
	_, _ = GenerateAgentTick(context.Background(), s, nil, "Reading file")
	if !strings.Contains(s.lastInst, "Reading file") {
		t.Errorf("prompt should include previous: %s", s.lastInst)
	}
	if !strings.Contains(s.lastInst, "say something NEW") {
		t.Error("prompt should ask for new content when previous is set")
	}
}

func TestGenerateAgentTick_omitsPreviousLineWhenEmpty(t *testing.T) {
	s := &stubSummer{resp: "Starting"}
	_, _ = GenerateAgentTick(context.Background(), s, nil, "")
	if strings.Contains(s.lastInst, "Previous:") {
		t.Error("first-tick prompt should NOT include Previous: line")
	}
}

func TestGenerateAgentTick_nilSummariser(t *testing.T) {
	got, err := GenerateAgentTick(context.Background(), nil, nil, "")
	if err != nil || got != "" {
		t.Errorf("nil summariser → silent no-op, got %q / %v", got, err)
	}
}

// ─── primaryArg ──────────────────────────────────────────────

func TestPrimaryArg_priority(t *testing.T) {
	cases := []struct {
		input  map[string]any
		wantK  string
	}{
		{map[string]any{"file_path": "a.go"}, "file_path"},
		{map[string]any{"path": "b.go"}, "path"},
		{map[string]any{"command": "ls"}, "command"},
		{map[string]any{"query": "ls"}, "query"},
		{map[string]any{"file_path": "a.go", "command": "ls"}, "file_path"},
		{map[string]any{}, ""},
		{nil, ""},
		{map[string]any{"random_key": "x"}, ""},
	}
	for _, tc := range cases {
		k, _ := primaryArg(tc.input)
		if k != tc.wantK {
			t.Errorf("primaryArg(%v) key = %q, want %q", tc.input, k, tc.wantK)
		}
	}
}

// ─── clipTo ──────────────────────────────────────────────────

func TestClipTo_unicodeSafe(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdefgh", 5, "abcd…"},
		{"中文测试abc", 4, "中文测…"},
		{"", 5, ""},
		{"x", 0, ""},
	}
	for _, tc := range cases {
		got := clipTo(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("clipTo(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
