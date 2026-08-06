package webclip

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/ingest"
)

func TestParseHTML(t *testing.T) {
	a := New()
	body, _ := json.Marshal(map[string]any{
		"html": "<html><head><title>BiuMind</title></head><body><p>One paragraph</p><p>Two</p></body></html>",
		"url":  "https://example.com/p",
	})
	out, err := a.Invoke(context.Background(), "parse_html", body)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	doc := out.(*ingest.ParsedDoc)
	if doc.Title != "BiuMind" {
		t.Errorf("title: %q", doc.Title)
	}
	if len(doc.Chunks) < 2 {
		t.Errorf("want 2+ blocks, got %d: %+v", len(doc.Chunks), doc.Chunks)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Remote</title></head><body><p>fetched</p></body></html>`))
	}))
	defer srv.Close()

	a := New()
	body, _ := json.Marshal(map[string]any{"url": srv.URL})
	out, err := a.Invoke(context.Background(), "fetch", body)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.(*ingest.ParsedDoc).Title != "Remote" {
		t.Errorf("fetched doc: %+v", out)
	}
}

func TestFetchRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	a := New()
	body, _ := json.Marshal(map[string]any{"url": srv.URL})
	if _, err := a.Invoke(context.Background(), "fetch", body); err == nil ||
		!strings.Contains(err.Error(), "410") {
		t.Errorf("want 410 err, got %v", err)
	}
}
