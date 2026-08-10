// Worker poll 路由「无条件挂载」单测 —— 启动竞态根治：queue 未就绪
// （nil / readiness pending）时路由仍在,handler 返 503 no_jetstream,
// 不再落默认 mux 404(daemon 无限重注册的根因)。无需 DB:queue 检查在
// store 查询之前。

package agentplane

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// newWorkerTestServer 装一个无 DB 的 Server 到 httptest。readiness /
// queue 由用例按场景注入。
func newWorkerTestServer(t *testing.T, readiness *Readiness, queue *Queue) (*httptest.Server, string) {
	t.Helper()
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	signer := bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute)
	srv := &Server{
		Verifier:  verifier,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: readiness,
		Queue:     queue,
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	tok, err := signer.Sign(&bauth.Claims{UserID: uuid.New().String()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return ts, tok
}

func postWithToken(t *testing.T, ts *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	return resp
}

// queue nil（dev / 无 reconciler 直挂）→ 路由仍挂载：无 token 401（不是
// 404），有 token 503 no_jetstream。
func TestWorkerRoutes_MountedWhenQueueNil(t *testing.T) {
	ts, tok := newWorkerTestServer(t, nil, nil)
	pollPath := "/v1/agent/work/" + uuid.New().String() + "/poll"

	resp := postWithToken(t, ts, pollPath, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d want 401 (route must be mounted, not 404)", resp.StatusCode)
	}

	resp2 := postWithToken(t, ts, pollPath, tok)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil-queue status=%d want 503", resp2.StatusCode)
	}
}

// readiness pending（NATS 配了但还没就绪）→ 三个 worker 端点都 503。
func TestWorkerRoutes_503WhenReadinessPending(t *testing.T) {
	q := NewQueue(nil)
	r := NewReadiness(&fakeBus{}, true, q, // fakeBus connected=false → disconnected
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts, tok := newWorkerTestServer(t, r, q)

	envID := uuid.New().String()
	paths := []string{
		"/v1/agent/work/" + envID + "/poll",
		"/v1/agent/control/" + envID + "/poll",
		"/v1/agent/sessions/" + uuid.New().String() + "/publish",
	}
	for _, p := range paths {
		resp := postWithToken(t, ts, p, tok)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s status=%d want 503 while readiness pending", p, resp.StatusCode)
		}
	}
}

// readiness 就绪后同一进程放行（不重启）：503 门消失,请求进到 store 层。
// 无 DB → store nil 会 panic,所以这里只验证「不再 503」用 queue() 的
// 直接语义：Readiness.Queue() 从 nil 翻成非 nil。
func TestWorkerRoutes_SelfHealWhenReady(t *testing.T) {
	q := NewQueue(nil)
	fb := &fakeBus{js: &fakeJS{}}
	r := NewReadiness(fb, true, q,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Tick = 5 * time.Millisecond
	r.MaxBackoff = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	if r.Queue() != nil {
		t.Fatal("queue should be nil before connect")
	}
	fb.setConnected(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && r.Queue() == nil {
		time.Sleep(2 * time.Millisecond)
	}
	if r.Queue() == nil {
		t.Fatal("queue should become available without restart once streams ready")
	}
	if r.Queue() != q {
		t.Fatal("readiness should hand out the same queue instance wired at boot")
	}
}
