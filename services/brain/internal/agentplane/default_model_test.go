// DefaultModelResolver 单测 —— httptest fake relay 覆盖 200 / 404 / 500 /
// 缓存 TTL / 负缓存退避,不打真 relay。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDefaultChatRelay 按 internal 端点契约应答;calls 计数用于断言缓存
// 命中时不再打 relay。auth 校验:bearer 不对 → 401,顺带锁住鉴权契约。
type fakeDefaultChatRelay struct {
	srv   *httptest.Server
	calls atomic.Int32

	token  string
	status int
	code   string
}

func newFakeDefaultChatRelay(t *testing.T, token string, status int, code string) *fakeDefaultChatRelay {
	t.Helper()
	f := &fakeDefaultChatRelay{token: token, status: status, code: code}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/v1/internal/models/default-chat" {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if f.status != http.StatusOK {
			http.Error(w, "nope", f.status)
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"code": f.code})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testResolver(relayURL string) *DefaultModelResolver {
	r := NewDefaultModelResolver(relayURL, "internal-token",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r
}

// 200 → 返回 model code,且在 TTL 内第二次调用命中缓存(不再打 relay)。
func TestDefaultModelResolver_HappyPathCached(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusOK, "claude-sonnet-4-6")
	r := testResolver(f.srv.URL)

	if got := r.DefaultChatModel(context.Background()); got != "claude-sonnet-4-6" {
		t.Fatalf("got %q", got)
	}
	if got := r.DefaultChatModel(context.Background()); got != "claude-sonnet-4-6" {
		t.Fatalf("cached call got %q", got)
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("expect 1 relay call (cache hit on 2nd); got %d", n)
	}
}

// TTL 过期后重新 fetch,拿到新值。
func TestDefaultModelResolver_TTLExpiryRefetches(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusOK, "model-v1")
	r := testResolver(f.srv.URL)

	now := time.Now()
	r.now = func() time.Time { return now }

	if got := r.DefaultChatModel(context.Background()); got != "model-v1" {
		t.Fatalf("got %q", got)
	}
	// 推进过 TTL → 重新 fetch。
	now = now.Add(2 * time.Minute)
	f.code = "model-v2"
	if got := r.DefaultChatModel(context.Background()); got != "model-v2" {
		t.Fatalf("after TTL expiry got %q", got)
	}
	if n := f.calls.Load(); n != 2 {
		t.Errorf("expect 2 relay calls; got %d", n)
	}
}

// 404(admin 未配默认模型)→ "",且负缓存期间不再打 relay。
func TestDefaultModelResolver_NotFoundNegativeCached(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusNotFound, "")
	r := testResolver(f.srv.URL)

	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("404 should yield empty; got %q", got)
	}
	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("negative-cached call got %q", got)
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("expect 1 relay call (negative cache on 2nd); got %d", n)
	}
}

// 500 → "" + 负缓存;负缓存过期后重试(relay 恢复 → 拿到值)。
func TestDefaultModelResolver_ErrorBackoffThenRetry(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusInternalServerError, "")
	r := testResolver(f.srv.URL)

	now := time.Now()
	r.now = func() time.Time { return now }

	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("500 should yield empty; got %q", got)
	}
	r.DefaultChatModel(context.Background())
	if n := f.calls.Load(); n != 1 {
		t.Errorf("expect negative cache (1 call); got %d", n)
	}

	// 负缓存过期 + relay 恢复 → 重试成功。
	now = now.Add(30 * time.Second)
	f.status = http.StatusOK
	f.code = "claude-sonnet-4-6"
	if got := r.DefaultChatModel(context.Background()); got != "claude-sonnet-4-6" {
		t.Fatalf("after backoff got %q", got)
	}
}

// relay 不可达(网络错误)→ "",不 panic。
func TestDefaultModelResolver_Unreachable(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusOK, "x")
	f.srv.Close() // 立即关掉 → connection refused
	r := testResolver(f.srv.URL)
	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("unreachable relay should yield empty; got %q", got)
	}
}

// token 错 → 401 → ""(鉴权契约:endpoint 要 Bearer internal token)。
func TestDefaultModelResolver_WrongToken(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "real-token", http.StatusOK, "x")
	r := testResolver(f.srv.URL) // token=internal-token ≠ real-token
	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("wrong token should yield empty; got %q", got)
	}
}

// relayURL / token 空 → 禁用,恒 "",不打任何 HTTP。
func TestDefaultModelResolver_Disabled(t *testing.T) {
	for _, r := range []*DefaultModelResolver{
		NewDefaultModelResolver("", "tok", nil),
		NewDefaultModelResolver("http://relay:7001", "", nil),
	} {
		if got := r.DefaultChatModel(context.Background()); got != "" {
			t.Errorf("disabled resolver should yield empty; got %q", got)
		}
	}
}

// ChatRunner.defaultChatModel 兜底链:relay 命中 > env 覆盖 > 硬兜底。
func TestChatRunner_DefaultChatModelChain(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusOK, "relay-default")
	r := testResolver(f.srv.URL)

	// relay 命中 → relay 值赢过 env。
	cr := &ChatRunner{DefaultModel: "env-override", DefaultModels: r, Logger: nopLogger()}
	if got := cr.defaultChatModel(context.Background()); got != "relay-default" {
		t.Errorf("relay default should win; got %q", got)
	}

	// relay 未配(404)→ 落 env。
	f.status = http.StatusNotFound
	f.calls.Store(0)
	r2 := testResolver(f.srv.URL)
	cr = &ChatRunner{DefaultModel: "env-override", DefaultModels: r2, Logger: nopLogger()}
	if got := cr.defaultChatModel(context.Background()); got != "env-override" {
		t.Errorf("env override expected; got %q", got)
	}

	// resolver nil + env 空 → 硬兜底。
	cr = &ChatRunner{Logger: nopLogger()}
	if got := cr.defaultChatModel(context.Background()); got != "claude-sonnet-4-6" {
		t.Errorf("hardcoded fallback expected; got %q", got)
	}
}
