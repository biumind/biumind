package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

type fakeProvider struct {
	out string
	err error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Frame, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan llm.Frame, 2)
	go func() {
		defer close(ch)
		ch <- llm.Frame{Kind: llm.KindDelta, Text: f.out}
		ch <- llm.Frame{Kind: llm.KindEnd}
	}()
	return ch, nil
}

func TestFromTextWithLLM_NilProviderFallsBackToHeuristic(t *testing.T) {
	res, err := FromTextWithLLM(context.Background(), nil, "", "see #rust and [[Ownership]]")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	kinds := map[string]int{}
	for _, c := range res.Candidates {
		kinds[c.Kind]++
	}
	if kinds["tag"] != 1 || kinds["concept"] != 1 {
		t.Errorf("heuristic-only fallback wrong: %+v", res.Candidates)
	}
}

func TestFromTextWithLLM_MergesAndDedupes(t *testing.T) {
	// LLM returns: rust (already heuristic), Borrow Checker (new concept),
	// Alice (new person), and a relation.
	fp := &fakeProvider{out: `{
		"nodes": [
			{"kind":"tag","name":"rust","summary":"systems lang"},
			{"kind":"concept","name":"Borrow Checker","summary":"compile-time alias rules"},
			{"kind":"person","name":"alice","summary":"reviewer"}
		],
		"relations": [
			{"src":"rust","dst":"Borrow Checker","relation":"related_to","weight":0.9}
		]
	}`}
	res, err := FromTextWithLLM(context.Background(), fp, "claude",
		"see #rust and [[Borrow Checker]] reviewed by Alice")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	kinds := map[string]int{}
	for _, c := range res.Candidates {
		kinds[c.Kind]++
	}
	// rust: heuristic-only (kept). Borrow Checker: heuristic via wikilink + LLM (deduped).
	// alice: LLM-only (added).
	if kinds["tag"] != 1 || kinds["concept"] != 1 || kinds["person"] != 1 {
		t.Errorf("merge counts wrong: %+v", kinds)
	}
	if len(res.Relations) != 1 || res.Relations[0].Relation != "related_to" {
		t.Errorf("relations: %+v", res.Relations)
	}
}

func TestFromTextWithLLM_StripsCodeFence(t *testing.T) {
	fp := &fakeProvider{out: "```json\n{\"nodes\":[{\"kind\":\"tag\",\"name\":\"x\"}],\"relations\":[]}\n```\n"}
	res, _ := FromTextWithLLM(context.Background(), fp, "", "anything")
	tagCount := 0
	for _, c := range res.Candidates {
		if c.Kind == "tag" && c.Name == "x" {
			tagCount++
		}
	}
	if tagCount != 1 {
		t.Errorf("code-fence unwrap failed: %+v", res.Candidates)
	}
}

func TestFromTextWithLLM_ProviderErrorFallsBackGracefully(t *testing.T) {
	fp := &fakeProvider{err: errBoom}
	res, err := FromTextWithLLM(context.Background(), fp, "", "see #rust")
	if err == nil || !strings.Contains(err.Error(), "llm-ner") {
		t.Errorf("want llm-ner err, got %v", err)
	}
	// Heuristic candidates still present.
	if len(res.Candidates) == 0 {
		t.Errorf("heuristic fallback should still emit candidates")
	}
}

func TestFromTextWithLLM_RejectsBogusKinds(t *testing.T) {
	fp := &fakeProvider{out: `{"nodes":[
		{"kind":"WIDGET","name":"shouldn't appear"},
		{"kind":"concept","name":"fine"}
	],"relations":[]}`}
	res, _ := FromTextWithLLM(context.Background(), fp, "", "anything")
	for _, c := range res.Candidates {
		if c.Kind == "WIDGET" || c.Name == "shouldn't appear" {
			t.Errorf("bogus kind leaked: %+v", c)
		}
	}
}

var errBoom = stringError("boom")

type stringError string

func (s stringError) Error() string { return string(s) }
