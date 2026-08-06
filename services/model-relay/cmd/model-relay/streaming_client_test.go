package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 黑洞上游:接受 TCP 连接、读完请求,但永不写响应头(模拟 your-llm-gateway
// DNS 解析成功后连到黑洞网关的真实卡死场景)。没有 ResponseHeaderTimeout
// 的 client 会在这里永久阻塞 → agent #11 无限转圈。
func TestStreamingHTTPClient_BlackholeUpstreamFailsFast(t *testing.T) {
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "200ms")

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不调用 WriteHeader / Write —— 让 handler 阻塞,客户端拿不到响应头。
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := streamingHTTPClient()

	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		resp, derr := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- derr
	}()

	select {
	case derr := <-done:
		if derr == nil {
			t.Fatal("黑洞上游应返回错误,实际成功 —— ResponseHeaderTimeout 未生效")
		}
		// 期望是超时类错误(net.Error.Timeout())。
		var ne net.Error
		if !asNetTimeout(derr, &ne) {
			t.Logf("返回的错误(非 net timeout 也可接受,只要快速失败): %v", derr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client.Do 在 ResponseHeaderTimeout(200ms)后仍未返回 —— 黑洞挂死复现,修复无效")
	}
}

// 健康上游:立即回 200 + body。client 应正常拿到响应,不被 header timeout 误伤。
func TestStreamingHTTPClient_HealthyUpstreamSucceeds(t *testing.T) {
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "200ms")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := streamingHTTPClient()
	resp, err := client.Do(mustReq(t, srv.URL))
	if err != nil {
		t.Fatalf("健康上游不该报错: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// 慢首字节但在超时内回头:ResponseHeaderTimeout 只卡"首响应头",一旦头到达,
// 后续 body 可以慢慢流(模拟长 SSE 流不被掐断)。
func TestStreamingHTTPClient_SlowBodyAfterHeaderNotKilled(t *testing.T) {
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "300ms")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 头立即回(在 300ms 内),body 分两段、每段间隔超过 header timeout。
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(500 * time.Millisecond) // > header timeout,但已过 header 阶段
		_, _ = w.Write([]byte("data: chunk\n\n"))
	}))
	defer srv.Close()

	client := streamingHTTPClient()
	resp, err := client.Do(mustReq(t, srv.URL))
	if err != nil {
		t.Fatalf("头已到达后慢 body 不该触发 header timeout: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	if _, rerr := resp.Body.Read(buf); rerr != nil && rerr.Error() != "EOF" {
		t.Fatalf("读流式 body 失败: %v", rerr)
	}
}

// upstreamHeaderTimeout:非法 env 回退默认,合法 env 生效。
func TestUpstreamHeaderTimeout_EnvOverride(t *testing.T) {
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "")
	if got := upstreamHeaderTimeout(); got != 120*time.Second {
		t.Fatalf("缺省应为 120s, got %v", got)
	}
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "45s")
	if got := upstreamHeaderTimeout(); got != 45*time.Second {
		t.Fatalf("env 覆盖应为 45s, got %v", got)
	}
	t.Setenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT", "garbage")
	if got := upstreamHeaderTimeout(); got != 120*time.Second {
		t.Fatalf("非法值应回退 120s, got %v", got)
	}
}

func mustReq(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func asNetTimeout(err error, target *net.Error) bool {
	for e := err; e != nil; {
		if ne, ok := e.(net.Error); ok && ne.Timeout() {
			*target = ne
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
