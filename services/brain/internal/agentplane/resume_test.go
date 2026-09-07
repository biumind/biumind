// durable resume 端点 + GET elicitations + GET result 的集成测试（P3-c）。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// newResumeHarness 比 newAPIHarness 多挂 ChatStore + ChatRunner —— resume
// 成功路径要真落 synthetic user 消息 + 真派发 turn。
func newResumeHarness(t *testing.T, withRunner bool) *apiHarness {
	t.Helper()
	h := newAPIHarness(t)
	chatStore := chatpkg.New(h.pool)
	h.server.Close() // 重建 mux 挂全依赖
	srv := &Server{
		Store:     h.store,
		Verifier:  bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		Signer:    bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, SessionTokenTTL),
		Queue:     NewQueue(h.fakeQueue),
		ChatStore: chatStore,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv.Transcript = NewTranscriptRecorder(chatStore, srv.Logger)
	if withRunner {
		// Loop=nil：runner goroutine 会在 1s 启动窗口后 finalize failed —
		// 测试只断言 resume 响应 + 落库副作用（都在 1s 前完成），不碰
		// runner 后续行为。
		srv.ChatRunner = NewChatRunner(srv.Queue, h.store, nil, "", "", nil, srv.Logger)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	h.server = httptest.NewServer(mux)
	return h
}

// seedPausedChatWithThread 建 paused chat session + 确保 chat.threads 行。
func seedPausedChatWithThread(t *testing.T, h *apiHarness) *Session {
	t.Helper()
	sess := seedChatSession(t, h, "paused")
	if err := chatpkg.New(h.pool).EnsureThread(
		context.Background(), *sess.ThreadID, sess.UserID, "t", ""); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestResume_Unauthorized(t *testing.T) {
	h := newResumeHarness(t, true)
	defer h.close()
	resp := h.req(t, "POST", "/v1/agent/sessions/"+uuid.NewString()+"/resume", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestResume_NotFound_And_NotPaused(t *testing.T) {
	h := newResumeHarness(t, true)
	defer h.close()

	// 不存在 / 跨用户 → 404
	resp := h.req(t, "POST", "/v1/agent/sessions/"+uuid.NewString()+"/resume", h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing session: status=%d want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// active session → 409 session_not_paused
	sess := seedChatSession(t, h, "active")
	resp = h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/resume", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("active session: status=%d want 409", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["error"].(map[string]any)["code"] != "session_not_paused" {
		t.Errorf("code=%v want session_not_paused", got["error"])
	}
}

func TestResume_RejectsDaemonMode(t *testing.T) {
	h := newResumeHarness(t, true)
	defer h.close()
	ctx := context.Background()
	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := h.store.InsertSession(ctx, CreateSessionReq{
		UserID: uid, EnvironmentID: &env.EnvironmentID, Mode: "agent", State: "paused",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/resume", h.mintToken(uid), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status=%d want 409", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["error"].(map[string]any)["code"] != "not_resumable_mode" {
		t.Errorf("code=%v want not_resumable_mode", got["error"])
	}
}

func TestResume_PendingConflict_ThenSuccess(t *testing.T) {
	h := newResumeHarness(t, true)
	defer h.close()
	ctx := context.Background()
	sess := seedPausedChatWithThread(t, h)

	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("用哪个方案?")); err != nil {
		t.Fatal(err)
	}

	// 1) pending 未答 → 409 elicitation_pending,响应带待答快照
	resp := h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/resume", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want 409", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["error"].(map[string]any)["code"] != "elicitation_pending" {
		t.Fatalf("code=%v want elicitation_pending", got["error"])
	}
	list, _ := got["elicitations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["question"] != "用哪个方案?" {
		t.Fatalf("elicitations=%v", got["elicitations"])
	}
	// state 没被动
	s, _ := h.store.GetSession(ctx, sess.UserID, sess.SessionID)
	if s.State != "paused" {
		t.Fatalf("state=%q want paused (untouched)", s.State)
	}

	// 2) 用户作答（模拟 ingress 迟到作答 CAS）→ resume 成功
	if _, _, ok, err := h.store.ResolveElicitationCAS(ctx, rid,
		[]byte(`{"action":"accept","content":{"answer":"方案A"}}`)); err != nil || !ok {
		t.Fatalf("CAS ok=%v err=%v", ok, err)
	}
	resp = h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/resume", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 200", resp.StatusCode, body)
	}
	got = decodeJSON[map[string]any](t, resp)
	if got["resumed"] != true || got["state"] != "active" || got["answers_injected"].(float64) != 1 {
		t.Errorf("resp=%v", got)
	}

	// synthetic user 消息落库（多题合并格式）
	var content string
	if err := h.pool.QueryRow(ctx, `
		SELECT content FROM chat.messages
		 WHERE thread_id = $1 AND role = 'user' ORDER BY position DESC LIMIT 1
	`, *sess.ThreadID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "我已回答你的提问") ||
		!strings.Contains(content, "Q: 用哪个方案?") ||
		!strings.Contains(content, "A: 方案A") {
		t.Errorf("synthetic message=%q", content)
	}

	// 答案已消费（防下次 resume 重复注入）
	e, _ := h.store.GetElicitation(ctx, rid)
	if e.ConsumedAt == nil {
		t.Errorf("consumed_at not set")
	}

	// SessionResumed 帧推到了 .out
	h.fakeQueue.mu.Lock()
	var sawResumed bool
	for _, p := range h.fakeQueue.publishes {
		if p.Subject == SessionSubjectOut(sess.SessionID.String()) {
			if raw, ok := p.Payload.(json.RawMessage); ok && strings.Contains(string(raw), "session_resumed") {
				sawResumed = true
			}
		}
	}
	h.fakeQueue.mu.Unlock()
	if !sawResumed {
		t.Errorf("session_resumed frame not published")
	}

	// 3) 再次 resume → 409（已 active）
	resp = h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/resume", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-resume: status=%d want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestListElicitationsEndpoint(t *testing.T) {
	h := newResumeHarness(t, false)
	defer h.close()
	ctx := context.Background()
	sess := seedPausedChatWithThread(t, h)

	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("q")); err != nil {
		t.Fatal(err)
	}

	resp := h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/elicitations", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	list, _ := got["elicitations"].([]any)
	if len(list) != 1 {
		t.Fatalf("elicitations=%v", got)
	}
	m := list[0].(map[string]any)
	if m["request_id"] != rid.String() || m["question"] != "q" || m["header"] != "H" {
		t.Errorf("row=%v", m)
	}
	if _, ok := m["options"].([]any); !ok {
		t.Errorf("options missing: %v", m)
	}

	// 跨用户 → 404
	resp = h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/elicitations", h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user: status=%d want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// 答掉后不再返回（只列 pending）
	if _, _, _, err := h.store.ResolveElicitationCAS(ctx, rid, []byte(`{"action":"cancel"}`)); err != nil {
		t.Fatal(err)
	}
	resp = h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/elicitations", h.mintToken(sess.UserID), nil)
	got = decodeJSON[map[string]any](t, resp)
	if n := len(got["elicitations"].([]any)); n != 0 {
		t.Errorf("after answer: %d pending want 0", n)
	}
}

func TestSessionResultEndpoint(t *testing.T) {
	h := newResumeHarness(t, false)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "completed")

	// 无结果行（chat 模式）→ result null 但 state 可读
	resp := h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/result", h.mintToken(sess.UserID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["state"] != "completed" || got["result"] != nil {
		t.Errorf("resp=%v", got)
	}

	// 有结果行 → result 带出
	if err := h.store.InsertSessionResult(ctx, SessionResult{
		SessionID: sess.SessionID, Status: "completed", FinalText: "done",
	}); err != nil {
		t.Fatal(err)
	}
	resp = h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/result", h.mintToken(sess.UserID), nil)
	got = decodeJSON[map[string]any](t, resp)
	res, _ := got["result"].(map[string]any)
	if res["status"] != "completed" || res["final_text"] != "done" {
		t.Errorf("result=%v", got["result"])
	}

	// 跨用户 → 404
	resp = h.req(t, "GET", "/v1/agent/sessions/"+sess.SessionID.String()+"/result", h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user: status=%d want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestBuildResumePrompt 是纯函数单测（不依赖 DB）。
func TestBuildResumePrompt(t *testing.T) {
	// 无答案 → 通用续跑提示
	if got := buildResumePrompt(nil); !strings.Contains(got, "请继续") {
		t.Errorf("empty=%q", got)
	}
	mk := func(q, action string, content map[string]any) Elicitation {
		payload, _ := json.Marshal(map[string]any{"question": q})
		ans, _ := json.Marshal(map[string]any{"action": action, "content": content})
		return Elicitation{Payload: payload, Answer: ans}
	}
	got := buildResumePrompt([]Elicitation{
		mk("q1", "accept", map[string]any{"answer": "A", "notes": "n"}),
		mk("q2", "decline", nil),
		mk("q3", "cancel", nil),
	})
	for _, want := range []string{"Q: q1", "A: A", "Notes: n", "Q: q2", "拒绝", "Q: q3", "取消", "请继续"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}
