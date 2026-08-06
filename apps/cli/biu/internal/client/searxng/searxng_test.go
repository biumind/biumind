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
		_ = json.NewEncoder(w).Encode(apiResp{
			Results: []struct {
				Title   string  `json:"title"`
				URL     string  `json:"url"`
				Content string  `json:"content"`
				Score   float64 `json:"score"`
				Engine  string  `json:"engine"`
			}{
				{Title: "BiuMind", URL: "https://biu.app", Content: "snippet", Score: 0.9, Engine: "google"},
			},
		})
	}))
	defer ts.Close()
	c := New(ts.URL)
	r, err := c.Search(context.Background(), "biumind", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[0].URL != "https://biu.app" {
		t.Errorf("got %+v", r)
	}
}

func TestSearchNoConfig(t *testing.T) {
	c := New("")
	if _, err := c.Search(context.Background(), "x", 5); err == nil {
		t.Fatal("expected error")
	}
}
