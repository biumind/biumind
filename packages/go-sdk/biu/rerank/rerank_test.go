package rerank

import (
	"context"
	"testing"
)

func TestStub_OrdersByOverlap(t *testing.T) {
	r := NewStub()
	query := "transformer attention mechanism"
	docs := []string{
		"cooking pasta recipe",                      // 0 shared
		"the transformer attention mechanism",       // 3 shared → top
		"attention is all you need",                 // 1 shared
	}
	scores, err := r.Rerank(context.Background(), query, docs, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(scores) != 3 {
		t.Fatalf("want 3 scores, got %d", len(scores))
	}
	// Top hit must be doc 1 (shares transformer + attention + mechanism).
	if scores[0].Index != 1 {
		t.Errorf("top index = %d, want 1 (most overlap); scores=%v", scores[0].Index, scores)
	}
	if scores[0].Score <= scores[2].Score {
		t.Errorf("top score %.3f not > cooking score %.3f", scores[0].Score, scores[2].Score)
	}
	// Cooking doc (index 0) should be last with score 0.
	if scores[len(scores)-1].Index != 0 {
		t.Errorf("last index = %d, want 0 (no overlap); scores=%v", scores[len(scores)-1].Index, scores)
	}
}

func TestStub_Deterministic(t *testing.T) {
	r := NewStub()
	docs := []string{"a b c", "b c d", "x y z"}
	a, _ := r.Rerank(context.Background(), "b c", docs, 0)
	b, _ := r.Rerank(context.Background(), "b c", docs, 0)
	if len(a) != len(b) {
		t.Fatalf("len mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestStub_TopN(t *testing.T) {
	r := NewStub()
	docs := []string{"a", "a b", "a b c", "x"}
	scores, _ := r.Rerank(context.Background(), "a b c", docs, 2)
	if len(scores) != 2 {
		t.Fatalf("topN=2 want 2 scores, got %d", len(scores))
	}
}

func TestStub_EmptyDocs(t *testing.T) {
	r := NewStub()
	scores, _ := r.Rerank(context.Background(), "q", nil, 0)
	if scores != nil {
		t.Fatalf("empty docs want nil, got %v", scores)
	}
}

func TestCohere_NewRequiresAPIKey(t *testing.T) {
	if _, err := NewCohere(CohereConfig{}); err == nil {
		t.Fatal("NewCohere with empty APIKey should error")
	}
}

func TestCohere_Defaults(t *testing.T) {
	r, err := NewCohere(CohereConfig{APIKey: "k"})
	if err != nil {
		t.Fatalf("NewCohere: %v", err)
	}
	if r.Model() != "BAAI/bge-reranker-v2-m3" {
		t.Errorf("default model = %q, want BAAI/bge-reranker-v2-m3", r.Model())
	}
	cr := r.(*cohereReranker)
	if cr.cfg.BaseURL != "https://api.cohere.ai/v1" {
		t.Errorf("default base = %q, want https://api.cohere.ai/v1", cr.cfg.BaseURL)
	}
}
