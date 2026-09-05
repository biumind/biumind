package research

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCleanThinking(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Hello world", "Hello world"},
		{"closed think", "<think>scratch</think>final answer", "final answer"},
		{"closed thinking variant", "<thinking>x</thinking>real", "real"},
		{"unclosed leaks rest", "Real text<think>oops the rest is private", "Real text"},
		{"multiple closed", "<think>a</think>between<think>b</think>after", "betweenafter"},
		{"trims whitespace", "  \n<think>x</think>  body  ", "body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanThinking(c.in)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestResumePhase — the resume decision is a pure function of persisted
// task state, so it can be exercised without a DB. Each branch maps to
// a real crash window in the pipeline:
//
//	page_id set       → crash after Complete raced the recover pick-up
//	synthesis present → crash mid-save (LLM done, page not written)
//	web_results set   → crash mid-synthesis (search done, LLM not run)
//	nothing           → crash mid-search, or a fresh queued task
func TestResumePhase(t *testing.T) {
	pid := uuid.New()
	webHits := []WebHit{{Title: "x", URL: "https://x"}}
	page := uuid.New()

	cases := []struct {
		name string
		task *Task
		want phase
	}{
		{"empty queued", &Task{ID: pid, Status: StatusQueued}, phaseSearch},
		{"searching no results", &Task{ID: pid, Status: StatusSearching}, phaseSearch},
		{"has web results", &Task{ID: pid, Status: StatusSynthesizing, WebResults: webHits}, phaseSynthesize},
		{"has synthesis", &Task{ID: pid, Status: StatusSaving, WebResults: webHits, Synthesis: "## body"}, phaseSave},
		{"has page id", &Task{ID: pid, Status: StatusSaving, PageID: &page, Synthesis: "## body"}, phaseDone},
		{"whitespace synthesis ignored", &Task{ID: pid, Status: StatusSynthesizing, WebResults: webHits, Synthesis: "   \n  "}, phaseSynthesize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resumePhase(c.task); got != c.want {
				t.Fatalf("resumePhase = %v, want %v", got, c.want)
			}
		})
	}
}

// TestTaskJSONSourceReview — taskJSON 透出 source_review_id：review →
// research 入口创建的任务要能让 UI 看到来源审阅项；手动创建的任务
// 该字段缺省（omitempty）。
func TestTaskJSONSourceReview(t *testing.T) {
	reviewID := uuid.New()
	task := &Task{
		ID:             uuid.New(),
		ProjectID:      uuid.New(),
		Topic:          "t",
		Queries:        []string{},
		Status:         StatusQueued,
		SourceReviewID: &reviewID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	raw, err := json.Marshal(taskJSON(task))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if got := m["source_review_id"]; got != reviewID.String() {
		t.Fatalf("source_review_id = %v, want %s", got, reviewID)
	}

	// 手动任务（无来源 review）不出这个 key。
	task.SourceReviewID = nil
	raw, err = json.Marshal(taskJSON(task))
	if err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["source_review_id"]; ok {
		t.Fatal("source_review_id should be omitted when nil")
	}
}

// TestNewOrchestratorDefaults — MaxConcurrent 0 clamps to 4 and the sem
// channel is allocated so Run never blocks forever on a nil receive.
func TestNewOrchestratorDefaults(t *testing.T) {
	o := NewOrchestrator(nil, nil, nil, nil, nil, Config{})
	if o.maxConcurrent != 4 {
		t.Fatalf("maxConcurrent = %d, want 4", o.maxConcurrent)
	}
	if cap(o.sem) != 4 {
		t.Fatalf("sem cap = %d, want 4", cap(o.sem))
	}
	// Explicit override respected.
	o2 := NewOrchestrator(nil, nil, nil, nil, nil, Config{MaxConcurrent: 2})
	if o2.maxConcurrent != 2 || cap(o2.sem) != 2 {
		t.Fatalf("override not respected: mc=%d cap=%d", o2.maxConcurrent, cap(o2.sem))
	}
}

// TestCheckQualityGate — syntheses that aren't worth a page must be
// rejected before savePage: empty/short bodies, and bodies citing no
// source. Rune counting keeps CJK text fair (a 120-rune Chinese body
// passes even though it's ~360 bytes).
func TestCheckQualityGate(t *testing.T) {
	long := strings.Repeat("a", 200)
	longCN := strings.Repeat("研", minSynthesisRunes)

	cases := []struct {
		name    string
		in      string
		wantErr string // substring; "" means pass
	}{
		{"empty", "", "body too short"},
		{"whitespace only", "  \n\t ", "body too short"},
		{"short with citation", "See [1] for details.", "body too short"},
		{"long but no citation", long, "no [N] source citations"},
		{"long with citation passes", long + " [1]", ""},
		{"exactly min runes with citation passes", longCN + "[2]", ""},
		{"CJK below min runes rejected", strings.Repeat("研", minSynthesisRunes-10) + "[1]", "body too short"},
		{"multi-digit citation counts", long + " see [12] and [3]", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkQualityGate(c.in)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), "synthesis below quality gate") ||
				!strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error %q missing expected substrings", err.Error())
			}
		})
	}
}

// TestReferencesSection — only hits the synthesis actually cites via
// [N] appear under ## References, with their ORIGINAL numbers kept (no
// renumbering) so in-text markers still match the list.
func TestReferencesSection(t *testing.T) {
	hits := []WebHit{
		{Title: "Alpha", URL: "https://a.example", Source: "bing"},
		{Title: "Beta", URL: "https://b.example", Source: "google"},
		{Title: "Gamma", URL: "https://c.example", Source: "bing"},
	}

	cases := []struct {
		name string
		body string
		hits []WebHit
		want string
	}{
		{
			"all cited",
			"Alpha says x [1]. Beta says y [2]. Gamma says z [3].",
			hits,
			"\n\n## References\n\n" +
				"1. [Alpha](https://a.example) — bing\n" +
				"2. [Beta](https://b.example) — google\n" +
				"3. [Gamma](https://c.example) — bing\n",
		},
		{
			"partial citations keep original numbering",
			"Only beta matters here [2].",
			hits,
			"\n\n## References\n\n2. [Beta](https://b.example) — google\n",
		},
		{
			"duplicate citation listed once",
			"Cited twice [1] and again [1].",
			hits,
			"\n\n## References\n\n1. [Alpha](https://a.example) — bing\n",
		},
		{
			"out-of-range citation ignored",
			"Cites a source we don't have [9].",
			hits,
			"",
		},
		{"no citations at all", "no markers here", hits, ""},
		{"no hits", "cites [1] but no hits", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := referencesSection(c.body, c.hits); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestBaseSystemPromptLanguageGate — the synthesis prompt must carry the
// mandatory output-language instruction (Chinese topic → Chinese page).
func TestBaseSystemPromptLanguageGate(t *testing.T) {
	if !strings.Contains(baseSystemPrompt, "MANDATORY OUTPUT LANGUAGE") {
		t.Fatal("baseSystemPrompt missing mandatory output-language section")
	}
	if !strings.Contains(baseSystemPrompt, "same language as the research topic and queries") {
		t.Fatal("baseSystemPrompt missing language-following instruction")
	}
}
