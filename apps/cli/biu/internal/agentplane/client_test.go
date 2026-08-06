package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubServer 起一个 httptest 假装 brain agentplane endpoints。每个 endpoint
// 用 handler 注册到 mux；测试断言请求形状。
func stubServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, *Client) {
	t.Helper()
	if mux == nil {
		mux = http.NewServeMux()
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := NewClient(ts.URL, "test-pat", &http.Client{Timeout: 5 * time.Second})
	return ts, c
}

// 验证 Bearer 头被正确加上 + 404 错误能被识别成 APIError.IsNotFound()
func TestClient_AuthHeaderAndNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-pat" {
			t.Errorf("missing/wrong Bearer header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such env"}}`))
	})
	_, c := stubServer(t, mux)
	err := c.Heartbeat(context.Background(), uuid.New())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if !apiErr.IsNotFound() {
		t.Errorf("expected IsNotFound true, got %d", apiErr.Status)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("code=%q want not_found", apiErr.Code)
	}
}

func TestClient_Register(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, r *http.Request) {
		var req RegisterReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.WorkerKind != "biu_daemon" || req.MachineName != "didi-mbp" {
			t.Errorf("body lost: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"environment_id":"550e8400-e29b-41d4-a716-446655440000","worker_kind":"biu_daemon","machine_name":"didi-mbp","state":"online"}`))
	})
	_, c := stubServer(t, mux)
	resp, err := c.Register(context.Background(), RegisterReq{
		WorkerKind: "biu_daemon", MachineName: "didi-mbp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.EnvironmentID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("envID=%q", resp.EnvironmentID)
	}
}

func TestClient_PollWork_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/work/{env_id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	_, c := stubServer(t, mux)
	work, err := c.PollWork(context.Background(), uuid.New(), 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if work != nil {
		t.Errorf("expected nil work on 204, got %+v", work)
	}
}

func TestClient_PollWork_Got(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/work/{env_id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ack_token":"tok-1","body":{"session_id":"550e8400-e29b-41d4-a716-446655440000","mode":"agent","prompt":"hi"}}`))
	})
	_, c := stubServer(t, mux)
	work, err := c.PollWork(context.Background(), uuid.New(), 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if work.AckToken != "tok-1" {
		t.Errorf("ack_token=%q", work.AckToken)
	}
	if !strings.Contains(string(work.Body), "session_id") {
		t.Errorf("body lost: %s", work.Body)
	}
}

func TestClient_PublishFrame(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "streamlined_text") {
			t.Errorf("body lost: %s", body)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	_, c := stubServer(t, mux)
	err := c.PublishFrame(context.Background(), uuid.New(),
		[]byte(`{"type":"streamlined_text","text":"hi","uuid":"u1","session_id":"s1"}`))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestClient_AckNak(t *testing.T) {
	mux := http.NewServeMux()
	var sawAck, sawNak bool
	mux.HandleFunc("POST /v1/agent/work/{env_id}/ack/{token}", func(w http.ResponseWriter, _ *http.Request) {
		sawAck = true
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/agent/work/{env_id}/nak/{token}", func(w http.ResponseWriter, _ *http.Request) {
		sawNak = true
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	_, c := stubServer(t, mux)
	if err := c.AckWork(context.Background(), uuid.New(), "tok"); err != nil {
		t.Fatal(err)
	}
	if err := c.NakWork(context.Background(), uuid.New(), "tok"); err != nil {
		t.Fatal(err)
	}
	if !sawAck || !sawNak {
		t.Errorf("ack=%v nak=%v", sawAck, sawNak)
	}
}

func TestClient_NetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "tok", &http.Client{Timeout: 100 * time.Millisecond})
	err := c.Heartbeat(context.Background(), uuid.New())
	if err == nil {
		t.Error("expected network error, got nil")
	}
	// 网络错不应该是 APIError
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("network err wrongly wrapped as APIError: %v", err)
	}
}
