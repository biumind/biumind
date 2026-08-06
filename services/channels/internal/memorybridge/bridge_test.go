package memorybridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecall_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no auth", 401)
			return
		}
		if r.URL.Path != "/v1/memory/recall" {
			http.Error(w, "bad path", 404)
			return
		}
		q := r.URL.Query()
		if q.Get("project_id") != "p1" {
			t.Errorf("project_id: %v", q.Get("project_id"))
		}
		if q.Get("q") != "vim" {
			t.Errorf("q: %v", q.Get("q"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"memories": []map[string]any{
				{"id": "m1", "kind": "recall", "content": "uses vimwiki",
					"salience": 0.5, "score": 2.39},
			},
			"mode":  "hybrid",
			"query": "vim",
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "tok", "p1")
	hits, mode, err := b.Recall(context.Background(), "vim")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if mode != "hybrid" {
		t.Errorf("mode: %s", mode)
	}
	if len(hits) != 1 || hits[0].Content != "uses vimwiki" {
		t.Errorf("hits: %+v", hits)
	}
	if hits[0].Score < 2 {
		t.Errorf("score: %v", hits[0].Score)
	}
}

func TestRecall_NilBridgeReturnsNil(t *testing.T) {
	var b *Bridge
	hits, mode, err := b.Recall(context.Background(), "anything")
	if err != nil || hits != nil || mode != "" {
		t.Errorf("nil bridge should be silent no-op: %v / %v / %v", hits, mode, err)
	}
}

func TestRecall_EmptyQueryReturnsNil(t *testing.T) {
	b := New("http://x", "t", "p")
	hits, mode, err := b.Recall(context.Background(), "   ")
	if err != nil || hits != nil || mode != "" {
		t.Errorf("empty query should be silent no-op: %v / %v / %v", hits, mode, err)
	}
}

func TestRecall_MissingConfigErrors(t *testing.T) {
	b := New("", "t", "p")
	_, _, err := b.Recall(context.Background(), "q")
	if err == nil {
		t.Error("expected error on missing brain URL")
	}
}

func TestRecall_BrainErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	b := New(srv.URL, "tok", "p1")
	_, _, err := b.Recall(context.Background(), "q")
	if err == nil {
		t.Error("expected error on 5xx")
	}
}

func TestRecall_RespectsLimit(t *testing.T) {
	var seenLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenLimit = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"memories": []any{},
			"mode":     "lexical",
		})
	}))
	defer srv.Close()
	b := New(srv.URL, "tok", "p")
	b.Limit = 12
	_, _, err := b.Recall(context.Background(), "anything")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if seenLimit != "12" {
		t.Errorf("limit param: got %q, want 12", seenLimit)
	}
}
