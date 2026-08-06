// SSE handler 测试 — 重点覆盖 v2-3 Last-Event-ID 续传路径.
//
// 由于 sse.go 走 net/http + chunked stream, 测试用 httptest.NewServer
// + http.Get 模拟客户端建连 + 收 SSE chunks 验证.

package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/realtime/internal/hub"
	"github.com/biumind/biumind/services/realtime/internal/ledger"
)

// stubAuthz — 永远 true.
type stubAuthz struct{}

func (stubAuthz) CanSubscribe(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

const testSecret = "test-jwt-secret-realtime-sse-32chars"
const testIssuer = "https://identity.biumind.test"
const testAudience = "biumind-api"

func newTestServer(t *testing.T, l *ledger.Ledger) (*httptest.Server, string) {
	t.Helper()
	signer := bauth.NewSigner(testSecret, testIssuer, testAudience, time.Hour)
	verifier := bauth.NewVerifier(testSecret, testIssuer, testAudience)

	s := &Server{
		Hub:             hub.NewHub(64),
		Ledger:          l,
		Authz:           stubAuthz{},
		Verifier:        verifier,
		HeartbeatPeriod: 30 * time.Second, // 不在测试期内触发
		Logger:          slog.Default(),
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tok, err := signer.Sign(&bauth.Claims{UserID: "u1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return srv, tok
}

// readSSEFrames — 从 chunked body 读 N 帧, 返每帧 (id, kind, payload).
type sseFrame struct {
	id      string
	event   string
	dataRaw string
}

func readSSEFrames(t *testing.T, body io.Reader, want int, deadline time.Duration) []sseFrame {
	t.Helper()
	br := bufio.NewReader(body)
	out := make([]sseFrame, 0, want)
	cur := sseFrame{event: "message"}
	dataBuf := bytes.Buffer{}
	timeoutAt := time.Now().Add(deadline)

	type lineRes struct {
		line string
		err  error
	}
	for time.Now().Before(timeoutAt) && len(out) < want {
		ch := make(chan lineRes, 1)
		go func() {
			line, err := br.ReadString('\n')
			ch <- lineRes{line, err}
		}()
		var lr lineRes
		select {
		case lr = <-ch:
		case <-time.After(time.Until(timeoutAt)):
			return out
		}
		if lr.err != nil {
			return out
		}
		line := strings.TrimRight(lr.line, "\r\n")
		if line == "" {
			if dataBuf.Len() > 0 {
				cur.dataRaw = dataBuf.String()
				out = append(out, cur)
				cur = sseFrame{event: "message"}
				dataBuf.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // heartbeat / comment
		}
		if strings.HasPrefix(line, "id: ") {
			cur.id = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "event: ") {
			cur.event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

// 1. 不带 Last-Event-ID — 仅收 open frame, 没有 replay
func TestSSE_NoLastEventID_NoReplay(t *testing.T) {
	l := ledger.New(time.Hour, 256)
	// 提前 append 一些 events 到 ledger
	l.Append(ledger.Event{ID: "01", Topic: "t1", Kind: "msg",
		Payload: []byte(`{"v":1}`), TS: time.Now()})
	l.Append(ledger.Event{ID: "02", Topic: "t1", Kind: "msg",
		Payload: []byte(`{"v":2}`), TS: time.Now()})

	srv, tok := newTestServer(t, l)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	frames := readSSEFrames(t, resp.Body, 1, 500*time.Millisecond)
	if len(frames) < 1 {
		t.Fatalf("expected open frame, got %d", len(frames))
	}
	// 第一帧应为 open (无 Last-Event-ID 时不回放)
	if !strings.Contains(frames[0].dataRaw, `"kind":"open"`) {
		t.Fatalf("first frame should be open, got %+v", frames[0])
	}
}

// 2. 带 Last-Event-ID — 收到 since 之后的事件 + open frame
func TestSSE_WithLastEventID_Replays(t *testing.T) {
	l := ledger.New(time.Hour, 256)
	now := time.Now()
	l.Append(ledger.Event{ID: "01", Topic: "t1", Kind: "msg",
		Payload: []byte(`{"v":1}`), TS: now})
	l.Append(ledger.Event{ID: "02", Topic: "t1", Kind: "msg",
		Payload: []byte(`{"v":2}`), TS: now})
	l.Append(ledger.Event{ID: "03", Topic: "t1", Kind: "msg",
		Payload: []byte(`{"v":3}`), TS: now})

	srv, tok := newTestServer(t, l)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "01") // 期望 replay 02 + 03
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 3, 1*time.Second)
	if len(frames) < 3 {
		t.Fatalf("expected 3 frames (02 + 03 + open), got %d: %+v", len(frames), frames)
	}
	// 前两帧应是 replay 的 02, 03 (按 ID 升序)
	if frames[0].id != "02" {
		t.Fatalf("first replay = %s, want 02", frames[0].id)
	}
	if frames[1].id != "03" {
		t.Fatalf("second replay = %s, want 03", frames[1].id)
	}
	// 第三帧是 open
	if !strings.Contains(frames[2].dataRaw, `"kind":"open"`) {
		t.Fatalf("third frame should be open, got %+v", frames[2])
	}
}

// 3. Last-Event-ID 在 ledger 之外 (太老 / 不存在) — 不阻塞 + 至少返 open
func TestSSE_LastEventID_NotInLedger(t *testing.T) {
	l := ledger.New(time.Hour, 256)
	l.Append(ledger.Event{ID: "10", Topic: "t1", Kind: "msg",
		Payload: []byte(`{}`), TS: time.Now()})

	srv, tok := newTestServer(t, l)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "99")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 1, 500*time.Millisecond)
	if len(frames) < 1 {
		t.Fatalf("expected open frame, got %d", len(frames))
	}
	// since=99 > ledger max id → replay 空 → 仅 open
	if !strings.Contains(frames[0].dataRaw, `"kind":"open"`) {
		t.Fatalf("first frame should be open, got %+v", frames[0])
	}
}

// 4. 多 topic 续传只回放订阅 topic 的事件
func TestSSE_LastEventID_TopicFilter(t *testing.T) {
	l := ledger.New(time.Hour, 256)
	now := time.Now()
	l.Append(ledger.Event{ID: "10", Topic: "t-a", Kind: "msg",
		Payload: []byte(`{"a":1}`), TS: now})
	l.Append(ledger.Event{ID: "11", Topic: "t-b", Kind: "msg",
		Payload: []byte(`{"b":1}`), TS: now})
	l.Append(ledger.Event{ID: "12", Topic: "t-a", Kind: "msg",
		Payload: []byte(`{"a":2}`), TS: now})

	srv, tok := newTestServer(t, l)
	// 只订 t-a, 不应收到 t-b 即使在 since 之后.
	// since="10" 等于 t-a min retained id (10), 在保留窗内 → 不 desync,
	// 期望 replay 12 (10 不>since, 11 不在订阅 topic, 12>since 且属 t-a).
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t-a", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "10")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 3, 1*time.Second)
	// 期望 t-a 的 2 帧 (10, 12) + 1 个 open. 不含 t-b 的 11.
	gotTopics := []string{}
	for _, f := range frames {
		// 判 topic: 看 dataRaw
		if strings.Contains(f.dataRaw, `"topic":"t-a"`) {
			gotTopics = append(gotTopics, "t-a")
		} else if strings.Contains(f.dataRaw, `"topic":"t-b"`) {
			gotTopics = append(gotTopics, "t-b")
		} else if strings.Contains(f.dataRaw, `"topic":"system"`) {
			gotTopics = append(gotTopics, "system")
		}
	}
	if contains(gotTopics, "t-b") {
		t.Fatalf("should not replay t-b: %+v", gotTopics)
	}
	if !contains(gotTopics, "t-a") {
		t.Fatalf("should replay t-a: %+v", gotTopics)
	}
}

// v2-6: Last-Event-ID 在 ring 已淘汰的位置 (gap) — 应发 desync 帧, 不 replay
func TestSSE_LastEventID_DesyncFrame(t *testing.T) {
	// ring 容量 3, 写 5 条 → 03/04/05 保留, 01/02 已淘汰
	l := ledger.New(time.Hour, 3)
	now := time.Now()
	l.Append(ledger.Event{ID: "01", Topic: "t1", Kind: "msg", Payload: []byte(`{"v":1}`), TS: now})
	l.Append(ledger.Event{ID: "02", Topic: "t1", Kind: "msg", Payload: []byte(`{"v":2}`), TS: now})
	l.Append(ledger.Event{ID: "03", Topic: "t1", Kind: "msg", Payload: []byte(`{"v":3}`), TS: now})
	l.Append(ledger.Event{ID: "04", Topic: "t1", Kind: "msg", Payload: []byte(`{"v":4}`), TS: now})
	l.Append(ledger.Event{ID: "05", Topic: "t1", Kind: "msg", Payload: []byte(`{"v":5}`), TS: now})

	srv, tok := newTestServer(t, l)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "01") // 01 < min retained 03 → desync
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 2, 1*time.Second)
	if len(frames) < 2 {
		t.Fatalf("expected desync + open, got %d: %+v", len(frames), frames)
	}
	if !strings.Contains(frames[0].dataRaw, `"kind":"desync"`) {
		t.Fatalf("first frame should be desync, got %+v", frames[0])
	}
	if !strings.Contains(frames[0].dataRaw, `"code":4009`) {
		t.Fatalf("desync frame must include code 4009, got %s", frames[0].dataRaw)
	}
	if !strings.Contains(frames[0].dataRaw, `"reason":"last_event_id_beyond_retention"`) {
		t.Fatalf("desync frame must include reason, got %s", frames[0].dataRaw)
	}
	// 关键: desync 之后不应 replay 03/04/05 (ledger.Replay 被跳过), 直接 open
	if !strings.Contains(frames[1].dataRaw, `"kind":"open"`) {
		t.Fatalf("second frame should be open (no replay after desync), got %+v", frames[1])
	}
	// 双保险: 帧总数应只 2 (desync + open), 不含任何 t1 msg replay
	for _, f := range frames {
		if strings.Contains(f.dataRaw, `"kind":"msg"`) {
			t.Fatalf("desync 后不应 replay msg, got %+v", f)
		}
	}
}

// 5. Last-Event-ID 过期 (超过 retention) — 不回放
func TestSSE_LastEventID_OverRetention(t *testing.T) {
	l := ledger.New(50*time.Millisecond, 256)
	l.Append(ledger.Event{ID: "01", Topic: "t1", Kind: "msg",
		Payload: []byte(`{}`), TS: time.Now().Add(-1 * time.Hour)})

	srv, tok := newTestServer(t, l)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/realtime/stream?topics=t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "00")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 1, 500*time.Millisecond)
	// 过期事件不该 replay, 仅 open
	for _, f := range frames {
		if f.id == "01" {
			t.Fatalf("过期事件不应 replay: %+v", f)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// 防止 fmt 包未用 (writeFrame 错误路径若用 fmt.Errorf)
var _ = fmt.Sprintf
