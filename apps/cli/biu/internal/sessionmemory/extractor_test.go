package sessionmemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// fakeSummer captures inputs + returns a canned response. Lets us
// verify the extractor calls the summariser at the right times
// without spinning up a real LLM.
type fakeSummer struct {
	calls    int
	lastMsgs []state.Message
	lastInst string
	resp     string
	err      error
}

func (f *fakeSummer) Summarise(ctx context.Context, msgs []state.Message, inst string) (string, error) {
	f.calls++
	f.lastMsgs = msgs
	f.lastInst = inst
	if f.err != nil {
		return "", f.err
	}
	return f.resp, nil
}

// ─── parseExtractionResponse ─────────────────────────────────

func TestParseExtractionResponse_basic(t *testing.T) {
	resp := `<<<SECTION:Session Title>>>
biu plugin loader refactor
<<<SECTION:Current State>>>
Refactoring loader.go to use embed.FS.
Next: tests.
<<<SECTION:Workflow>>>
go build ./...
go test ./internal/plugins/...
<<<END>>>
trailing commentary should be dropped
`
	got := parseExtractionResponse(resp)
	if got["Session Title"] != "biu plugin loader refactor" {
		t.Errorf("Session Title = %q", got["Session Title"])
	}
	if !strings.Contains(got["Current State"], "Refactoring loader.go") {
		t.Errorf("Current State missing body: %q", got["Current State"])
	}
	if !strings.Contains(got["Workflow"], "go test") {
		t.Errorf("Workflow missing body: %q", got["Workflow"])
	}
	if _, ok := got["dropped"]; ok {
		t.Error("trailing line should not have become a section")
	}
}

func TestParseExtractionResponse_missingEndMarker(t *testing.T) {
	resp := `<<<SECTION:Current State>>>
some body content
no END marker — last section should still flush
`
	got := parseExtractionResponse(resp)
	if !strings.Contains(got["Current State"], "no END marker") {
		t.Errorf("missing tail body: %q", got["Current State"])
	}
}

func TestParseExtractionResponse_emptyResponse(t *testing.T) {
	got := parseExtractionResponse("")
	if len(got) != 0 {
		t.Errorf("empty response should give empty map, got %v", got)
	}
}

func TestParseExtractionResponse_duplicateSectionsLastWins(t *testing.T) {
	resp := `<<<SECTION:Current State>>>
first
<<<SECTION:Current State>>>
second
<<<END>>>`
	got := parseExtractionResponse(resp)
	if got["Current State"] != "second" {
		t.Errorf("dup → last should win, got %q", got["Current State"])
	}
}

// ─── countToolUses ───────────────────────────────────────────

func TestCountToolUses(t *testing.T) {
	msgs := []state.Message{
		{Role: state.RoleAssistant, Content: []state.ContentBlock{
			{Type: state.ContentText, Text: "thinking"},
			{Type: state.ContentToolUse},
			{Type: state.ContentToolUse},
		}},
		{Role: state.RoleUser, Content: []state.ContentBlock{
			{Type: state.ContentToolResult},
		}},
		{Role: state.RoleAssistant, Content: []state.ContentBlock{
			{Type: state.ContentToolUse},
		}},
	}
	if got := countToolUses(msgs); got != 3 {
		t.Errorf("countToolUses = %d, want 3", got)
	}
}

// ─── Extractor cadence ───────────────────────────────────────

func TestExtractor_firstRunNeedsMessageThreshold(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("ext-first")
	summer := &fakeSummer{resp: validResp()}
	ext := NewExtractor(mem, summer, ExtractorConfig{
		MinToolCallsBetweenRuns: 5,
		MinMessagesForFirstRun:  6,
	})
	// Only 3 messages — below threshold; should not run.
	msgs := makeMessages(3, 0)
	ran, err := ext.MaybeRun(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("first run should be gated by message threshold")
	}
	if summer.calls != 0 {
		t.Errorf("summariser should not be called yet, got %d calls", summer.calls)
	}

	// Now satisfy the threshold.
	ran, err = ext.MaybeRun(context.Background(), makeMessages(8, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("threshold met → should run")
	}
}

func TestExtractor_subsequentRunsNeedToolCallDelta(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("ext-cadence")
	summer := &fakeSummer{resp: validResp()}
	ext := NewExtractor(mem, summer, ExtractorConfig{
		MinToolCallsBetweenRuns: 5,
		MinMessagesForFirstRun:  3,
	})
	// First run.
	_, err := ext.MaybeRun(context.Background(), makeMessages(10, 5))
	if err != nil {
		t.Fatal(err)
	}
	if summer.calls != 1 {
		t.Fatalf("first run expected, calls = %d", summer.calls)
	}
	// Cadence: 5 + 4 < 10 cumulative tool uses → should NOT run.
	_, _ = ext.MaybeRun(context.Background(), makeMessages(10, 9))
	if summer.calls != 1 {
		t.Errorf("4 more tool calls < threshold (5), should not re-run; calls = %d", summer.calls)
	}
	// 5 + 6 = 11 cumulative tool uses → delta 6 ≥ 5 → run.
	_, _ = ext.MaybeRun(context.Background(), makeMessages(10, 11))
	if summer.calls != 2 {
		t.Errorf("delta met → second run; calls = %d", summer.calls)
	}
}

func TestExtractor_summariserErrorPropagates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("ext-err")
	summer := &fakeSummer{err: errors.New("boom")}
	ext := NewExtractor(mem, summer, ExtractorConfig{MinMessagesForFirstRun: 1})

	ran, err := ext.MaybeRun(context.Background(), makeMessages(2, 0))
	if ran {
		t.Error("ran flag should be false on error")
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err should propagate, got %v", err)
	}
}

func TestExtractor_unparseableResponseFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("ext-parse")
	summer := &fakeSummer{resp: "no section markers here, just prose"}
	ext := NewExtractor(mem, summer, ExtractorConfig{MinMessagesForFirstRun: 1})

	_, err := ext.MaybeRun(context.Background(), makeMessages(2, 0))
	if err == nil {
		t.Error("unparseable response should error")
	}
}

func TestExtractor_writesSectionsToFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem, _ := Load("ext-write")
	summer := &fakeSummer{resp: validResp()}
	ext := NewExtractor(mem, summer, ExtractorConfig{MinMessagesForFirstRun: 1})

	if _, err := ext.MaybeRun(context.Background(), makeMessages(2, 0)); err != nil {
		t.Fatal(err)
	}
	again, _ := Load("ext-write")
	cs, ok := again.FindSection("Current State")
	if !ok || !strings.Contains(cs.Body, "extracted body") {
		t.Errorf("Current State not persisted: %+v", cs)
	}
}

func TestExtractor_nilSafe(t *testing.T) {
	if ext := NewExtractor(nil, &fakeSummer{}, ExtractorConfig{}); ext != nil {
		t.Error("nil mem should produce nil extractor")
	}
	if ext := NewExtractor(&SessionMemory{}, nil, ExtractorConfig{}); ext != nil {
		t.Error("nil summariser should produce nil extractor")
	}
	var ext *Extractor
	ran, err := ext.MaybeRun(context.Background(), nil)
	if ran || err != nil {
		t.Errorf("nil receiver MaybeRun should be a silent no-op")
	}
}

// ─── helpers ─────────────────────────────────────────────────

func validResp() string {
	return `<<<SECTION:Session Title>>>
test extraction
<<<SECTION:Current State>>>
extracted body
<<<END>>>`
}

func makeMessages(n, toolUses int) []state.Message {
	msgs := make([]state.Message, n)
	for i := 0; i < n; i++ {
		msgs[i] = state.Message{Role: state.RoleAssistant, Content: nil}
	}
	if toolUses > 0 {
		blocks := make([]state.ContentBlock, toolUses)
		for i := range blocks {
			blocks[i] = state.ContentBlock{Type: state.ContentToolUse}
		}
		// Stuff all tool uses into the first message — count is
		// what matters for cadence, not distribution.
		msgs[0].Content = blocks
	}
	return msgs
}
