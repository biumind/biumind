package searxng

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "biumind" {
			http.Error(w, "bad q", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(apiResponse{
			Query: "biumind",
			Results: []struct {
				Title   string  `json:"title"`
				URL     string  `json:"url"`
				Content string  `json:"content"`
				Score   float64 `json:"score"`
				Engine  string  `json:"engine"`
			}{
				{Title: "BiuMind", URL: "https://biu.app", Content: "snippet 1", Score: 0.9, Engine: "google"},
				{Title: "Other", URL: "https://x.example", Content: "snippet 2", Score: 0.8},
			},
		})
	}))
	defer ts.Close()

	c := New(ts.URL)
	r, err := c.Search(context.Background(), "biumind", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 || r[0].URL != "https://biu.app" || r[1].Snippet != "snippet 2" {
		t.Errorf("got %+v", r)
	}
}

func TestSearchLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(apiResponse{
			Results: make([]struct {
				Title   string  `json:"title"`
				URL     string  `json:"url"`
				Content string  `json:"content"`
				Score   float64 `json:"score"`
				Engine  string  `json:"engine"`
			}, 5),
		})
	}))
	defer ts.Close()

	c := New(ts.URL)
	r, err := c.Search(context.Background(), "x", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 {
		t.Errorf("len=%d", len(r))
	}
}

func TestSearchEmptyURL(t *testing.T) {
	c := New("")
	if _, err := c.Search(context.Background(), "x", 5); err == nil {
		t.Fatal("expected error on empty URL")
	}
}
