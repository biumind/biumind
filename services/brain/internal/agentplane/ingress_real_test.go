// S3-5C ingress 真 NATS broker + DB 集成测试。
//
// 跟 ingress_test.go（mock JS）的区别：这里用 *真* bus.JetStream，验证：
//
//	1. End-to-end fanout：worker → PublishSessionFrame → ingress 订阅 → WS
//	2. Multi-client：同 session 两个 WS 各起独立 ephemeral consumer，都收到
//	3. Resume：publish N 帧 → 客户端带 since_seq=k 连 → 收到 [k+1..N]
//	4. Inbound：WS 写帧 → ingress publish 到 `.in` → 另一个订阅者收到
//
// 双重 skip：DATABASE_URL 缺 skip（session 必须落 DB），NATS broker dial
// 失败 skip。CI 同时配齐才会跑这一族测试。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

// realIngressHarness 起 httptest server，用真 NATS bus.JetStream + 真 DB
// 装 Ingress。任一 dep 不可用就 skip。返回的 cleanup 在 t.Cleanup 已注册。
func realIngressHarness(t *testing.T) (
	ts *httptest.Server,
	signer *bauth.Signer,
	store *Store,
	js bus.JetStream,
	pool *pgxpool.Pool,
) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — ingress real test needs DB for session lookup")
	}
	_, _, js = natsBrokerOrSkip(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	_, _ = pool.Exec(context.Background(),
		`TRUNCATE agent_environments, agent_sessions, agent_session_results CASCADE`)

	store = NewStore(pool)
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	signer = bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, SessionTokenTTL)

	// 真 ingress + 真 JS。EnsureSessionStream 让 BIU_SESSIONS 存在
	q := NewQueue(js)
	if err := q.EnsureSessionStream(context.Background()); err != nil {
		t.Fatalf("EnsureSessionStream: %v", err)
	}
	ingress := NewIngress(js, store, verifier,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := NewServer(store, verifier, signer, q, ingress,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts = httptest.NewServer(mux)
	t.Cleanup(func() { ts.Close(); pool.Close() })
	return ts, signer, store, js, pool
}

// dialSession 升级 WS 到 /v1/agent/sessions/{id}/stream。返回 conn + 已校验
// 升级成功（resp 200）。caller 负责 conn.Close()。
func dialSession(t *testing.T, ts *httptest.Server, sessionID uuid.UUID, sessionTok string, sinceSeq string) *websocket.Conn {
	t.Helper()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sessionID.String() +
		"/stream?session_token=" + sessionTok
	if sinceSeq != "" {
		wsURL += "&since_seq=" + sinceSeq
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial: %v (status=%d)", err, resp.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	return conn
}

// readFramesFor 在 deadline 内读所有帧解 JSON 出来。读到 EOF / 超时就返回。
func readFramesFor(t *testing.T, conn *websocket.Conn, want int, deadline time.Duration) []map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(deadline))
	frames := make([]map[string]any, 0, want)
	for len(frames) < want {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f map[string]any
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		frames = append(frames, f)
	}
	return frames
}

// TestIngressReal_EndToEnd ：开 WS → worker 路径 PublishSessionFrame
// 到 .out → WS 收到这一帧。验证 ingress live-subscribe 真的串起来。
func TestIngressReal_EndToEnd(t *testing.T) {
	ts, signer, store, js, _ := realIngressHarness(t)
	q := NewQueue(js)

	uid := uuid.New()
	sess, err := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "agent",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)

	conn := dialSession(t, ts, sess.SessionID, tok, "")
	defer conn.Close()

	// 等 ingress 真订阅上 —— bus.Subscribe 是异步建 consumer，立即 publish
	// 可能漏第一帧。简单 sleep 100ms，CI 友好。
	time.Sleep(100 * time.Millisecond)

	frame := []byte(`{"type":"biumind.streamlined_text","text":"hello","uuid":"u1","session_id":"` + sess.SessionID.String() + `"}`)
	if err := q.PublishSessionFrame(context.Background(), sess.SessionID, frame); err != nil {
		t.Fatalf("PublishSessionFrame: %v", err)
	}

	got := readFramesFor(t, conn, 1, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("got %d frames, want 1", len(got))
	}
	if got[0]["text"] != "hello" {
		t.Errorf("frame body lost: %+v", got[0])
	}
}

// TestIngressReal_MultiClient ：两个 WS 连同一 session → publish 一帧 →
// 两个都收到。每 WS 独立 ephemeral durable，互不抢 ack。
func TestIngressReal_MultiClient(t *testing.T) {
	ts, signer, store, js, _ := realIngressHarness(t)
	q := NewQueue(js)

	uid := uuid.New()
	sess, err := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "agent",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)

	conn1 := dialSession(t, ts, sess.SessionID, tok, "")
	defer conn1.Close()
	conn2 := dialSession(t, ts, sess.SessionID, tok, "")
	defer conn2.Close()
	time.Sleep(150 * time.Millisecond)

	frame := []byte(`{"type":"biumind.streamlined_text","text":"broadcast","uuid":"u2","session_id":"` + sess.SessionID.String() + `"}`)
	if err := q.PublishSessionFrame(context.Background(), sess.SessionID, frame); err != nil {
		t.Fatalf("PublishSessionFrame: %v", err)
	}

	var wg sync.WaitGroup
	results := make([][]map[string]any, 2)
	for i, c := range []*websocket.Conn{conn1, conn2} {
		wg.Add(1)
		go func(idx int, c *websocket.Conn) {
			defer wg.Done()
			results[idx] = readFramesFor(t, c, 1, 2*time.Second)
		}(i, c)
	}
	wg.Wait()

	for i, frames := range results {
		if len(frames) != 1 {
			t.Errorf("conn%d got %d frames, want 1", i+1, len(frames))
			continue
		}
		if frames[0]["text"] != "broadcast" {
			t.Errorf("conn%d body lost: %+v", i+1, frames[0])
		}
	}
}

// TestIngressReal_Replay ：先 publish 3 帧 → 客户端用 since_seq=N 连 →
// 应收到 N+1 .. (3 帧里所有 seq > N 的)。
//
// stream 是共享 BIU_SESSIONS，FirstSeq 跟着别的测试 / 旧消息走，无法预测
// 具体 seq 数字。我们用 seq 偏移：先记录"publish 之前的 stream LastSeq"为
// baseline，再 publish 3 帧 → 它们的 seq 是 baseline+1, +2, +3。客户端
// 用 since_seq=baseline+1 → 应收到第 2 帧、第 3 帧 (2 条)。
func TestIngressReal_Replay(t *testing.T) {
	ts, signer, store, js, _ := realIngressHarness(t)
	q := NewQueue(js)

	uid := uuid.New()
	sess, err := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "agent",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)

	rawJS := js.RawJetStream()
	if rawJS == nil {
		t.Skip("RawJetStream unavailable; replay test needs raw JS")
	}
	stream, err := rawJS.Stream(context.Background(), SessionStreamName)
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	baseline := info.State.LastSeq

	// publish 3 帧。它们的 seq 是 baseline+1, +2, +3
	for i := 1; i <= 3; i++ {
		frame := []byte(fmt.Sprintf(
			`{"type":"biumind.streamlined_text","text":"msg-%d","uuid":"u%d","session_id":"%s"}`,
			i, i, sess.SessionID))
		if err := q.PublishSessionFrame(context.Background(), sess.SessionID, frame); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	// 让 broker 写盘
	time.Sleep(100 * time.Millisecond)

	// since_seq=baseline+1 → OrderedConsumer OptStartSeq=baseline+2 → 应该
	// 看到 msg-2 + msg-3
	conn := dialSession(t, ts, sess.SessionID, tok, fmt.Sprintf("%d", baseline+1))
	defer conn.Close()

	got := readFramesFor(t, conn, 2, 3*time.Second)
	if len(got) < 2 {
		t.Fatalf("got %d frames, want ≥2; raw=%+v", len(got), got)
	}
	// 顺序应为 msg-2, msg-3
	if got[0]["text"] != "msg-2" {
		t.Errorf("first replay frame: text=%v want msg-2", got[0]["text"])
	}
	if got[1]["text"] != "msg-3" {
		t.Errorf("second replay frame: text=%v want msg-3", got[1]["text"])
	}
}

// TestIngressReal_InboundFrame ：客户端 WS 写一帧 → ingress publish 到
// biu.session.<sid>.in → 另一个独立订阅者收到。验证入站路径（worker 端
// 在 S3-8 daemon 端就靠 .in 收回 user message）。
func TestIngressReal_InboundFrame(t *testing.T) {
	ts, signer, store, js, _ := realIngressHarness(t)

	uid := uuid.New()
	sess, err := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "agent",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	tok, _, _ := IssueSessionToken(signer, uid, sess.SessionID)

	// 起独立订阅者听 .in
	inbox := make(chan []byte, 4)
	subSpec := bus.ConsumerSpec{
		Stream:        SessionStreamName,
		Durable:       "test-in-listener-" + uniqStreamSuffix(t),
		FilterSubject: SessionSubjectIn(sess.SessionID.String()),
		AckWait:       10 * time.Second,
		MaxDeliver:    1,
	}
	sub, err := js.Subscribe(context.Background(), subSpec, func(_ context.Context, m *bus.Message) error {
		inbox <- m.Body
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe in: %v", err)
	}
	defer sub.Drain()
	time.Sleep(100 * time.Millisecond)

	conn := dialSession(t, ts, sess.SessionID, tok, "")
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	out := []byte(`{"type":"user_message","text":"hello-from-client"}`)
	if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	select {
	case got := <-inbox:
		if !strings.Contains(string(got), "hello-from-client") {
			t.Errorf("body lost: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive inbound frame on .in subject")
	}
}
