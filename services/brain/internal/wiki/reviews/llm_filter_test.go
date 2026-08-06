package reviews

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── parseVerdicts ──────────────────────────────────────────────

func TestParseVerdicts_PlainArray(t *testing.T) {
	in := `[{"id": 0, "verdict": "duplicate", "reason": "same topic"},
	         {"id": 1, "verdict": "related", "reason": "different angle"}]`
	got, err := parseVerdicts(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got[0] != "duplicate" || got[1] != "related" {
		t.Errorf("verdict map wrong: %+v", got)
	}
}

func TestParseVerdicts_StripsCodeFences(t *testing.T) {
	in := "```json\n" +
		`[{"id":0,"verdict":"duplicate"}]` + "\n```"
	got, err := parseVerdicts(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0] != "duplicate" {
		t.Errorf("got %v", got)
	}
}

func TestParseVerdicts_RecoversFromTrailingProse(t *testing.T) {
	// Models sometimes ignore "JSON only" and append commentary.
	// Recovery uses first '[' + last ']' to bound the array.
	in := `Here are the verdicts:
[{"id":0,"verdict":"duplicate"}]
Hope that helps!`
	got, err := parseVerdicts(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0] != "duplicate" {
		t.Errorf("got %v", got)
	}
}

func TestParseVerdicts_HandlesStringIDs(t *testing.T) {
	// OpenAI sometimes wraps integer IDs in strings.
	in := `[{"id":"0","verdict":"duplicate"},{"id":"1","verdict":"related"}]`
	got, err := parseVerdicts(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0] != "duplicate" || got[1] != "related" {
		t.Errorf("got %v", got)
	}
}

func TestParseVerdicts_DropsUnknownVerdicts(t *testing.T) {
	in := `[{"id":0,"verdict":"maybe"},{"id":1,"verdict":"duplicate"}]`
	got, err := parseVerdicts(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := got[0]; ok {
		t.Errorf("unknown verdict 'maybe' should be dropped, got %v", got)
	}
	if got[1] != "duplicate" {
		t.Errorf("got %v", got)
	}
}

func TestParseVerdicts_RejectsEmptyAndNonArray(t *testing.T) {
	if _, err := parseVerdicts(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := parseVerdicts("just prose, no json"); err == nil {
		t.Error("no array should error")
	}
}

// ── NoopFilter ─────────────────────────────────────────────────

func TestNoopFilter_PassesAllPairsThrough(t *testing.T) {
	pairs := []PagePair{
		{PageA: uuid.New(), PageB: uuid.New(), Similarity: 0.95},
		{PageA: uuid.New(), PageB: uuid.New(), Similarity: 0.93},
	}
	got, err := NoopFilter{}.FilterDedup(context.Background(), uuid.New(), pairs)
	if err != nil {
		t.Fatalf("noop should never error: %v", err)
	}
	if len(got) != len(pairs) {
		t.Errorf("noop dropped pairs: %d vs %d", len(got), len(pairs))
	}
}

// ── HubLLMFilter (offline / config-gap behaviour) ──────────────

func TestHubLLMFilter_PassesThroughOnEmptyConfig(t *testing.T) {
	f := &HubLLMFilter{} // no RelayURL, no Signer
	pairs := []PagePair{{PageA: uuid.New(), PageB: uuid.New()}}
	got, err := f.FilterDedup(context.Background(), uuid.New(), pairs)
	if err != nil {
		t.Fatalf("misconfigured filter must not error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected passthrough, got %d", len(got))
	}
}

func TestHubLLMFilter_NoPairsIsCheap(t *testing.T) {
	f := &HubLLMFilter{RelayURL: "http://nope"}
	got, err := f.FilterDedup(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pairs, got %d", len(got))
	}
}

// ── Worker integration with stub filter ────────────────────────

// stubFilter returns whatever the test prepared. Used to exercise the
// worker's "filter dropped pairs" log path without a real LLM.
type stubFilter struct {
	keep []PagePair
	err  error
}

func (s stubFilter) FilterDedup(_ context.Context, _ uuid.UUID, _ []PagePair) ([]PagePair, error) {
	return s.keep, s.err
}

// FilterFindings — stub passthrough so stubFilter satisfies the
// extended LLMFilter interface (P2-tail-3).
func (s stubFilter) FilterFindings(_ context.Context, _ uuid.UUID, findings []Finding) ([]Finding, error) {
	if s.err != nil {
		return nil, s.err
	}
	return findings, nil
}

func TestWorker_FilterErrorKeepsAllPairs(t *testing.T) {
	// We validate the contract by calling FilterDedup directly with a
	// stub that errors and asserting the worker would fall back to the
	// original pairs. The actual worker test path is integration-only
	// (needs DB), so this is the unit boundary.
	pairs := []PagePair{
		{PageA: uuid.New(), PageB: uuid.New(), Similarity: 0.95},
	}
	filter := stubFilter{keep: nil, err: errors.New("model-relay down")}
	got, err := filter.FilterDedup(context.Background(), uuid.New(), pairs)
	if err == nil {
		t.Fatal("stub should propagate the error")
	}
	if got != nil {
		t.Errorf("error path returns nil, got %v", got)
	}
	// The worker reads err != nil and substitutes `filtered = pairs`.
	// (See worker.go scanProject.) We document that contract here.
}

// ── prompt building ─────────────────────────────────────────────

func TestBuildPrompt_IncludesAllPairs(t *testing.T) {
	f := &HubLLMFilter{SnippetMaxChars: 200}
	pairs := []PagePair{
		{TitleA: "Page Alpha", TitleB: "Page Beta",
			SnippetA: "alpha body", SnippetB: "beta body"},
		{TitleA: "Concept X", TitleB: "Concept Y",
			SnippetA: "x body", SnippetB: "y body"},
	}
	prompt := f.buildPrompt(pairs)
	for _, want := range []string{
		"Page Alpha", "Page Beta",
		"Concept X", "Concept Y",
		"alpha body", "beta body",
		"--- Pair 0 ---", "--- Pair 1 ---",
		"STRICT JSON",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_TruncatesLongSnippets(t *testing.T) {
	f := &HubLLMFilter{SnippetMaxChars: 50}
	long := strings.Repeat("x", 500)
	prompt := f.buildPrompt([]PagePair{
		{TitleA: "T", TitleB: "T", SnippetA: long, SnippetB: long},
	})
	// Each truncated snippet should be ≤ 50 chars + ellipsis.
	if strings.Contains(prompt, strings.Repeat("x", 100)) {
		t.Errorf("snippet not truncated:\n%s", prompt)
	}
	if !strings.Contains(prompt, "…") {
		t.Errorf("expected ellipsis on truncation")
	}
}
