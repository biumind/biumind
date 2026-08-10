// S3-5A ingress 测试 —— 重点是鉴权 + 错误路径，**不**测真 JetStream 流。
//
// 真 stream round-trip / fanout / resume 测试在 S3-5B（需 NATS_URL 注 broker
// + 加 OrderedConsumer 支持）。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
)

// jsonUnmarshal 别名 —— 避免单测内既 import "encoding/json" 又写
// json.Unmarshal 顶层引用混乱。
var jsonUnmarshal = json.Unmarshal

// fakeIngressJS 是一个 bus.JetStream 的 mock。Subscribe 返回个永远不发
// 消息的 dummy subscription —— 让 ingress goroutine 等不到东西，测试只
// 走鉴权 / 路由 / 入口校验逻辑。
type fakeIngressJS struct{}

func (fakeIngressJS) EnsureStream(_ context.Context, _ bus.StreamSpec) error { return nil }
func (fakeIngressJS) Publish(_ context.Context, _ string, _ any, _ ...bus.Header) error {
	return nil
}
func (fakeIngressJS) Subscribe(_ context.Context, _ bus.ConsumerSpec, _ bus.JSHandler) (bus.Subscription, error) {
	return &dummySub{}, nil
}

type dummySub struct{}

func (*dummySub) Drain() error { return nil }

// fakeIngressJS.RawJetStream() 返回 nil —— ingress resume 分支走"raw JS
// unavailable"路径，简化测试不去搭真 broker（真 broker 测试是 S3-5C
// follow-up）。
func (fakeIngressJS) RawJetStream() jetstream.JetStream { return nil }

// ingressHarness 装一个真 *Ingress 到 httptest server，测 handleStream 行为。
func ingressHarness(t *testing.T) (*httptest.Server, *bauth.Signer, *Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — ingress test needs DB for session lookup")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	_, _ = pool.Exec(context.Background(),
		`TRUNCATE agent_environments, agent_sessions, agent_session_results CASCADE`)

	store := NewStore(pool)
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	signer := bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, SessionTokenTTL)
	ingress := NewIngress(fakeIngressJS{}, store, verifier,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := NewServer(store, verifier, signer, nil, ingress,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.Close(); pool.Close() })
	return ts, signer, store, pool
}

// 路由无条件挂载（启动竞态根治）：JS 未就绪时不再是 404，而是 503
// no_jetstream —— 客户端能区分「路由不存在」跟「broker 还没就绪」。
// 无需 DB：js 检查在 store 查询之前。
func TestIngress_Mounted503WhenJSNil(t *testing.T) {
	ingress := NewIngress(nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := NewServer(nil, nil, nil, nil, ingress,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/agent/sessions/" + uuid.New().String() + "/stream")
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := jsonUnmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "no_jetstream" {
		t.Errorf("code=%q want no_jetstream", body.Error.Code)
	}
}

// Ingress 本身未注入（main.go 不该这么 wire）也挂固定 503，不留 404。
func TestIngress_Mounted503WhenIngressNil(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, // ingress nil
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/agent/sessions/" + uuid.New().String() + "/stream")
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (route must stay mounted, never 404)", resp.StatusCode)
	}
}

// JS 后补就绪 → 不重启进程即可通过 js 门：第一次请求 503，jsFn 灌入假
// JS 后同一 ingress 实例直接越过 503（无 token → 401，证明已到 js 门之后）。
func TestIngress_LateJSReadyNoRestart(t *testing.T) {
	var jsSlot atomic.Value // bus.JetStream
	ingress := NewIngress(nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ingress.SetJSFunc(func() bus.JetStream {
		if v := jsSlot.Load(); v != nil {
			return v.(bus.JetStream)
		}
		return nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/agent/sessions/{id}/stream", ingress.handleStream)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	url := ts.URL + "/v1/agent/sessions/" + uuid.New().String() + "/stream"

	// 1) JS 未就绪 → 503 no_jetstream
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("pre-ready status=%d want 503", resp.StatusCode)
	}

	// 2) JS 后补就绪 → 同一进程越过 503（缺 session_token → 401）
	jsSlot.Store(bus.JetStream(fakeIngressJS{}))
	resp2, err := http.Get(url)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-ready status=%d want 401 (js gate passed, token gate next)", resp2.StatusCode)
	}
}

func TestIngress_BadSessionID(t *testing.T) {
	ts, _, _, _ := ingressHarness(t)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/not-a-uuid/stream"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad uuid = %v, want 400", resp)
	}
}

func TestIngress_MissingSessionToken(t *testing.T) {
	ts, _, _, _ := ingressHarness(t)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + uuid.New().String() + "/stream"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token = %v, want 401", resp)
	}
}

func TestIngress_WrongScopeSessionToken(t *testing.T) {
	ts, signer, _, _ := ingressHarness(t)
	uid := uuid.New()
	sidA := uuid.New()
	sidB := uuid.New()
	tokA, _, _ := IssueSessionToken(signer, uid, sidA)

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sidB.String() + "/stream?session_token=" + tokA
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("scope mismatch = %v, want 401", resp)
	}
}

func TestIngress_SessionNotFound(t *testing.T) {
	ts, signer, _, _ := ingressHarness(t)
	uid := uuid.New()
	sid := uuid.New() // 不存在
	tok, _, _ := IssueSessionToken(signer, uid, sid)

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sid.String() + "/stream?session_token=" + tok
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing session = %v, want 404", resp)
	}
}

func TestIngress_FinalizedSession(t *testing.T) {
	ts, signer, store, _ := ingressHarness(t)
	uid := uuid.New()
	sess, _ := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "chat",
	})
	_ = FinalizeSessionResult(context.Background(), store, sess, FinalizeOpts{
		Status: "completed",
	})

	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sess.SessionID.String() + "/stream?session_token=" + tok

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("finalized session = %v, want 409", resp)
	}
}

// ── 不需要 DB 的纯单元测试 ────────────────────────────────

func TestIngress_ActiveCount(t *testing.T) {
	i := NewIngress(fakeIngressJS{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if i.ActiveCount() != 0 {
		t.Errorf("initial=%d", i.ActiveCount())
	}
	i.incActive()
	i.incActive()
	if i.ActiveCount() != 2 {
		t.Errorf("after 2 inc=%d", i.ActiveCount())
	}
	i.decActive()
	if i.ActiveCount() != 1 {
		t.Errorf("after 1 dec=%d", i.ActiveCount())
	}
}

func TestIngress_SessionSubjects(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	if got := SessionSubjectOut(sid); got != "biu.session."+sid+".out" {
		t.Errorf("out=%q", got)
	}
	if got := SessionSubjectIn(sid); got != "biu.session."+sid+".in" {
		t.Errorf("in=%q", got)
	}
}

// ── S3-5B parseSinceSeq + resume 路径 ────────────────────────

func TestIngress_ParseSinceSeq(t *testing.T) {
	cases := []struct {
		raw  string
		want uint64
	}{
		{"", 0},
		{"0", 0},
		{"42", 42},
		{"99999999", 99999999},
		{"-1", 0},                   // 负数（'-' 非数字）
		{"abc", 0},                  // 完全非数字
		{"12abc", 0},                // 部分非数字
		{"99999999999999999999", 0}, // 20 位超过 uint64
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/?since_seq="+c.raw, nil)
			if got := parseSinceSeq(r); got != c.want {
				t.Errorf("parseSinceSeq(%q) = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}

// TestIngress_Resume_NoRawJS：fakeIngressJS.RawJetStream() 返 nil →
// resume 路径走 desync fallback：发 SessionDesynced 帧 + close。
//
// 这个测试不需要 DB 之外的复杂依赖（不需真 NATS）。
func TestIngress_Resume_NoRawJS(t *testing.T) {
	ts, signer, store, _ := ingressHarness(t)

	// 创建一个活跃 session（不能是 finalize 状态）
	uid := uuid.New()
	sess, _ := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "chat",
	})

	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sess.SessionID.String() +
		"/stream?session_token=" + tok + "&since_seq=42"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		// dial 失败时 resp 可能 nil；ingress 会 upgrade 后立即关，所以
		// dial 通常成功
		if resp != nil {
			t.Logf("dial err with resp.Status=%d", resp.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	// 期望收到一帧 SessionDesynced 然后正常 close
	var sawDesynced bool
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if err := unmarshalJSON(raw, &frame); err != nil {
			continue
		}
		if frame.Type == "biumind.session_desynced" &&
			frame.SessionID == sess.SessionID.String() {
			sawDesynced = true
		}
	}
	if !sawDesynced {
		t.Errorf("expected SessionDesynced frame; got nothing or different type")
	}
}

// unmarshalJSON 是 json.Unmarshal 的简化包装（避免 ingress_test 直接
// import encoding/json 让 import 块更乱）。
func unmarshalJSON(data []byte, v any) error {
	return jsonUnmarshal(data, v)
}
