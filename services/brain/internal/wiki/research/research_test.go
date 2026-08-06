package research

import (
	"testing"

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
