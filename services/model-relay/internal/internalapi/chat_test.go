package internalapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/api"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

func chatReq(t *testing.T, token string, body map[string]any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/chat", bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestChat_AuthRequired(t *testing.T) {
	s := &Server{Token: "secret", Messages: &api.MessagesHandler{}}
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "wrong", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", w.Code)
	}
}

func TestChat_MissingUserID(t *testing.T) {
	s := &Server{Token: "secret", Messages: &api.MessagesHandler{}}
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "secret", map[string]any{"model": "claude-opus-4-8"}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id: got %d, want 400", w.Code)
	}
}

func TestChat_HandlerNotWired(t *testing.T) {
	s := &Server{Token: "secret"} // Messages nil
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "secret", map[string]any{"user_id": "u1"}))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil handler: got %d, want 503", w.Code)
	}
}

// 回归: stream=true 必须 SSE 透传, 不再 500 no_streaming。
//
// 修复前 bufferRecorder 不实现 http.Flusher, MessagesHandler 的流式路径
// 直接 500 —— 内部车道被迫"必须 stream=false"。streamForwarder 透传外层
// writer 后, wiki-llm 的 streaming partial-save(边生成边落页)得以保留。
func TestChat_StreamingPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"}}]}\n\n"))
			fl.Flush()
			_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"世界\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
		}))
	defer upstream.Close()

	reg := provider.NewRegistry()
	reg.Register(openai.New())
	h := &api.MessagesHandler{
		Registry:   reg,
		HTTPClient: http.DefaultClient,
		CredsResolver: func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error) {
			return "openai", &provider.Credentials{APIKey: "k", BaseURL: upstream.URL}, r, nil
		},
	}

	s := &Server{Token: "secret", Messages: h}
	mux := http.NewServeMux()
	s.MountChat(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, chatReq(t, "secret", map[string]any{
		"user_id":    "u1",
		"model":      "gpt-x",
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"max_tokens": 16,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("stream: got %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: delta") {
		t.Fatalf("missing unified delta frames, body=%q", body)
	}
	if !strings.Contains(body, "你好") || !strings.Contains(body, "世界") {
		t.Fatalf("delta text lost in passthrough, body=%q", body)
	}
}
