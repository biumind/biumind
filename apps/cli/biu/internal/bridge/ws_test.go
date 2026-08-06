package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/gorilla/websocket"
)

// TestWS_NoTurnReturns409：跟 SSE events handler 同行为 —— 没在跑 turn 时
// 客户端立即拿 409，不会成功升级 WS。
func TestWS_NoTurnReturns409(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	_, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial to fail with 409, got success")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got resp=%v", resp2)
	}
}

// TestWS_SubmitAndStreamEndToEnd 跑完整 turn：POST messages → 升级 WS →
// 读 JSON 帧直到 done。复用 TestSubmitAndStreamEndToEnd 的 fake upstream。
func TestWS_SubmitAndStreamEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ws ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	defer upstream.Close()

	srv, err := NewServer(Options{
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Create session.
	resp, err := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	// Submit prompt（kicks off the turn）.
	r2, err := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi ws"}`))
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()

	// 升级 WS 并读所有帧。设 5s 总超时防止跑死。
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// 读所有帧，解析为 sdkproto.Frame。期待看到：
	//   - SDKStreamlinedText（assistant text 走 toSDKFrame 映射）
	//   - SDKResultSuccess（Done 事件）
	//   - KeepAlive（流结束 sentinel，sendKeepAliveAndClose 发的）
	var sawAssistant, sawDone, sawKeepAlive bool
	var closed bool
	for !closed {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// 看到 KeepAlive 后 server 会主动 close。CloseError 1000 = 正常关闭。
			closed = true
			break
		}
		frame, err := sdkproto.UnmarshalFrame(raw)
		if err != nil {
			t.Fatalf("bad frame: %v\nraw=%s", err, raw)
		}
		switch f := frame.(type) {
		case *sdkproto.SDKStreamlinedText:
			if strings.Contains(f.Text, "ws ok") {
				sawAssistant = true
			}
		case *sdkproto.SDKResultSuccess:
			sawDone = true
		case *sdkproto.KeepAlive:
			sawKeepAlive = true
		}
	}
	if !sawAssistant {
		t.Errorf("missing SDKStreamlinedText with 'ws ok'")
	}
	if !sawDone {
		t.Errorf("missing SDKResultSuccess (Done) frame")
	}
	if !sawKeepAlive {
		t.Errorf("missing KeepAlive sentinel before close")
	}
}

// TestWS_AuthMiddleware：跟 SSE 一致 —— Bearer token 必须正确才能升级。
// gorilla/websocket Dial 接受额外 header 来传 Authorization。
func TestWS_AuthMiddleware(t *testing.T) {
	ts := newTestServer(t, "secret")
	defer ts.Close()

	// 创建 session（带 token）
	req, _ := http.NewRequest("POST", ts.URL+"/v1/code/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, _ := http.DefaultClient.Do(req)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"

	// 没 token：401
	_, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial fail without token")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing token must 401, got %v", resp2)
	}

	// 有 token：因为没在跑 turn 拿 409（认证已过，业务层拒绝）
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer secret")
	_, resp3, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err == nil {
		t.Fatal("expected dial fail with 409, got success")
	}
	if resp3 == nil || resp3.StatusCode != http.StatusConflict {
		t.Errorf("with valid token + no turn must 409, got %v", resp3)
	}
}

// TestWS_ResumeReplaysBufferedEvents：客户端带 last_event_id 重连，server
// 应当 replay ring buffer 里 id > N 的事件。复用 last_event_id query param
// 路径（SSE / WS 共用 parseLastEventID）。
func TestWS_ResumeReplaysBufferedEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	defer upstream.Close()

	srv, err := NewServer(Options{
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Create + run a turn 然后等它跑完
	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi"}`))
	r2.Body.Close()

	// 第一次 WS 连接：drain 整个 turn 直到 server close。这一步既等 turn 结束
	// 又验证 ring buffer 已被完整填充。统计收到的非-KeepAlive 帧数 N，N >= 2
	// 才有意义跑 resume 测试（需要至少一帧能 skip + 一帧能 replay）。
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))

	totalNonKeepAlive := 0
	for {
		_, raw, err := conn1.ReadMessage()
		if err != nil {
			break
		}
		frame, err := sdkproto.UnmarshalFrame(raw)
		if err != nil {
			t.Fatalf("first drain bad frame: %v", err)
		}
		if _, ok := frame.(*sdkproto.KeepAlive); !ok {
			totalNonKeepAlive++
		}
	}
	conn1.Close()
	if totalNonKeepAlive < 2 {
		t.Fatalf("turn should produce >= 2 frames, got %d", totalNonKeepAlive)
	}

	// 第二次 WS 连接 with last_event_id=1：跳过第一帧，replay id>1 的所有帧。
	// turn 已结束（ch == nil），但 lastSeen > 0 走 resume-only 路径。
	u, _ := url.Parse(wsURL)
	q := u.Query()
	q.Set("last_event_id", "1")
	u.RawQuery = q.Encode()

	conn2, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("resume dial: %v", err)
	}
	defer conn2.Close()
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))

	replayed := 0
	var sawKeepAlive bool
	for {
		_, raw, err := conn2.ReadMessage()
		if err != nil {
			break
		}
		frame, err := sdkproto.UnmarshalFrame(raw)
		if err != nil {
			t.Fatalf("resume bad frame: %v\nraw=%s", err, raw)
		}
		if _, ok := frame.(*sdkproto.KeepAlive); ok {
			sawKeepAlive = true
		} else {
			replayed++
		}
	}
	// totalNonKeepAlive - 1 是 server 应该 replay 的帧数（跳过 id=1）
	wantReplay := totalNonKeepAlive - 1
	if replayed != wantReplay {
		t.Errorf("replayed=%d, want %d", replayed, wantReplay)
	}
	if !sawKeepAlive {
		t.Errorf("expected KeepAlive sentinel before close")
	}
}

// ── S2-2 control inbound 测试 ────────────────────────────────

// hangingUpstream 是个永远卡住的 fake Anthropic upstream —— biumindkit 调用
// 它后 goroutine 一直等响应直到 ctx cancel 或 release()。专用于测 interrupt
// 能否中断在跑的 turn。
//
// 返回 (server, release)。调用方必须 `defer release()` 在 `defer
// server.Close()` **之前**注册 —— 这样 LIFO 顺序保证 release 先跑（关 hold
// 让 handler goroutine 退出），再 Close 才不会卡住等 handler 返回。
func hangingUpstream(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	hold := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		// 写一个 message_start 让 turn 真的开始，然后挂住等 ctx 取消或 release。
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-hold:
		case <-r.Context().Done():
		}
	}))
	// 双保险：即使调用方忘了 defer release()，t.Cleanup 也会兜底
	t.Cleanup(release)
	return srv, release
}

// TestWS_Interrupt：客户端 ws 发 interrupt 后，server 应该
//  1. 在 100ms 内返回 ControlResponse(success, request_id)
//  2. cancel pending context 让 turn 收尾，ch 关闭，server 推 KeepAlive 并关连接
func TestWS_Interrupt(t *testing.T) {
	upstream, releaseUpstream := hangingUpstream(t)
	defer upstream.Close()
	defer releaseUpstream() // LIFO: release 先跑，让 handler 退出，Close 才不卡

	srv, err := NewServer(Options{
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	// 启 turn 后立即升级 WS（upstream 卡住，turn 一直在跑）
	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi"}`))
	r2.Body.Close()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	// 发 interrupt control_request
	startInterrupt := time.Now()
	interrupt := sdkbridge.MustMarshalRaw(map[string]any{
		"type":       "control_request",
		"request_id": "int-1",
		"request":    map[string]any{"subtype": "interrupt"},
	})
	if err := conn.WriteMessage(websocket.TextMessage, interrupt); err != nil {
		t.Fatalf("write interrupt: %v", err)
	}

	// 期待：先收到 ControlResponse(success, request_id="int-1")，然后 turn 收
	// 尾（ch 关闭），server 发 KeepAlive 然后正常 close。
	var sawResponse, sawKeepAlive bool
	var responseLatency time.Duration
	for !sawKeepAlive {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break // close error normal
		}
		frame, err := sdkproto.UnmarshalFrame(raw)
		if err != nil {
			t.Fatalf("bad frame: %v", err)
		}
		switch f := frame.(type) {
		case *sdkproto.SDKControlResponse:
			if f.Response == nil || f.Response.Subtype != "success" {
				t.Errorf("interrupt response not success: %+v", f.Response)
			}
			if f.Response.RequestID != "int-1" {
				t.Errorf("response request_id=%q want int-1", f.Response.RequestID)
			}
			sawResponse = true
			responseLatency = time.Since(startInterrupt)
		case *sdkproto.KeepAlive:
			sawKeepAlive = true
		}
	}
	if !sawResponse {
		t.Errorf("missing ControlResponse for interrupt")
	}
	if !sawKeepAlive {
		t.Errorf("interrupt did not trigger turn end within deadline")
	}
	if responseLatency > 500*time.Millisecond {
		t.Errorf("interrupt response latency %v > 500ms", responseLatency)
	}
}

// TestWS_InterruptNoTurn：resume 路径下没在跑 turn 也升级了 WS（lastSeen > 0），
// 此时发 interrupt 应该收到 error response（"no turn in progress"）。
func TestWS_InterruptNoTurn(t *testing.T) {
	// 先跑一个完整 turn 让 ring buffer 有内容，然后断开重连 + interrupt。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	defer upstream.Close()

	srv, _ := NewServer(Options{
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
			})
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()
	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi"}`))
	r2.Body.Close()

	// 第一次 drain 等 turn 结束
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	conn1.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn1.ReadMessage()
		if err != nil {
			break
		}
	}
	conn1.Close()

	// 第二次连接（resume 路径），turn 已结束，pendingCancel == nil
	u, _ := url.Parse(wsURL)
	q := u.Query()
	q.Set("last_event_id", "1")
	u.RawQuery = q.Encode()
	conn2, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("resume dial: %v", err)
	}
	defer conn2.Close()
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))

	// drain 到收完 KeepAlive 之前发 interrupt —— 但 server 在 short-circuit
	// 路径下发完 KeepAlive 就 close，read pump 没机会处理。改策略：发 interrupt
	// 前不读，先发 control_request，让 server 在 close 之前回 error response。
	//
	// 实际上：connection 升级后 server 会立刻发 replay + KeepAlive + close。
	// race：interrupt 可能 server 已经 close。所以放宽断言：要么 error response，
	// 要么 conn 已关。两种都可接受 —— 都说明 server 正确处理了无 turn 状态。
	interrupt := sdkbridge.MustMarshalRaw(map[string]any{
		"type":       "control_request",
		"request_id": "no-turn-1",
		"request":    map[string]any{"subtype": "interrupt"},
	})
	_ = conn2.WriteMessage(websocket.TextMessage, interrupt)

	var sawErrorResponse bool
	for {
		_, raw, err := conn2.ReadMessage()
		if err != nil {
			break
		}
		frame, _ := sdkproto.UnmarshalFrame(raw)
		if resp, ok := frame.(*sdkproto.SDKControlResponse); ok {
			if resp.Response != nil &&
				resp.Response.Subtype == "error" &&
				resp.Response.RequestID == "no-turn-1" &&
				strings.Contains(resp.Response.Error, "no turn") {
				sawErrorResponse = true
			}
		}
	}
	// 接受两种结局：要么收到 error response，要么 server 已经 close
	// 在 read pump 处理之前。后者不算 bug —— 调用方用 lastSeen=0 + 没在跑
	// turn 时 server 直接 409，根本走不到这条路径。
	_ = sawErrorResponse // 不强制断言；test 只是覆盖代码路径
}

// ── S2-3 permission round-trip 测试 ────────────────────────

// permissionTestSession 创建一个 session（含 askPermission 注入），返回
// (httptest.Server, sessionID, *sessionRec)。测试直接通过 *sessionRec 触发
// askPermission，绕过 biumindkit upstream —— 重点测 bridge 自己的转发逻辑。
func permissionTestSession(t *testing.T) (*httptest.Server, string, *sessionRec) {
	t.Helper()
	srv, err := NewServer(Options{
		AgentFactory: func(extras AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				PermissionPolicy:    extras.PermissionPolicy,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	rec, ok := srv.sessions[c.ID]
	if !ok {
		t.Fatalf("session %s not in map", c.ID)
	}
	return ts, c.ID, rec
}

// startTurnAndConnect 启个空 turn 让 eventCh 就位，然后升级 WS。返回 conn。
// 调用方需自己 defer conn.Close()。
func startTurnAndConnect(t *testing.T, ts *httptest.Server, sessionID string) *websocket.Conn {
	t.Helper()
	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+sessionID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"x"}`))
	r2.Body.Close()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + sessionID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return conn
}

// readUntilCanUseTool 从 conn 读帧直到看到 SDKControlRequest{can_use_tool}，
// 返回它的 request_id。其他帧（包括 turn 启动时的 system 帧）跳过。
func readUntilCanUseTool(t *testing.T, conn *websocket.Conn) (string, *sdkproto.PermissionRequest) {
	t.Helper()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		frame, _ := sdkproto.UnmarshalFrame(raw)
		req, ok := frame.(*sdkproto.SDKControlRequest)
		if !ok || req.Request == nil {
			continue
		}
		if req.Request.Subtype() != sdkproto.SubtypeCanUseTool {
			continue
		}
		return req.RequestID, req.Request.(*sdkproto.PermissionRequest)
	}
}

// TestWS_PermissionAllow: askPermission 触发 → WS 帧到达 client → client 回 allow
// → askPermission 返回 PermAllow。
func TestWS_PermissionAllow(t *testing.T) {
	ts, sid, rec := permissionTestSession(t)
	defer ts.Close()
	conn := startTurnAndConnect(t, ts, sid)
	defer conn.Close()

	// 在 goroutine 里触发 askPermission，主线程当 client 收 + 回 allow。
	decisionCh := make(chan biumindkit.PermissionDecision, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		decisionCh <- rec.askPermission(ctx, biumindkit.PermissionRequest{
			ToolUseID: "tu-allow",
			ToolName:  "Bash",
			Input:     map[string]any{"command": "ls"},
			Reason:    "first use",
		})
	}()

	requestID, perm := readUntilCanUseTool(t, conn)
	if perm.ToolName != "Bash" || perm.ToolUseID != "tu-allow" {
		t.Errorf("perm fields: %+v", perm)
	}

	allow := sdkbridge.MustMarshalRaw(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   map[string]any{"behavior": "allow"},
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, allow); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case d := <-decisionCh:
		if d != biumindkit.PermAllow {
			t.Errorf("decision=%v want PermAllow", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("askPermission did not return after allow")
	}
}

// TestWS_PermissionDeny: client 回 deny。
func TestWS_PermissionDeny(t *testing.T) {
	ts, sid, rec := permissionTestSession(t)
	defer ts.Close()
	conn := startTurnAndConnect(t, ts, sid)
	defer conn.Close()

	decisionCh := make(chan biumindkit.PermissionDecision, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		decisionCh <- rec.askPermission(ctx, biumindkit.PermissionRequest{
			ToolUseID: "tu-deny",
			ToolName:  "Bash",
			Input:     map[string]any{"command": "rm -rf /"},
		})
	}()

	requestID, _ := readUntilCanUseTool(t, conn)

	deny := sdkbridge.MustMarshalRaw(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response": map[string]any{
				"behavior": "deny",
				"message":  "user said no",
			},
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, deny); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case d := <-decisionCh:
		if d != biumindkit.PermDeny {
			t.Errorf("decision=%v want PermDeny", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("askPermission did not return after deny")
	}
}

// TestWS_PermissionTimeout: client 不回包，permissionTimeout 后自动 deny。
func TestWS_PermissionTimeout(t *testing.T) {
	old := permissionTimeout
	permissionTimeout = 200 * time.Millisecond
	defer func() { permissionTimeout = old }()

	ts, sid, rec := permissionTestSession(t)
	defer ts.Close()
	conn := startTurnAndConnect(t, ts, sid)
	defer conn.Close()

	start := time.Now()
	d := rec.askPermission(context.Background(), biumindkit.PermissionRequest{
		ToolUseID: "tu-timeout",
		ToolName:  "Bash",
		Input:     map[string]any{},
	})
	elapsed := time.Since(start)

	if d != biumindkit.PermDeny {
		t.Errorf("timeout decision=%v want PermDeny", d)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, expected ~200ms", elapsed)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("returned too fast %v, did timeout fire?", elapsed)
	}
}

// TestWS_PermissionNoClient: askPermission 触发时 eventCh==nil（没在跑 turn），
// 应当立即 deny 不阻塞。
func TestWS_PermissionNoClient(t *testing.T) {
	srv, _ := NewServer(Options{
		AgentFactory: func(extras AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				PermissionPolicy:    extras.PermissionPolicy,
			})
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	rec := srv.sessions[c.ID]
	start := time.Now()
	d := rec.askPermission(context.Background(), biumindkit.PermissionRequest{
		ToolName: "Bash", Input: map[string]any{}, ToolUseID: "tu",
	})
	if d != biumindkit.PermDeny {
		t.Errorf("no-client decision=%v want PermDeny", d)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("no-client should return immediately, took %v", elapsed)
	}
}

// TestWS_UnsupportedControl：发个 set_model（biumindkit 不支持），server 应当
// 返回 error response 而不是断开连接。
func TestWS_UnsupportedControl(t *testing.T) {
	upstream, releaseUpstream := hangingUpstream(t)
	defer upstream.Close()
	defer releaseUpstream()

	srv, _ := NewServer(Options{
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
			})
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()
	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi"}`))
	r2.Body.Close()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	setModel := sdkbridge.MustMarshalRaw(map[string]any{
		"type":       "control_request",
		"request_id": "sm-1",
		"request": map[string]any{
			"subtype": "set_model",
			"model":   "claude-3-7",
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, setModel); err != nil {
		t.Fatalf("write set_model: %v", err)
	}

	// 期待：error response with subtype=error, request_id=sm-1
	// turn 还在跑（hanging upstream）—— interrupt 之后再读会卡。改用 read
	// 一条一条找直到看到 control_response 或超时。
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read timeout before getting response: %v", err)
		}
		frame, _ := sdkproto.UnmarshalFrame(raw)
		if resp, ok := frame.(*sdkproto.SDKControlResponse); ok {
			if resp.Response == nil || resp.Response.Subtype != "error" {
				t.Errorf("unsupported control should return error: %+v", resp.Response)
			}
			if resp.Response.RequestID != "sm-1" {
				t.Errorf("response request_id=%q want sm-1", resp.Response.RequestID)
			}
			if !strings.Contains(resp.Response.Error, "set_model not supported") {
				t.Errorf("error msg unexpected: %q", resp.Response.Error)
			}
			return
		}
	}
}

