package agentplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestJanitor_MarksStaleEnvironmentOffline：seed 一个 last_seen_at 很久以前
// 的 online environment，RunOnce 一次后状态变 offline。
func TestJanitor_MarksStaleEnvironmentOffline(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	ctx := context.Background()

	// 注册一个 environment（默认 last_seen_at = now，state=online）
	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID:      &uid,
		WorkerKind:  "biu_daemon",
		MachineName: "stale",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 直接改 last_seen_at 倒退 5min（超过 90s TTL）
	_, err = h.pool.Exec(ctx,
		`UPDATE agent_environments SET last_seen_at = now() - INTERVAL '5 minutes' WHERE environment_id = $1`,
		env.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}

	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	n := j.RunOnce(ctx)
	if n != 1 {
		t.Errorf("marked %d, want 1", n)
	}

	// 验证 state 真变了
	got, err := h.store.GetEnvironment(ctx, uid, env.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "offline" {
		t.Errorf("state=%q want offline", got.State)
	}
}

// TestJanitor_LeavesFreshEnvironmentAlone：刚 heartbeat 过的 online
// environment 不应被标 offline。
func TestJanitor_LeavesFreshEnvironmentAlone(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	ctx := context.Background()
	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID:      &uid,
		WorkerKind:  "biu_daemon",
		MachineName: "fresh",
	})
	if err != nil {
		t.Fatal(err)
	}

	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	n := j.RunOnce(ctx)
	if n != 0 {
		t.Errorf("marked %d, want 0", n)
	}
	got, _ := h.store.GetEnvironment(ctx, uid, env.EnvironmentID)
	if got.State != "online" {
		t.Errorf("state=%q want online", got.State)
	}
}

// TestJanitor_SkipsAlreadyOffline：已经 offline 的 environment 不应被
// 重复 update（验证 WHERE state='online' 过滤）。
func TestJanitor_SkipsAlreadyOffline(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	ctx := context.Background()
	uid := uuid.New()
	env, _ := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID:      &uid,
		WorkerKind:  "biu_daemon",
		MachineName: "offline-already",
	})
	// 直接置成 offline + 老 last_seen_at
	_, _ = h.pool.Exec(ctx,
		`UPDATE agent_environments SET state='offline', last_seen_at=now() - INTERVAL '5 minutes' WHERE environment_id=$1`,
		env.EnvironmentID)

	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	n := j.RunOnce(ctx)
	if n != 0 {
		t.Errorf("marked %d, want 0 (already offline)", n)
	}
}

// TestJanitor_OrphanSessionPublishesFailFrame：env 离线 → 名下 active agent
// session 不仅被标 failed,还要向 biu.session.<id>.out 推一帧 SDKResultError ——
// 否则正在 /stream 的客户端 spinner 永远转（R8 修复的核心断言）。
func TestJanitor_OrphanSessionPublishesFailFrame(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()

	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "dead-daemon",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 心跳过期 → 本轮 sweep 会把它标 offline。
	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_environments SET last_seen_at = now() - INTERVAL '5 minutes' WHERE environment_id=$1`,
		env.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	// 一条绑在该 env 上的 active agent session（in-flight,客户端正在等）。
	sess, err := h.store.InsertSession(ctx, CreateSessionReq{
		UserID: uid, Mode: "agent", EnvironmentID: &env.EnvironmentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	fakeJS := &fakeJSForAPI{}
	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), NewQueue(fakeJS))
	j.RunOnce(ctx)

	// 1. session 落 failed
	var state string
	if err := h.pool.QueryRow(ctx,
		`SELECT state FROM agent_sessions WHERE session_id=$1`, sess.SessionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("session state = %q, want failed", state)
	}

	// 2. 向该 session 的 .out 推了一帧,且帧体是 recoverable=false 的错误
	wantSubject := SessionSubjectOut(sess.SessionID.String())
	var found bool
	for _, p := range fakeJS.publishes {
		if p.Subject != wantSubject {
			continue
		}
		raw, _ := json.Marshal(p.Payload)
		if bytes.Contains(raw, []byte("执行设备已离线")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no fail frame published to %s; publishes=%+v", wantSubject, fakeJS.publishes)
	}
}

// TestJanitor_OrphanWithoutQueueNoPanic：queue 为空（无 NATS 的 dev）时,
// 标 failed 仍工作,只是不推帧 —— 不能 panic。
func TestJanitor_OrphanWithoutQueueNoPanic(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()

	uid := uuid.New()
	env, _ := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "dead-daemon-noq",
	})
	_, _ = h.pool.Exec(ctx,
		`UPDATE agent_environments SET last_seen_at = now() - INTERVAL '5 minutes' WHERE environment_id=$1`,
		env.EnvironmentID)
	sess, _ := h.store.InsertSession(ctx, CreateSessionReq{
		UserID: uid, Mode: "agent", EnvironmentID: &env.EnvironmentID,
	})

	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), nil) // queue=nil
	j.RunOnce(ctx)

	var state string
	_ = h.pool.QueryRow(ctx,
		`SELECT state FROM agent_sessions WHERE session_id=$1`, sess.SessionID).Scan(&state)
	if state != "failed" {
		t.Fatalf("session state = %q, want failed (no-queue path)", state)
	}
}

// TestJanitor_RunStopsOnContextCancel：Run 在 ctx cancel 后应该立即退出。
// 用 deadline 防止单测卡住。
func TestJanitor_RunStopsOnContextCancel(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// 缩短 sweep interval 让 Run 不会因为 ticker 卡住测试
	old := JanitorSweepInterval
	JanitorSweepInterval = 50 * time.Millisecond
	defer func() { JanitorSweepInterval = old }()

	j := NewJanitor(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		j.Run(ctx)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond) // 让它 sweep 几次
	cancel()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	if j.Stats().Sweeps == 0 {
		t.Error("expected at least one sweep")
	}
}
