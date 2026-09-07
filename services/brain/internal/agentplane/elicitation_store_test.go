// agent_elicitations store 集成测试（P3-c durable resume）。
// 需要 DATABASE_URL 指向真实 PG（同 api_test.go harness 约定）。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

var elicTestLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// seedChatSession 建一条 chat 模式 session（无 environment），返回 session。
func seedChatSession(t *testing.T, h *apiHarness, state string) *Session {
	t.Helper()
	uid := uuid.New()
	tid := uuid.New()
	sess, err := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, ThreadID: &tid, Mode: "chat", State: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func elicTestPayload(question string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"question": question, "header": "H", "multi_select": false,
		"options": []map[string]any{{"label": "A", "description": "a"}, {"label": "B", "description": "b"}},
	})
	return raw
}

func TestInsertElicitation_CancelsStalePending(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")

	r1, r2 := uuid.New(), uuid.New()
	if err := h.store.InsertElicitation(ctx, r1, sess.SessionID, elicTestPayload("q1")); err != nil {
		t.Fatal(err)
	}
	if err := h.store.InsertElicitation(ctx, r2, sess.SessionID, elicTestPayload("q2")); err != nil {
		t.Fatal(err)
	}

	// 新提问落库把旧 pending 作废（daemon 重跑重问防双表单）
	e1, err := h.store.GetElicitation(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Status != "cancelled" {
		t.Errorf("stale row status=%q want cancelled", e1.Status)
	}
	e2, _ := h.store.GetElicitation(ctx, r2)
	if e2.Status != "pending" {
		t.Errorf("new row status=%q want pending", e2.Status)
	}
	// INSERT...SELECT 带入了 session 元数据
	if e2.ThreadID == nil || *e2.ThreadID != *sess.ThreadID || e2.UserID != sess.UserID {
		t.Errorf("metadata not carried: %+v", e2)
	}
	if d := time.Until(e2.ExpiresAt); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Errorf("expires_at off: %v from now", d)
	}

	// 同 request_id 重插（帧 replay）→ 幂等不报错不顶行
	if err := h.store.InsertElicitation(ctx, r2, sess.SessionID, elicTestPayload("q2-dup")); err != nil {
		t.Fatalf("replay insert should be idempotent: %v", err)
	}
	e2b, _ := h.store.GetElicitation(ctx, r2)
	if !json.Valid(e2b.Payload) || string(e2b.Payload) == string(elicTestPayload("q2-dup")) {
		t.Errorf("replay should not overwrite payload")
	}

	// session 不存在 → ErrNotFound
	if err := h.store.InsertElicitation(ctx, uuid.New(), uuid.New(), elicTestPayload("q")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session err=%v want ErrNotFound", err)
	}
}

func TestResolveElicitationCAS(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")
	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("q")); err != nil {
		t.Fatal(err)
	}

	ans := []byte(`{"action":"accept","content":{"answer":"A"}}`)
	// 第一次认领：命中
	sessID, payload, ok, err := h.store.ResolveElicitationCAS(ctx, rid, ans)
	if err != nil || !ok {
		t.Fatalf("first CAS ok=%v err=%v", ok, err)
	}
	if sessID != sess.SessionID {
		t.Errorf("session_id=%v want %v", sessID, sess.SessionID)
	}
	var meta map[string]any
	if err := json.Unmarshal(payload, &meta); err != nil || meta["question"] != "q" {
		t.Errorf("payload snapshot wrong: %s", payload)
	}
	// 第二次认领（replay / 并发 resume）→ miss 无错误
	if _, _, ok, err := h.store.ResolveElicitationCAS(ctx, rid, ans); err != nil || ok {
		t.Errorf("second CAS ok=%v err=%v, want miss", ok, err)
	}
	// 不存在的 request_id → miss
	if _, _, ok, err := h.store.ResolveElicitationCAS(ctx, uuid.New(), ans); err != nil || ok {
		t.Errorf("unknown CAS ok=%v err=%v, want miss", ok, err)
	}
	// 状态与答案落库核验
	e, _ := h.store.GetElicitation(ctx, rid)
	if e.Status != "answered" || e.AnsweredAt == nil {
		t.Errorf("status=%q answered_at=%v", e.Status, e.AnsweredAt)
	}
	var stored map[string]any
	if err := json.Unmarshal(e.Answer, &stored); err != nil || stored["action"] != "accept" {
		t.Errorf("answer=%s", e.Answer)
	}
}

func TestListPendingAndConsumedAnswers(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "paused")

	r1, r2 := uuid.New(), uuid.New()
	_ = h.store.InsertElicitation(ctx, r1, sess.SessionID, elicTestPayload("q1"))
	// r2 落库把 r1 作废；再手工把 r1 复活成 pending 模拟「两题都 pending」
	_ = h.store.InsertElicitation(ctx, r2, sess.SessionID, elicTestPayload("q2"))
	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_elicitations SET status='pending' WHERE request_id=$1`, r1); err != nil {
		t.Fatal(err)
	}
	pending, err := h.store.ListPendingElicitations(ctx, sess.SessionID)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending=%d err=%v want 2", len(pending), err)
	}
	if pending[0].RequestID != r1 { // 创建时间升序
		t.Errorf("order: first=%v want %v", pending[0].RequestID, r1)
	}

	// 答掉两题 → ListUnconsumedAnswers 两条；MarkAnswersConsumed 后清空
	ans := []byte(`{"action":"accept","content":{"answer":"A"}}`)
	for _, rid := range []uuid.UUID{r1, r2} {
		if _, _, ok, err := h.store.ResolveElicitationCAS(ctx, rid, ans); err != nil || !ok {
			t.Fatalf("CAS %v: ok=%v err=%v", rid, ok, err)
		}
	}
	answers, err := h.store.ListUnconsumedAnswers(ctx, sess.SessionID)
	if err != nil || len(answers) != 2 {
		t.Fatalf("unconsumed=%d err=%v want 2", len(answers), err)
	}
	if err := h.store.MarkAnswersConsumed(ctx, sess.SessionID); err != nil {
		t.Fatal(err)
	}
	answers, _ = h.store.ListUnconsumedAnswers(ctx, sess.SessionID)
	if len(answers) != 0 {
		t.Errorf("after consume: %d want 0", len(answers))
	}
	e, _ := h.store.GetElicitation(ctx, r1)
	if e.ConsumedAt == nil || e.Status != "answered" {
		t.Errorf("consumed_at=%v status=%q", e.ConsumedAt, e.Status)
	}
}

func TestPauseZombieChatSessions(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()

	// chat active（僵尸候选）+ chat paused（不动）+ daemon active（不动）
	chatActive := seedChatSession(t, h, "active")
	chatPaused := seedChatSession(t, h, "paused")
	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := h.store.InsertSession(ctx, CreateSessionReq{
		UserID: uid, EnvironmentID: &env.EnvironmentID, Mode: "agent", State: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := h.store.PauseZombieChatSessions(ctx)
	if err != nil || n != 1 {
		t.Fatalf("paused=%d err=%v want 1", n, err)
	}
	got, _ := h.store.GetSession(ctx, chatActive.UserID, chatActive.SessionID)
	if got.State != "paused" {
		t.Errorf("chat active state=%q want paused", got.State)
	}
	got, _ = h.store.GetSession(ctx, chatPaused.UserID, chatPaused.SessionID)
	if got.State != "paused" {
		t.Errorf("chat paused state=%q", got.State)
	}
	got, _ = h.store.GetSession(ctx, uid, daemon.SessionID)
	if got.State != "active" {
		t.Errorf("daemon session state=%q want active (untouched)", got.State)
	}
	// 幂等：再跑一次 0 行
	if n, _ := h.store.PauseZombieChatSessions(ctx); n != 0 {
		t.Errorf("second sweep n=%d want 0", n)
	}
}

func TestResumeSessionCAS(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "paused")

	ok, err := h.store.ResumeSessionCAS(ctx, sess.SessionID)
	if err != nil || !ok {
		t.Fatalf("first resume ok=%v err=%v", ok, err)
	}
	// 并发第二次 → 失败（已不是 paused）
	if ok, err := h.store.ResumeSessionCAS(ctx, sess.SessionID); err != nil || ok {
		t.Errorf("second resume ok=%v err=%v want false", ok, err)
	}
	got, _ := h.store.GetSession(ctx, sess.UserID, sess.SessionID)
	if got.State != "active" {
		t.Errorf("state=%q want active", got.State)
	}
}

func TestJanitor_ExpiresElicitations(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "paused")

	fresh, stale := uuid.New(), uuid.New()
	_ = h.store.InsertElicitation(ctx, fresh, sess.SessionID, elicTestPayload("fresh"))
	// 绕过 InsertElicitation 的作废旧行逻辑，直接插一条已过期的 pending
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO agent_elicitations (request_id, session_id, user_id, payload, expires_at)
		VALUES ($1, $2, $3, '{}'::jsonb, now() - INTERVAL '1 hour')
	`, stale, sess.SessionID, sess.UserID); err != nil {
		t.Fatal(err)
	}

	j := NewJanitor(h.pool, elicTestLogger, nil)
	j.RunOnce(ctx)

	e, _ := h.store.GetElicitation(ctx, stale)
	if e.Status != "expired" {
		t.Errorf("stale status=%q want expired", e.Status)
	}
	e, _ = h.store.GetElicitation(ctx, fresh)
	if e.Status != "pending" {
		t.Errorf("fresh status=%q want pending", e.Status)
	}
	if j.Stats().ElicitationsExpired != 1 {
		t.Errorf("stats expired=%d want 1", j.Stats().ElicitationsExpired)
	}
}

func TestGetSessionResult(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")

	// 无结果行 → ErrNotFound
	if _, err := h.store.GetSessionResult(ctx, sess.SessionID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
	if err := h.store.InsertSessionResult(ctx, SessionResult{
		SessionID: sess.SessionID, Status: "completed", FinalText: "done", DurationMs: 42,
	}); err != nil {
		t.Fatal(err)
	}
	r, err := h.store.GetSessionResult(ctx, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" || r.FinalText != "done" || r.DurationMs != 42 {
		t.Errorf("result=%+v", r)
	}
}
