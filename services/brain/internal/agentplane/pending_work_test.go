package agentplane

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 在某 user 下插一台 device + 它的一个 environment（指定 state）。返回
// (deviceID, env)。device token 不真造——直接 INSERT。
func seedDeviceEnv(t *testing.T, h *apiHarness, uid uuid.UUID, state string) (uuid.UUID, *Environment) {
	t.Helper()
	deviceID := uuid.New()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, 'mac', $3, 'pfx', now()+interval '1 year')`,
		deviceID, uid, sha256Hex("tok-"+deviceID.String()))
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	env, err := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "mac", DeviceID: &deviceID,
	})
	if err != nil {
		t.Fatalf("seed env: %v", err)
	}
	if state != "online" {
		if _, err := h.pool.Exec(context.Background(),
			`UPDATE agent_environments SET state=$2 WHERE environment_id=$1`, env.EnvironmentID, state); err != nil {
			t.Fatalf("set env state: %v", err)
		}
		env.State = state
	}
	return deviceID, env
}

// 离线 + device → 201 pending + 写 agent_pending_work + 不 enqueue。
func TestCreateAgentSession_OfflineDevice_Queues(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	deviceID, env := seedDeviceEnv(t, h, uid, "offline")

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid), map[string]any{
		"mode": "agent", "environment_id": env.EnvironmentID.String(),
		"prompt": "build it", "model": "m",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	sid := got["session_id"].(string)

	// session 应是 pending。
	var state string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT state FROM agent_sessions WHERE session_id=$1`, sid).Scan(&state)
	if state != "pending" {
		t.Errorf("session state=%q want pending", state)
	}
	// agent_pending_work 应有一行，device_id 正确。
	var pendDevice uuid.UUID
	if err := h.pool.QueryRow(context.Background(),
		`SELECT device_id FROM agent_pending_work WHERE session_id=$1`, sid).Scan(&pendDevice); err != nil {
		t.Fatalf("pending row missing: %v", err)
	}
	if pendDevice != deviceID {
		t.Errorf("pending device=%v want %v", pendDevice, deviceID)
	}
	// 不应 enqueue。
	if len(h.fakeQueue.publishes) != 0 {
		t.Errorf("offline path should not enqueue, got %d", len(h.fakeQueue.publishes))
	}
}

// 离线 + 无 device（PAT 注册）→ 仍 409。
func TestCreateAgentSession_OfflineNoDevice_409(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "pat-mac",
	})
	_, _ = h.pool.Exec(context.Background(),
		`UPDATE agent_environments SET state='offline' WHERE environment_id=$1`, env.EnvironmentID)

	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid), map[string]any{
		"mode": "agent", "environment_id": env.EnvironmentID.String(), "prompt": "x", "model": "m",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status=%d want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

// 离线挂起达上限 → 第 N+1 条 429，且不留越限/孤儿 pending session。
func TestCreateAgentSession_PendingLimit(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	_, env := seedDeviceEnv(t, h, uid, "offline")
	tok := h.mintToken(uid)

	for i := 0; i < maxPendingPerDevice; i++ {
		resp := h.req(t, "POST", "/v1/agent/sessions", tok, map[string]any{
			"mode": "agent", "environment_id": env.EnvironmentID.String(),
			"prompt": "task", "model": "m",
		})
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("queue #%d: status=%d body=%s", i, resp.StatusCode, body)
		}
		resp.Body.Close()
	}
	// 第 N+1 条 → 429。
	resp := h.req(t, "POST", "/v1/agent/sessions", tok, map[string]any{
		"mode": "agent", "environment_id": env.EnvironmentID.String(),
		"prompt": "over", "model": "m",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("over-limit status=%d want 429 body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// pending_work 行数恰为上限（没越限）。
	var cnt int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_pending_work w
		   JOIN agent_environments e ON e.device_id = w.device_id
		  WHERE e.environment_id = $1`, env.EnvironmentID).Scan(&cnt)
	if cnt != maxPendingPerDevice {
		t.Errorf("pending_work count=%d want %d", cnt, maxPendingPerDevice)
	}
	// 关键回归：被拒的那条不能留成无 pending_work 的 pending 孤儿（janitor 扫不到）。
	var orphans int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_sessions s
		  WHERE s.state = 'pending'
		    AND NOT EXISTS (SELECT 1 FROM agent_pending_work w WHERE w.session_id = s.session_id)`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("orphan pending sessions (no pending_work)=%d want 0", orphans)
	}
}

// 设备重连(注册新 env) → 离线挂起任务重派：enqueue 到新 env + session active +
// environment_id 更新 + pending 行删除。
func TestRedispatch_OnDeviceReconnect(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	deviceID, offlineEnv := seedDeviceEnv(t, h, uid, "offline")

	// 1) 离线建挂起任务。
	resp := h.req(t, "POST", "/v1/agent/sessions", h.mintToken(uid), map[string]any{
		"mode": "agent", "environment_id": offlineEnv.EnvironmentID.String(),
		"prompt": "deferred task", "model": "m",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pending: %d", resp.StatusCode)
	}
	sid := decodeJSON[map[string]any](t, resp)["session_id"].(string)
	h.fakeQueue.publishes = nil // 清掉(离线路径本就没投，保险)

	// 2) 设备用 device token 注册（新 env，带同 device_id）→ 触发重派。把
	// seeded device 的 token_hash 设成已知值，注册时 requireAuth 据此把 device_id
	// 带进 claims、handleRegister stamp 到新 env、dispatchPendingForDevice 重派。
	token := "biu_dev_reconnect_secret"
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE agent_devices SET token_hash=$2 WHERE device_id=$1`, deviceID, sha256Hex(token)); err != nil {
		t.Fatalf("set device token: %v", err)
	}

	regResp := h.req(t, "POST", "/v1/agent/environments", token, map[string]any{
		"worker_kind": "biu_daemon", "machine_name": "mac-reconnect",
	})
	if regResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(regResp.Body)
		t.Fatalf("register: %d %s", regResp.StatusCode, body)
	}
	newEnvID := decodeJSON[map[string]any](t, regResp)["environment_id"].(string)

	// 3) 断言：work enqueue 到新 env subject。
	if len(h.fakeQueue.publishes) != 1 {
		t.Fatalf("redispatch should enqueue 1, got %d", len(h.fakeQueue.publishes))
	}
	wantSubject := "biu.work." + newEnvID
	if h.fakeQueue.publishes[0].Subject != wantSubject {
		t.Errorf("subject=%q want %q", h.fakeQueue.publishes[0].Subject, wantSubject)
	}
	wp := h.fakeQueue.publishes[0].Payload.(WorkPayload)
	if wp.Prompt != "deferred task" {
		t.Errorf("redispatched prompt=%q", wp.Prompt)
	}
	// session active + environment_id 更新 + pending 行删。
	var state, envID string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT state, environment_id::text FROM agent_sessions WHERE session_id=$1`, sid).Scan(&state, &envID)
	if state != "active" || envID != newEnvID {
		t.Errorf("session state=%q env=%q want active/%s", state, envID, newEnvID)
	}
	var cnt int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_pending_work WHERE session_id=$1`, sid).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("pending row should be deleted after redispatch, got %d", cnt)
	}
}

// 过期挂起任务 → janitor 标 session failed + 删 pending。
func TestJanitor_ExpiresPendingWork(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	deviceID, offlineEnv := seedDeviceEnv(t, h, uid, "offline")

	sess, err := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, EnvironmentID: &offlineEnv.EnvironmentID, Mode: "agent", State: "pending",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	// 插一条已过期的 pending work。
	if err := h.store.InsertPendingWork(context.Background(), PendingWork{
		SessionID: sess.SessionID, UserID: uid, DeviceID: deviceID, Prompt: "old",
	}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("insert pending: %v", err)
	}

	j := NewJanitor(h.pool, nil, nil)
	j.RunOnce(context.Background())

	var state string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT state FROM agent_sessions WHERE session_id=$1`, sess.SessionID).Scan(&state)
	if state != "failed" {
		t.Errorf("expired pending session state=%q want failed", state)
	}
	var cnt int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_pending_work WHERE session_id=$1`, sess.SessionID).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("expired pending row should be deleted, got %d", cnt)
	}
}
