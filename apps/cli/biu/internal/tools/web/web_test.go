package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestBashRunsAndStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	var lines []string
	env := &engine.ToolEnv{
		OnProgress: func(p engine.ProgressData) {
			if l, ok := p["line"].(string); ok {
				lines = append(lines, l)
			}
		},
	}
	out, err := BashTool{}.Call(context.Background(), map[string]any{
		"command": "echo hello && echo world",
	}, env)
	if err != nil || out.IsError {
		t.Fatalf("bash: %+v %v", out, err)
	}
	body := flatten(out)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("body missing output: %s", body)
	}
	if len(lines) < 2 {
		t.Errorf("expected ≥2 streamed lines, got %d", len(lines))
	}
}

func TestBashSurfacesNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	out, _ := BashTool{}.Call(context.Background(), map[string]any{
		"command": "exit 7",
	}, &engine.ToolEnv{})
	if !out.IsError {
		t.Errorf("non-zero exit must mark IsError")
	}
	if !strings.Contains(flatten(out), "exit=7") {
		t.Errorf("body missing exit code: %s", flatten(out))
	}
}

func TestWebFetchHTMLToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte(`<html><head><style>x</style></head><body><h1>Hi</h1><script>bad</script><p>Para.</p></body></html>`))
	}))
	defer srv.Close()
	out, _ := WebFetchTool{}.Call(context.Background(), map[string]any{
		"url": srv.URL,
	}, nil)
	body := flatten(out)
	if strings.Contains(body, "<script") || strings.Contains(body, "<style") {
		t.Errorf("html not stripped: %s", body)
	}
	if strings.Contains(body, "bad") || strings.Contains(body, "{x}") {
		t.Errorf("script/style content leaked: %s", body)
	}
	if !strings.Contains(body, "Hi") || !strings.Contains(body, "Para.") {
		t.Errorf("text missing: %s", body)
	}
}

func TestWebFetchSurfaces4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	out, _ := WebFetchTool{}.Call(context.Background(), map[string]any{
		"url": srv.URL,
	}, nil)
	if !out.IsError || !strings.Contains(out.SoftError, "404") {
		t.Errorf("404 must soft-error: %+v", out)
	}
}

type stubProvider struct{}

func (stubProvider) WebSearch(_ context.Context, q string, n int) ([]SearchResult, error) {
	if q == "boom" {
		return nil, errors.New("provider down")
	}
	return []SearchResult{
		{Title: "Result A", URL: "https://a", Snippet: "snippet a"},
		{Title: "Result B", URL: "https://b", Snippet: "snippet b"},
	}, nil
}

func TestWebSearchFormatsResults(t *testing.T) {
	tool := WebSearchTool{Provider: stubProvider{}}
	out, _ := tool.Call(context.Background(), map[string]any{
		"query": "go modules",
	}, nil)
	body := flatten(out)
	if !strings.Contains(body, "Result A") || !strings.Contains(body, "https://a") {
		t.Errorf("missing result a: %s", body)
	}
	if !strings.Contains(body, "1.") || !strings.Contains(body, "2.") {
		t.Errorf("expected numbered list: %s", body)
	}
}

func TestWebSearchMissingProvider(t *testing.T) {
	out, _ := WebSearchTool{}.Call(context.Background(), map[string]any{"query": "x"}, nil)
	if !out.IsError || !strings.Contains(out.SoftError, "provider") {
		t.Errorf("missing provider should soft-error: %+v", out)
	}
}

type stubLSP struct{}

func (stubLSP) LSP(_ context.Context, req LSPRequest) (any, error) {
	return map[string]any{"got": req.Operation}, nil
}

func TestLSPHappyPath(t *testing.T) {
	out, _ := LSPTool{Backend: stubLSP{}}.Call(context.Background(), map[string]any{
		"operation": "hover", "filePath": "x.go", "line": float64(10), "character": float64(3),
	}, nil)
	if out.IsError || !strings.Contains(flatten(out), "hover") {
		t.Errorf("lsp result wrong: %+v", out)
	}
}

func TestLSPNoBackend(t *testing.T) {
	out, _ := LSPTool{}.Call(context.Background(), map[string]any{
		"operation": "hover",
	}, nil)
	if !out.IsError {
		t.Errorf("no backend must soft-error")
	}
}

func flatten(p *engine.ToolResultPayload) string {
	out := ""
	for _, b := range p.Content {
		out += b.Text
	}
	return out
}
