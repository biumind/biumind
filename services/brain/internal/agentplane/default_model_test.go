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
// default-chat 与 preferred-chat 两个端点共用 token,状态 / 返回值各自
// 独立(pref* 字段管 preferred-chat)。
type fakeDefaultChatRelay struct {
	srv   *httptest.Server
	calls atomic.Int32

	token  string
	status int
	code   string

	prefStatus int
	prefCode   string
}

func newFakeDefaultChatRelay(t *testing.T, token string, status int, code string) *fakeDefaultChatRelay {
	t.Helper()
	f := &fakeDefaultChatRelay{token: token, status: status, code: code,
		prefStatus: http.StatusNotFound}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		wantStatus, wantCode := f.status, f.code
		switch {
		case r.URL.Path == "/v1/internal/models/default-chat":
		case r.URL.Path == "/v1/internal/models/preferred-chat",
			r.URL.Path == "/v1/internal/models/preferred":
			wantStatus, wantCode = f.prefStatus, f.prefCode
		default:
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if wantStatus != http.StatusOK {
			http.Error(w, "nope", wantStatus)
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"code": wantCode})
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
		if got := r.PreferredChatModel(context.Background()); got != "" {
			t.Errorf("disabled resolver should yield empty; got %q", got)
		}
	}
}

// preferred-chat 端点:200 → code 且 TTL 内命中缓存;与 default-chat
// 的缓存槽互不影响(一端 404 不污染另一端)。
func TestDefaultModelResolver_PreferredChat(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusNotFound, "")
	f.prefStatus, f.prefCode = http.StatusOK, "pref-1"
	r := testResolver(f.srv.URL)

	if got := r.PreferredChatModel(context.Background()); got != "pref-1" {
		t.Fatalf("got %q", got)
	}
	if got := r.PreferredChatModel(context.Background()); got != "pref-1" {
		t.Fatalf("cached call got %q", got)
	}
	if got := r.DefaultChatModel(context.Background()); got != "" {
		t.Fatalf("default-chat should stay empty; got %q", got)
	}
	if n := f.calls.Load(); n != 2 {
		t.Errorf("expect 2 relay calls (1 pref + 1 default); got %d", n)
	}
}

// ChatRunner.defaultChatModel 兜底链:relay default-chat > env 覆盖 >
// relay preferred-chat > 明确报错(不再有硬编码模型名兜底)。
func TestChatRunner_DefaultChatModelChain(t *testing.T) {
	f := newFakeDefaultChatRelay(t, "internal-token", http.StatusOK, "relay-default")
	r := testResolver(f.srv.URL)

	// relay default 命中 → relay 值赢过 env。
	cr := &ChatRunner{DefaultModel: "env-override", DefaultModels: r, Logger: nopLogger()}
	got, err := cr.defaultChatModel(context.Background())
	if err != nil || got != "relay-default" {
		t.Errorf("relay default should win; got %q err=%v", got, err)
	}

	// default-chat 未配(404) → 落 env。
	f.status = http.StatusNotFound
	r2 := testResolver(f.srv.URL)
	cr = &ChatRunner{DefaultModel: "env-override", DefaultModels: r2, Logger: nopLogger()}
	got, err = cr.defaultChatModel(context.Background())
	if err != nil || got != "env-override" {
		t.Errorf("env override expected; got %q err=%v", got, err)
	}

	// default-chat 404 + env 空 + preferred-chat 命中 → 落自动优选。
	f.prefStatus, f.prefCode = http.StatusOK, "relay-preferred"
	r3 := testResolver(f.srv.URL)
	cr = &ChatRunner{DefaultModels: r3, Logger: nopLogger()}
	got, err = cr.defaultChatModel(context.Background())
	if err != nil || got != "relay-preferred" {
		t.Errorf("preferred-chat expected; got %q err=%v", got, err)
	}

	// 全部落空 → 明确 error,不再硬编码兜底。
	f.prefStatus = http.StatusNotFound
	r4 := testResolver(f.srv.URL)
	for _, c := range []*ChatRunner{
		{DefaultModels: r4, Logger: nopLogger()}, // resolver 全 404
		{Logger: nopLogger()},                    // resolver nil + env 空
	} {
		got, err := c.defaultChatModel(context.Background())
		if err == nil || got != "" {
			t.Errorf("expected errNoChatModelAvailable; got %q err=%v", got, err)
		}
	}
}

// PreferredModel 泛化形态: mode / capability 正确编进 query。
func TestDefaultModelResolver_PreferredModelQuery(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "vis-1"})
	}))
	defer srv.Close()
	r := NewDefaultModelResolver(srv.URL, "internal-token",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := r.PreferredModel(context.Background(), "chat", "vision"); got != "vis-1" {
		t.Fatalf("got %q", got)
	}
	if gotPath != "/v1/internal/models/preferred" || gotQuery != "mode=chat&capability=vision" {
		t.Fatalf("path=%q query=%q", gotPath, gotQuery)
	}
}
