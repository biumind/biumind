package health

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

// 不依赖真 DB 的 embedding probe 单测. 直接构造 Probe + stub server.
// 验证: URL 拼接 (NormalizeBaseURL) / 请求体格式 / 响应解析 / 错误分类.

func newEmbeddingProbe() *Probe {
	return &Probe{cfg: Config{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func TestRunEmbeddingHTTP_OK(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotInput string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		gotInput = body.Input

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "embedding": []float64{0.1, 0.2, 0.3, 0.4, 0.5}, "index": 0},
			},
			"model": "BAAI/bge-m3",
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}))
	defer stub.Close()

	p := newEmbeddingProbe()
	res := p.runEmbeddingHTTP(context.Background(), openai.New(), []byte("sk-test"),
		stub.URL+"/v1", // 模拟用户填带 /v1 的 base_url (典型 New-API 场景)
		nil, "bge-m3", "BAAI/bge-m3")

	if !res.OK {
		t.Fatalf("expected OK, got error: code=%s msg=%s status=%d", res.ErrorCode, res.Error, res.StatusCode)
	}
	if gotPath != "/v1/embeddings" {
		t.Errorf("path: got %q, want /v1/embeddings (NormalizeBaseURL 应把尾部 /v1 去掉再让 probe 加 /v1)", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth: got %q, want Bearer sk-test", gotAuth)
	}
	if gotModel != "BAAI/bge-m3" {
		t.Errorf("body.model: got %q, want BAAI/bge-m3", gotModel)
	}
	if gotInput != "hello" {
		t.Errorf("body.input: got %q, want hello", gotInput)
	}
}

func TestRunEmbeddingHTTP_BaseURLVariants(t *testing.T) {
	// 一个共享 stub, 验证不同 base_url 写法都被 normalize 成相同的目标 path.
	var gotPath string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float64{0.1}}},
			"usage": map[string]any{"prompt_tokens": 1},
		})
	}))
	defer stub.Close()

	cases := []struct {
		name string
		base string
	}{
		{"host only", stub.URL},
		{"with /v1", stub.URL + "/v1"},
		{"with /v1/", stub.URL + "/v1/"},
		{"trailing slash", stub.URL + "/"},
	}
	p := newEmbeddingProbe()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPath = ""
			res := p.runEmbeddingHTTP(context.Background(), openai.New(), []byte("k"), tc.base, nil, "m", "m")
			if !res.OK {
				t.Fatalf("expected OK, got: %s", res.Error)
			}
			if gotPath != "/v1/embeddings" {
				t.Errorf("base=%q: path=%q (期望统一成 /v1/embeddings)", tc.base, gotPath)
			}
		})
	}
}

func TestRunEmbeddingHTTP_Unauthorized(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer stub.Close()

	p := newEmbeddingProbe()
	res := p.runEmbeddingHTTP(context.Background(), openai.New(), []byte("bad"), stub.URL, nil, "m", "m")
	if res.OK {
		t.Fatal("expected failure on 401")
	}
	if res.ErrorCode != CodeUnauthorized {
		t.Errorf("error_code: got %q, want %q", res.ErrorCode, CodeUnauthorized)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status_code: got %d, want 401", res.StatusCode)
	}
}

func TestRunEmbeddingHTTP_BadResponse(t *testing.T) {
	// 200 但 data 为空 — 模拟上游返回了不符合 OpenAI embedding 格式的响应.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer stub.Close()

	p := newEmbeddingProbe()
	res := p.runEmbeddingHTTP(context.Background(), openai.New(), []byte("k"), stub.URL, nil, "m", "m")
	if res.OK {
		t.Fatal("expected failure on empty data")
	}
	if res.ErrorCode != CodeBadResponse {
		t.Errorf("error_code: got %q, want %q", res.ErrorCode, CodeBadResponse)
	}
	if !strings.Contains(res.Error, "empty embedding") {
		t.Errorf("error msg: got %q, want substring 'empty embedding'", res.Error)
	}
}

func TestRunEmbeddingHTTP_HeaderOverride(t *testing.T) {
	var gotXKey string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXKey = r.Header.Get("X-Custom-Auth")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1]}],"usage":{"prompt_tokens":1}}`))
	}))
	defer stub.Close()

	p := newEmbeddingProbe()
	res := p.runEmbeddingHTTP(context.Background(), openai.New(), []byte("k"), stub.URL,
		map[string]string{"X-Custom-Auth": "override-token"}, "m", "m")
	if !res.OK {
		t.Fatalf("unexpected fail: %s", res.Error)
	}
	if gotXKey != "override-token" {
		t.Errorf("header override not applied: got %q", gotXKey)
	}
}
