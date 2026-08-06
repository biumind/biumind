package agentplane

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// ── CreateSession: chat mode ───────────────────────────────

func TestRouter_CreateChatSession(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	resp := h.req(t, "POST", "/v1/agent/sessions", tok, map[string]any{
		"mode":   "chat",
		"prompt": "hello",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["mode"] != "chat" {
		t.Errorf("mode=%v want chat", got["mode"])
	}
	if got["session_id"] == "" || got["session_token"] == "" {
		t.Errorf("missing session_id / session_token: %v", got)
	}
	// chat mode: 不 enqueue work
	if len(h.fakeQueue.publishes) != 0 {
		t.Errorf("chat mode should not publish work, got %d", len(h.fakeQueue.publishes))
	}

	// session_token 用 VerifySessionToken 能解
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	sid, _ := uuid.Parse(got["session_id"].(string))
	if _, err := VerifySessionToken(verifier, got["session_token"].(string), sid); err != nil {
		t.Errorf("returned session_token does not verify: %v", err)
	}
}

// ── CreateSession: agent mode ──────────────────────────────

func TestRouter_CreateAgentSession(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	// 注册一个 biu_daemon environment
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "laptop",
	})

	resp := h.req(t, "POST", "/v1/agent/sessions", tok, map[string]any{
		"mode":           "agent",
		"environment_id": env.EnvironmentID.String(),
		"prompt":         "list files",
		"model":          "claude-3-7",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["mode"] != "agent" {
		t.Errorf("mode=%v want agent", got["mode"])
	}
	if got["environment_id"] != env.EnvironmentID.String() {
		t.Errorf("environment_id=%v", got["environment_id"])
	}
	// 应该 enqueue 一条 work 到正确的 subject
	if len(h.fakeQueue.publishes) != 1 {
		t.Fatalf("agent mode should publish 1 work, got %d", len(h.fakeQueue.publishes))
	}
	pub := h.fakeQueue.publishes[0]
	wantSubject := "biu.work." + env.EnvironmentID.String()
	if pub.Subject != wantSubject {
		t.Errorf("subject=%q want %q", pub.Subject, wantSubject)
	}
}

func TestRouter_CreateAgentSession_MissingEnvID(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uuid.New()),
		map[string]any{"mode": "agent", "prompt": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

func TestRouter_CreateAgentSession_CrossUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// user A 注册 environment
	uidA := uuid.New()
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uidA, WorkerKind: "biu_daemon", MachineName: "a",
	})

	// user B 试用 A 的 environment_id —— 404
	uidB := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uidB),
		map[string]any{
			"mode":           "agent",
			"environment_id": env.EnvironmentID.String(),
			"prompt":         "x",
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user agent = %d, want 404", resp.StatusCode)
	}
}

func TestRouter_CreateAgentSession_OfflineEnv(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "dead",
	})
	// 改成 offline
	_, _ = h.pool.Exec(context.Background(),
		`UPDATE agent_environments SET state='offline' WHERE environment_id=$1`,
		env.EnvironmentID)

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid),
		map[string]any{
			"mode":           "agent",
			"environment_id": env.EnvironmentID.String(),
			"prompt":         "x",
		})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("offline env = %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "offline") {
		t.Errorf("error body should mention offline: %s", body)
	}
}

func TestRouter_CreateAgentSession_RuntimeKind(t *testing.T) {
	// agent mode 应当拒绝 runtime kind 的 environment（runtime 走 task）
	h := newAPIHarness(t)
	defer h.close()

	// runtime 的 user_id 字段是 NULL —— 用直接 SQL 注册（API 也允许，但
	// 这里走 store 更直观）
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: nil, WorkerKind: "runtime", MachineName: "rt-1",
	})

	uid := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid),
		map[string]any{
			"mode":           "agent",
			"environment_id": env.EnvironmentID.String(),
			"prompt":         "x",
		})
	// 注意：因为 runtime user_id=NULL，agent 端 GetEnvironment 用 user_id
	// 严格匹配会 404 —— 这是预期行为（agent 模式 user 看不到系统级 runtime）。
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("agent-with-runtime = %d, want 404 (cross visibility)", resp.StatusCode)
	}
}

// ── CreateSession: task mode ───────────────────────────────

func TestRouter_CreateTaskSession(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// seed 一个 online runtime
	rtEnv, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		WorkerKind:  "runtime",
		MachineName: "runtime-pod-x",
		PoolTag:     "runtime-prod",
	})

	uid := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid),
		map[string]any{"mode": "task", "prompt": "do task", "pool_tag": "runtime-prod"})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["environment_id"] != rtEnv.EnvironmentID.String() {
		t.Errorf("env_id=%v want %v", got["environment_id"], rtEnv.EnvironmentID)
	}
	if len(h.fakeQueue.publishes) != 1 {
		t.Fatalf("task mode should publish 1 work, got %d", len(h.fakeQueue.publishes))
	}
}

func TestRouter_CreateTaskSession_NoRuntime(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uuid.New()),
		map[string]any{"mode": "task", "prompt": "x"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no runtime = %d, want 503", resp.StatusCode)
	}
}

func TestRouter_CreateTaskSession_PoolFiltered(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// seed 一个 runtime in pool A，请求 pool B —— 应当 503
	_, _ = h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		WorkerKind: "runtime", MachineName: "rt", PoolTag: "pool-a",
	})

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uuid.New()),
		map[string]any{"mode": "task", "pool_tag": "pool-b"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("filtered pool empty = %d, want 503", resp.StatusCode)
	}
}

// ── CreateSession: 错误路径 ───────────────────────────────

func TestRouter_CreateSession_BadMode(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uuid.New()),
		map[string]any{"mode": "spaghetti"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

// nil-signer 路径已经在 tokens_test.go::TestAPI_RefreshSessionToken_NoSigner 覆盖。
// CreateSession 的 nil-signer 分支跟 refresh-token 同套早出代码（`if s.Signer == nil`），
// 不重复写测试。

// ── Finalize hook ─────────────────────────────────────────

func TestFinalize_TaskWritesResult(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	rtEnv, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		WorkerKind: "runtime", MachineName: "rt",
	})
	envIDCopy := rtEnv.EnvironmentID
	sess, _ := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID:        uid,
		EnvironmentID: &envIDCopy,
		Mode:          "task",
	})

	err := FinalizeSessionResult(context.Background(), h.store, sess, FinalizeOpts{
		Status:           "completed",
		FinalText:        "task done",
		CostUSD:          0.123,
		PromptTokens:     100,
		CompletionTokens: 200,
		DurationMs:       1500,
	})
	if err != nil {
		t.Fatal(err)
	}

	// agent_session_results 应该有一行
	var status, finalText string
	var cost float64
	err = h.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(final_text, ''), COALESCE(cost_usd, 0)
		   FROM agent_session_results WHERE session_id = $1`,
		sess.SessionID).Scan(&status, &finalText, &cost)
	if err != nil {
		t.Fatal(err)
	}
	if status != "completed" || finalText != "task done" || cost < 0.122 {
		t.Errorf("got status=%q final=%q cost=%v", status, finalText, cost)
	}

	// agent_sessions.state 应被更新
	got, _ := h.store.GetSession(context.Background(), uid, sess.SessionID)
	if got.State != "completed" {
		t.Errorf("session state=%q want completed", got.State)
	}
}

func TestFinalize_ChatSkipsResultsTable(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	sess, _ := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "chat",
	})

	err := FinalizeSessionResult(context.Background(), h.store, sess, FinalizeOpts{
		Status: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}

	// chat 模式不写 agent_session_results
	var n int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_session_results WHERE session_id = $1`,
		sess.SessionID).Scan(&n)
	if n != 0 {
		t.Errorf("chat mode should not write session_results, got %d rows", n)
	}

	// 但 session state 仍要更新
	got, _ := h.store.GetSession(context.Background(), uid, sess.SessionID)
	if got.State != "completed" {
		t.Errorf("session state=%q want completed", got.State)
	}
}

