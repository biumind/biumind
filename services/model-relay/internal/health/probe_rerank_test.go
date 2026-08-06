package health

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/dashscope"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

// rerank probe 单测. openai.Adaptor 走 Cohere shape /v1/rerank;
// dashscope.Adaptor 走 dashscope native /api/v1/services/rerank/text-rerank/text-rerank.
// 两条路都覆盖.

func newRerankProbe() *Probe {
	return &Probe{cfg: Config{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func TestRunRerankHTTP_OpenAI_Cohere(t *testing.T) {
	var gotPath, gotAuth, gotQuery string
	var gotDocsLen int
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery = body.Query
		gotDocsLen = len(body.Documents)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "req_x",
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.91},
				{"index": 1, "relevance_score": 0.42},
			},
			"meta": map[string]any{
				"billed_units": map[string]any{"search_units": 1},
			},
		})
	}))
	defer stub.Close()

	p := newRerankProbe()
	res := p.runRerankHTTP(context.Background(), openai.New(),
		[]byte("sk-test"), stub.URL, nil, "bge-reranker", "BAAI/bge-reranker-v2-m3")

	if !res.OK {
		t.Fatalf("expected OK, got: %+v", res)
	}
	if gotPath != "/v1/rerank" {
		t.Errorf("path: %q (Cohere shape 应走 /v1/rerank)", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth: %q", gotAuth)
	}
	if gotQuery != "hello" {
		t.Errorf("query: %q (probe 用 hello 占位)", gotQuery)
	}
	if gotDocsLen != 2 {
		t.Errorf("docs len: %d (probe 默认 2 条)", gotDocsLen)
	}
	if res.Tokens != 1 {
		t.Errorf("Tokens (search_units): %d, want 1", res.Tokens)
	}
}

func TestRunRerankHTTP_DashScope_Native(t *testing.T) {
	// dashscope rerank 用 native shape: input.{query,documents} 嵌套.
	var gotPath, gotQuery string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Input struct {
				Query string `json:"query"`
			} `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery = body.Input.Query

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"results": []map[string]any{
					{"index": 0, "relevance_score": 0.95},
				},
			},
			"usage":      map[string]any{"total_tokens": 24},
			"request_id": "req_zh",
		})
	}))
	defer stub.Close()

	p := newRerankProbe()
	res := p.runRerankHTTP(context.Background(), dashscope.New(),
		[]byte("sk-test"), stub.URL, nil, "qwen3-rerank", "gte-rerank-v2")

	if !res.OK {
		t.Fatalf("expected OK, got: %+v", res)
	}
	if gotPath != "/api/v1/services/rerank/text-rerank/text-rerank" {
		t.Errorf("path: %q (dashscope native rerank 路径不对)", gotPath)
	}
	if gotQuery != "hello" {
		t.Errorf("input.query: %q", gotQuery)
	}
	// dashscope total_tokens=24 透传到 Tokens (因为 BilledUnits.SearchUnits 占位)
	if res.Tokens != 24 {
		t.Errorf("Tokens: %d, want 24 (dashscope total_tokens)", res.Tokens)
	}
}

func TestRunRerankHTTP_Unauthorized(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer stub.Close()

	p := newRerankProbe()
	res := p.runRerankHTTP(context.Background(), openai.New(),
		[]byte("bad"), stub.URL, nil, "m", "m")
	if res.OK {
		t.Fatal("expected failure on 401")
	}
	if res.ErrorCode != CodeUnauthorized {
		t.Errorf("error_code: %q, want %q", res.ErrorCode, CodeUnauthorized)
	}
}

func TestRunRerankHTTP_EmptyResults(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`)) // 空 results
	}))
	defer stub.Close()

	p := newRerankProbe()
	res := p.runRerankHTTP(context.Background(), openai.New(),
		[]byte("k"), stub.URL, nil, "m", "m")
	if res.OK {
		t.Fatal("expected failure on empty results")
	}
	if res.ErrorCode != CodeBadResponse {
		t.Errorf("error_code: %q, want %q", res.ErrorCode, CodeBadResponse)
	}
}
