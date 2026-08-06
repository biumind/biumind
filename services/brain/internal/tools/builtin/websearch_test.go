package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/search/searxng"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// fakeSearxNG returns canned JSON shaped like a real SearxNG
// `format=json` response.
func fakeSearxNG(t *testing.T, results []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/search") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query":   r.URL.Query().Get("q"),
				"results": results,
			})
		}))
}

func TestWebSearchDescriptor(t *testing.T) {
	tool := WebSearch(searxng.New(""))
	if tool.Name != "websearch" {
		t.Errorf("name: got %q", tool.Name)
	}
	if !tool.Runtime.AvailableIn(tools.ExecutionCloud) ||
		!tool.Runtime.AvailableIn(tools.ExecutionClient) {
		t.Errorf("expected RuntimeBoth")
	}
	if tool.Invoke == nil {
		t.Fatal("Invoke nil")
	}
}

func TestWebSearchHappyPath(t *testing.T) {
	srv := fakeSearxNG(t, []map[string]any{
		{
			"title":   "Result A",
			"url":     "https://a.example",
			"content": "Snippet A",
			"score":   0.9,
			"engine":  "duckduckgo",
		},
		{
			"title":   "Result B",
			"url":     "https://b.example",
			"content": "Snippet B",
			"score":   0.5,
			"engine":  "google",
		},
	})
	defer srv.Close()

	tool := WebSearch(searxng.New(srv.URL))
	out, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"query":"hello","limit":5}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := out.(map[string]any)
	if m["query"] != "hello" {
		t.Errorf("query echo: %v", m["query"])
	}
	results := m["results"].([]map[string]any)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0]["title"] != "Result A" {
		t.Errorf("first result: %+v", results[0])
	}
	if results[0]["snippet"] != "Snippet A" {
		t.Errorf("snippet projection: %+v", results[0])
	}
}

func TestWebSearchEmptyQueryRejected(t *testing.T) {
	tool := WebSearch(searxng.New("http://unused"))
	_, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"query":""}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected query-required error, got %v", err)
	}
}

func TestWebSearchLimitDefaultAndCap(t *testing.T) {
	// Server records the limit applied by the tool — but the tool
	// asks SearxNG for ALL results and trims locally; the test
	// simulates a bigger result set and asserts cap.
	results := make([]map[string]any, 0, 30)
	for i := 0; i < 30; i++ {
		results = append(results, map[string]any{
			"title": "x", "url": "u", "content": "c",
		})
	}
	srv := fakeSearxNG(t, results)
	defer srv.Close()

	tool := WebSearch(searxng.New(srv.URL))
	// limit=999 should cap to 20
	out, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"query":"x","limit":999}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got := len(out.(map[string]any)["results"].([]map[string]any))
	if got != 20 {
		t.Errorf("expected cap 20, got %d", got)
	}

	// no limit specified → default 8
	out, err = tool.Invoke(context.Background(),
		json.RawMessage(`{"query":"x"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got = len(out.(map[string]any)["results"].([]map[string]any))
	if got != 8 {
		t.Errorf("expected default 8, got %d", got)
	}
}

func TestWebSearchPropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
	defer srv.Close()
	tool := WebSearch(searxng.New(srv.URL))
	_, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"query":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "websearch:") {
		t.Errorf("expected wrapped websearch error, got %v", err)
	}
}
