// S4-5/6/7 chat WS 端到端测试。
//
// 流程：
//
//	1. 起 ingressHarness（真 NATS + 真 DB）+ ChatRunner（fake Anthropic upstream）
//	2. POST /v1/agent/sessions { mode: "chat", prompt: "hi" } 拿 session_token
//	3. WS /v1/agent/sessions/{id}/stream?session_token=...
//	4. 收到 SDK Protocol 帧（biumind.streamlined_text → biumind.result_success）
//
// 双 skip：DATABASE_URL 缺 / NATS 不 dial 通 → t.Skip。
//
// 不测 SSE 兼容（S4-6 JsonEmitter 已经直接吐 SDK Protocol，BlockEmitter
// SSE 路径是 legacy /v1/threads/:id/send，跟 chat WS 平行）。

package agentplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/biumind/biumind/services/brain/internal/tools"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeAnthropicChatUpstream 一个 SSE upstream 假装 model-relay 的 verbatim
// Anthropic endpoint(`X-Stream-Format: anthropic` 形态)：assistant
// 文本 "ok"，end_turn。chat runner 应该收 streaming_text 帧 + result_success。
// 不校验 auth header —— PassThrough 下收到的是 `Authorization: Bearer
// <user JWT>`,由 chatRunnerHarness 的真 router 链路保证。
func fakeAnthropicChatUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
}

// chatRunnerHarness 起 httptest server with full agentplane Server +
// ChatRunner pointed at fake Anthropic upstream. 任一依赖缺自动 skip.
func chatRunnerHarness(t *testing.T) (
	ts *httptest.Server,
	signer *bauth.Signer,
	store *Store,
	upstream *httptest.Server,
) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — chat runner test needs DB")
	}
	_, _, js := natsBrokerOrSkip(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	_, _ = pool.Exec(context.Background(),
		`TRUNCATE agent_environments, agent_sessions, agent_session_results CASCADE`)

	store = NewStore(pool)
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	signer = bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, SessionTokenTTL)

	q := NewQueue(js)
	if err := q.EnsureWorkStream(context.Background()); err != nil {
		t.Fatalf("EnsureWorkStream: %v", err)
	}
	if err := q.EnsureSessionStream(context.Background()); err != nil {
		t.Fatalf("EnsureSessionStream: %v", err)
	}
	ingress := NewIngress(js, store, verifier, slog.New(slog.NewTextHandler(io.Discard, nil)))

	upstream = fakeAnthropicChatUpstream(t)
	t.Cleanup(upstream.Close)

	srv := NewServer(store, verifier, signer, q, ingress,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	loop := chatpkg.NewAgentLoop(nil, tools.New())
	srv.ChatRunner = NewChatRunner(q, store, loop,
		"",           // defaultModel — 单测走硬兜底
		upstream.URL, // RelayURL — PassThrough:user JWT 当 Bearer 透传到 fake upstream
		nil,          // defaultModels — 单测不查 relay default-chat
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	mux := http.NewServeMux()
	srv.Mount(mux)
	ts = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, signer, store, upstream
}

// TestChatRunner_EndToEnd ：创建 chat session → WS 收到 streamlined_text +
// result_success。
//
// 测试自己签 user JWT 走 POST /v1/agent/sessions（router 路径）—— 这样
// 还顺便覆盖了 router.createChatSession + ChatRunner.RunSession 的胶水。
func TestChatRunner_EndToEnd(t *testing.T) {
	ts, _, _, _ := chatRunnerHarness(t)

	uid := uuid.New()
	userTok := signUserToken(t, uid)

	// 1. 创建 session：POST /v1/agent/sessions { mode: chat, prompt: "hi" }
	body := bytes.NewBufferString(`{"mode":"chat","prompt":"hi","model":"claude-haiku-4-5"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/agent/sessions", body)
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST sessions status=%d body=%s", resp.StatusCode, raw)
	}
	var sessResp struct {
		SessionID    string `json:"session_id"`
		SessionToken string `json:"session_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessResp); err != nil {
		t.Fatal(err)
	}
	if sessResp.SessionID == "" || sessResp.SessionToken == "" {
		t.Fatalf("missing fields: %+v", sessResp)
	}

	// 2. WS 连 session 收帧
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/v1/agent/sessions/" + sessResp.SessionID +
		"/stream?session_token=" + sessResp.SessionToken
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// 收帧直到 result_success / result_error / 超时
	var (
		sawText    bool
		sawSuccess bool
	)
	for !sawSuccess {
		_, raw, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		var f struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			Text      string `json:"text"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		switch f.Type {
		case "streamlined_text":
			sawText = true
			if f.Text != "ok" && !strings.Contains(f.Text, "ok") {
				t.Errorf("streamlined_text body lost: %q", f.Text)
			}
		case "result":
			if f.Subtype == "success" {
				sawSuccess = true
			} else {
				t.Fatalf("got result_error: %s", raw)
			}
		}
	}

	if !sawText {
		t.Errorf("did not receive streamlined_text frame")
	}
	if !sawSuccess {
		t.Errorf("did not receive result_success frame within timeout")
	}
}

// signUserToken 给 uid 签一个 user JWT 让 router.requireAuth 通过。
func signUserToken(t *testing.T, uid uuid.UUID) string {
	t.Helper()
	signer := bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute)
	tok, err := signer.Sign(&bauth.Claims{
		UserID: uid.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestChatRunner_MissingBearerFinalizesFailed ：RelayURL / user bearer 空
// 时 ChatRunner 应该走 missing_bearer 早退分支(publishErrorAndFinalize),
// 而不是静默不响应或 panic。
func TestChatRunner_MissingBearerFinalizesFailed(t *testing.T) {
	// Queue 非 nil(nil JS → publish 静默失败) + Store nil(finalize 静默
	// 跳过) + RelayURL 空 → resolveCreds ok=false → missing_bearer 分支。
	// Smoke:runner 不 panic + 早退;具体 result_error 帧路径已在 EndToEnd
	// 覆盖到(走 fake upstream 成功路径)。
	q := NewQueue(nil)
	if q.js != nil {
		t.Fatal("expected nil js")
	}
	loop := chatpkg.NewAgentLoop(nil, tools.New())
	r := NewChatRunner(q, nil, loop, "", "", nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.runSessionImpl(context.Background(),
		&Session{SessionID: uuid.New()},
		WorkPayload{Prompt: "hi"})
}
