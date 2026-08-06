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
)

// image probe 单测. dashscope.Adaptor 走 AsyncImageAdaptor: submit 拿到
// task_id 即认为通, 不 poll 等出图 (probe 健康度 ≠ 真出图).

func newImageProbe() *Probe {
	return &Probe{cfg: Config{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func TestRunImageHTTP_DashScope_Async_OK(t *testing.T) {
	var gotPath, gotAsyncHeader string
	var gotPrompt string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAsyncHeader = r.Header.Get("X-DashScope-Async")
		var body struct {
			Input struct {
				Prompt string `json:"prompt"`
			} `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotPrompt = body.Input.Prompt

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output":     map[string]any{"task_id": "task_zh_xyz"},
			"request_id": "req_x",
		})
	}))
	defer stub.Close()

	p := newImageProbe()
	res := p.runImageHTTP(context.Background(), dashscope.New(),
		[]byte("sk-test"), stub.URL, nil, "wanx", "wanx2.0-t2i-turbo")

	if !res.OK {
		t.Fatalf("expected OK, got: %+v", res)
	}
	if gotPath != "/api/v1/services/aigc/text2image/image-synthesis" {
		t.Errorf("path: %q (dashscope wanx submit 路径不对)", gotPath)
	}
	if gotAsyncHeader != "enable" {
		t.Errorf("X-DashScope-Async: %q, want 'enable' (异步必须开)", gotAsyncHeader)
	}
	if gotPrompt != "a red apple" {
		t.Errorf("prompt: %q (probe 默认 a red apple)", gotPrompt)
	}
}

func TestRunImageHTTP_DashScope_BusinessError(t *testing.T) {
	// 200 但 body 含 code/message — dashscope 业务错误.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"key invalid","request_id":"x"}`))
	}))
	defer stub.Close()

	p := newImageProbe()
	res := p.runImageHTTP(context.Background(), dashscope.New(),
		[]byte("bad"), stub.URL, nil, "wanx", "wanx2.0-t2i-turbo")

	if res.OK {
		t.Fatal("expected failure on business error")
	}
	if res.ErrorCode != CodeBadResponse {
		t.Errorf("error_code: %q, want %q", res.ErrorCode, CodeBadResponse)
	}
}

func TestRunImageHTTP_DashScope_Unauthorized(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey"}`))
	}))
	defer stub.Close()

	p := newImageProbe()
	res := p.runImageHTTP(context.Background(), dashscope.New(),
		[]byte("bad"), stub.URL, nil, "wanx", "wanx2.0-t2i-turbo")

	if res.OK {
		t.Fatal("expected failure on 401")
	}
	if res.ErrorCode != CodeUnauthorized {
		t.Errorf("error_code: %q, want %q", res.ErrorCode, CodeUnauthorized)
	}
}
