package repl

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

// ─── /diff ──────────────────────────────────────────────────

func TestSlashDiff_outsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Chdir(t.TempDir())
	got := model{}.handleDiff([]string{"/diff"})
	if !strings.Contains(got, "not inside a git work tree") {
		t.Errorf("non-repo cwd should report not-in-tree, got %q", got)
	}
}

func TestSlashDiff_buildArgs(t *testing.T) {
	cases := []struct {
		parts     []string
		wantArgs  []string
		wantLabel string
	}{
		{[]string{"/diff"}, nil, "(working tree vs HEAD)"},
		{[]string{"/diff", "staged"}, []string{"--staged"}, "(staged vs HEAD)"},
		{[]string{"/diff", "cached"}, []string{"--staged"}, "(staged vs HEAD)"},
		{[]string{"/diff", "main"}, []string{"main...HEAD"}, "(vs main, range)"},
		{[]string{"/diff", "--cached"}, []string{"--cached"}, "(custom: --cached)"},
	}
	for _, tc := range cases {
		args, label := buildDiffArgs(tc.parts)
		if !strSliceEq(args, tc.wantArgs) {
			t.Errorf("buildDiffArgs(%v) args = %v, want %v", tc.parts, args, tc.wantArgs)
		}
		if label != tc.wantLabel {
			t.Errorf("buildDiffArgs(%v) label = %q, want %q", tc.parts, label, tc.wantLabel)
		}
	}
}

// ─── /copy ──────────────────────────────────────────────────

func TestSlashCopy_emptyHistory(t *testing.T) {
	got := model{}.handleCopy([]string{"/copy"})
	if !strings.Contains(got, "nothing to copy") {
		t.Errorf("empty history should be reported: %s", got)
	}
}

func TestSlashCopy_lastAssistant(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "user", Content: "ask"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "follow up"},
			{Role: "assistant", Content: "second answer"},
		},
	}
	body, _ := m.findCopyBody([]string{"/copy"})
	if body != "second answer" {
		t.Errorf("got %q, want last assistant", body)
	}
}

func TestSlashCopy_codeFence(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "assistant", Content: "Here you go:\n\n```go\nfunc x() {}\n```\n"},
		},
	}
	body, _ := m.findCopyBody([]string{"/copy", "code"})
	if strings.TrimSpace(body) != "func x() {}" {
		t.Errorf("got %q, want extracted code", body)
	}
}

func TestSlashCopy_codeFenceMissing(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "assistant", Content: "no fences here"},
		},
	}
	body, label := m.findCopyBody([]string{"/copy", "code"})
	if body != "" {
		t.Errorf("body should be empty when no fence: %q", body)
	}
	if !strings.Contains(label, "no fenced code block") {
		t.Errorf("label = %q", label)
	}
}

func TestSlashCopy_patternSearch(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "assistant", Content: "first answer about cats"},
			{Role: "assistant", Content: "second answer about DOGS"},
			{Role: "assistant", Content: "third answer with no animals"},
		},
	}
	// Newest-first search; "dog" matches the second-from-last
	// assistant turn (case-insensitive).
	body, _ := m.findCopyBody([]string{"/copy", "dog"})
	if !strings.Contains(body, "DOGS") {
		t.Errorf("got %q, expected pattern hit", body)
	}
}

func TestSlashCopy_patternMiss(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "assistant", Content: "irrelevant"},
		},
	}
	body, label := m.findCopyBody([]string{"/copy", "nope"})
	if body != "" {
		t.Errorf("miss should return empty body, got %q", body)
	}
	if !strings.Contains(label, `"nope"`) {
		t.Errorf("label should quote pattern: %q", label)
	}
}

func TestLastFencedCode_picksLast(t *testing.T) {
	text := "first\n\n```go\nA\n```\n\nbody\n\n```python\nB\n```\n"
	got := lastFencedCode(text)
	if strings.TrimSpace(got) != "B" {
		t.Errorf("expected last block 'B', got %q", got)
	}
}

func TestLastFencedCode_noFences(t *testing.T) {
	if got := lastFencedCode("plain text"); got != "" {
		t.Errorf("no fences should return empty, got %q", got)
	}
}

// ─── /stats ─────────────────────────────────────────────────

func TestSlashStats_basicShape(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "user", Content: "a"},
			{Role: "assistant", Content: "b"},
			{Role: "user", Content: "c"},
		},
	}
	got := m.handleStats([]string{"/stats"})
	for _, want := range []string{"this session", "messages:", "2 user", "1 assistant"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in stats: %s", want, got)
		}
	}
}

func TestCountMessages(t *testing.T) {
	hist := []client.Message{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "system"},
		{Role: "tool"},
		{Role: "user"},
	}
	user, asst := countMessages(hist)
	if user != 2 || asst != 1 {
		t.Errorf("countMessages = (%d, %d), want (2, 1)", user, asst)
	}
}

// ─── helpers ────────────────────────────────────────────────

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
