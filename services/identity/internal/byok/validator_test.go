package byok

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 用 httptest.Server 模拟上游, validator 拿到 base URL 替换 — 但 MVP 写死了 url,
// 所以这里只能测 pingHTTP 直接路径. 把 pingHTTP 改成可注入 url 后再回头测专用 provider.
//
// 这里用 net/http test server 测 pingHTTP 的状态码分支.

func TestPingHTTP_2xxValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.pingHTTP(context.Background(), "GET", srv.URL, map[string]string{
		"Authorization": "Bearer good",
	})
	if got != PingValid {
		t.Fatalf("got %s, want PingValid", got)
	}
}

func TestPingHTTP_401Invalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.pingHTTP(context.Background(), "GET", srv.URL, nil)
	if got != PingInvalid {
		t.Fatalf("got %s, want PingInvalid", got)
	}
}

func TestPingHTTP_403Invalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.pingHTTP(context.Background(), "GET", srv.URL, nil)
	if got != PingInvalid {
		t.Fatalf("got %s, want PingInvalid", got)
	}
}

func TestPingHTTP_500Network(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.pingHTTP(context.Background(), "GET", srv.URL, nil)
	if got != PingNetwork {
		t.Fatalf("got %s, want PingNetwork (5xx 不能判定)", got)
	}
}

func TestPingHTTP_DNSFailureNetwork(t *testing.T) {
	v := NewValidator()
	got := v.pingHTTP(context.Background(), "GET", "http://no-such-host-12345.invalid/", nil)
	if got != PingNetwork {
		t.Fatalf("got %s, want PingNetwork", got)
	}
}

func TestPing_EmptyKeyInvalid(t *testing.T) {
	v := NewValidator()
	got := v.Ping(context.Background(), PingArgs{Provider: "anthropic", APIKey: ""})
	if got != PingInvalid {
		t.Fatalf("got %s, want PingInvalid", got)
	}
}

func TestPing_UnknownProviderReturnsUnknown(t *testing.T) {
	v := NewValidator()
	got := v.Ping(context.Background(), PingArgs{Provider: "volcengine", APIKey: "sk-x"})
	if got != PingUnknown {
		t.Fatalf("got %s, want PingUnknown (volcengine MVP 不验)", got)
	}
}

// 验证 pingHTTP 设了 headers
func TestPingHTTP_HeadersForwarded(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	_ = v.pingHTTP(context.Background(), "GET", srv.URL, map[string]string{
		"x-api-key":         "test-key",
		"anthropic-version": "2023-06-01",
	})
	if !strings.EqualFold(gotHeader, "test-key") {
		t.Fatalf("x-api-key header not forwarded: %q", gotHeader)
	}
}

// ─── 00033: custom provider + base_url ────────────────

func TestPing_CustomOpenAICompat(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer sk-good" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.Ping(context.Background(), PingArgs{
		Provider: "custom", APIKey: "sk-good",
		BaseURL: srv.URL, Protocol: "openai_compat",
	})
	if got != PingValid {
		t.Fatalf("got %s, want PingValid", got)
	}
	// base_url 不含 /v1 → 自动补 → /v1/models (与 refresh.go/direct_llm_probe 同款)
	if hitPath != "/v1/models" {
		t.Errorf("hit %s, want /v1/models (base 无 /v1 应补)", hitPath)
	}
}

func TestPing_CustomBaseURLAlreadyV1(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	_ = v.Ping(context.Background(), PingArgs{
		Provider: "custom", APIKey: "sk-x",
		BaseURL: srv.URL + "/v1", Protocol: "openai_compat",
	})
	if hitPath != "/v1/models" {
		t.Errorf("hit %s, want /v1/models (base 含 /v1 不重复补)", hitPath)
	}
}

func TestPing_CustomAnthropic(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	got := v.Ping(context.Background(), PingArgs{
		Provider: "custom", APIKey: "sk-ant",
		BaseURL: srv.URL, Protocol: "anthropic",
	})
	if got != PingValid {
		t.Fatalf("got %s, want PingValid", got)
	}
	if !strings.EqualFold(gotHeader, "sk-ant") {
		t.Errorf("anthropic custom 应用 x-api-key, got %q", gotHeader)
	}
}

func TestPing_CustomEmptyBaseURL(t *testing.T) {
	v := NewValidator()
	got := v.Ping(context.Background(), PingArgs{
		Provider: "custom", APIKey: "sk-x", Protocol: "openai_compat",
	})
	if got != PingInvalid {
		t.Fatalf("got %s, want PingInvalid (custom 无 base_url)", got)
	}
}

func TestPing_StandardProviderBaseURLOverride(t *testing.T) {
	// 标准 provider (MVP 默认 PingUnknown 的 volcengine) 传 BaseURL 时
	// 应覆盖默认 endpoint, 实际打 httptest server (补 MVP 遗留).
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	v := NewValidator()
	_ = v.Ping(context.Background(), PingArgs{
		Provider: "volcengine", APIKey: "sk-x", BaseURL: srv.URL,
	})
	if !hit {
		t.Fatal("BaseURL 覆盖应打到 httptest server, 实际未命中")
	}
}
