package agentplane

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRouter_AgentSession_StampsDeviceToolPolicy（R6.3 / D7）：environment 关联
// device → createAgentSession 把该 device 的 tool_policy stamp 进 WorkPayload。
func TestRouter_AgentSession_StampsDeviceToolPolicy(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	// 直接插一台 device（绕过配对流程）+ 设 readonly policy。
	deviceID := uuid.New()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, tool_policy, expires_at)
		VALUES ($1, $2, 'laptop', $3, 'biu_dev_x', 'readonly', now() + interval '1 year')`,
		deviceID, uid, []byte("hash-"+deviceID.String()))
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	// environment 关联该 device。
	env, err := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "laptop", DeviceID: &deviceID,
	})
	if err != nil {
		t.Fatalf("insert env: %v", err)
	}
	if env.DeviceID == nil || *env.DeviceID != deviceID {
		t.Fatalf("environment.device_id not persisted: %v", env.DeviceID)
	}

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
	if len(h.fakeQueue.publishes) != 1 {
		t.Fatalf("want 1 publish, got %d", len(h.fakeQueue.publishes))
	}
	wp, ok := h.fakeQueue.publishes[0].Payload.(WorkPayload)
	if !ok {
		t.Fatalf("payload type %T, want WorkPayload", h.fakeQueue.publishes[0].Payload)
	}
	if wp.ToolPolicy != "readonly" {
		t.Errorf("WorkPayload.ToolPolicy=%q want readonly", wp.ToolPolicy)
	}
}

// TestRouter_AgentSession_NoDevice_NoStamp：environment 无 device（JWT 注册）→
// 不 stamp，daemon 用本地 flag 地板。
func TestRouter_AgentSession_NoDevice_NoStamp(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)
	env, _ := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "laptop",
	})

	resp := h.req(t, "POST", "/v1/agent/sessions", tok, map[string]any{
		"mode": "agent", "environment_id": env.EnvironmentID.String(),
		"prompt": "x", "model": "m",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	wp := h.fakeQueue.publishes[0].Payload.(WorkPayload)
	if wp.ToolPolicy != "" {
		t.Errorf("no-device env should not stamp ToolPolicy, got %q", wp.ToolPolicy)
	}
}

// TestRegister_DeviceToken_StampsDeviceID（R6.3）：用 device token 注册 →
// requireAuth 把 device_id 带进 claims → handleRegister 把它落到 environment。
// 这是 per-device policy 的链路根基（之前 api.go:273 把 device_id 丢了）。
func TestRegister_DeviceToken_StampsDeviceID(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	deviceID := uuid.New()
	token := "biu_dev_testpfx_secretpart"
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, 'laptop', $3, 'testpfx', now() + interval '1 year')`,
		deviceID, uid, sha256Hex(token))
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	resp := h.req(t, "POST", "/v1/agent/environments", token, map[string]any{
		"worker_kind": "biu_daemon", "machine_name": "laptop",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	envID := decodeJSON[map[string]any](t, resp)["environment_id"].(string)

	var gotDevice *uuid.UUID
	if err := h.pool.QueryRow(context.Background(),
		`SELECT device_id FROM agent_environments WHERE environment_id=$1`, envID).Scan(&gotDevice); err != nil {
		t.Fatalf("query device_id: %v", err)
	}
	if gotDevice == nil || *gotDevice != deviceID {
		t.Fatalf("environment.device_id=%v want %v", gotDevice, deviceID)
	}
}

// TestListDevices_OnlineAggregation（R6.4 / R9）：ListDevices 按 device 的
// environment state 聚合出 Online/LastSeenAt——无 environment→离线；online→在线；
// 重连（device_id upsert）把同一行 offline→online→在线。R9 起一台 device 至多
// 一行 environment（agent_environments_device_uniq），故不再有「多 env 行取最新」
// 的场景；LATERAL ORDER BY last_seen DESC LIMIT 1 对单行仍正确（防御性保留）。
func TestListDevices_OnlineAggregation(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	mkDevice := func(name string) uuid.UUID {
		id := uuid.New()
		_, err := h.pool.Exec(context.Background(), `
			INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
			VALUES ($1, $2, $3, $4, $5, now() + interval '1 year')`,
			id, uid, name, sha256Hex("tok-"+id.String()), name[:3])
		if err != nil {
			t.Fatalf("insert device %s: %v", name, err)
		}
		return id
	}
	mkEnv := func(devID uuid.UUID, state string, seenAgo time.Duration) {
		_, err := h.pool.Exec(context.Background(), `
			INSERT INTO agent_environments
			  (environment_id, user_id, worker_kind, machine_name, state, device_id, last_seen_at)
			VALUES ($1, $2, 'biu_daemon', 'm', $3, $4, now() - $5::interval)`,
			uuid.New(), uid, state, devID, fmt.Sprintf("%d seconds", int(seenAgo.Seconds())))
		if err != nil {
			t.Fatalf("insert env: %v", err)
		}
	}

	noEnv := mkDevice("dev-noenv")
	onlineDev := mkDevice("dev-online")
	mkEnv(onlineDev, "online", 5*time.Second)
	flappy := mkDevice("dev-flappy")
	mkEnv(flappy, "offline", 300*time.Second) // 断线时遗留的 offline 行
	// R9：设备重连按 device_id upsert 复用同一行并把 state 拉回 online（不再新插
	// 一行）。聚合应反映这次重连 = 在线。
	if _, err := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "m", DeviceID: &flappy,
	}); err != nil {
		t.Fatalf("reconnect upsert: %v", err)
	}
	// 不变量：重连后该 device 仍只有一行 environment。
	var flappyRows int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_environments WHERE device_id=$1`, flappy).Scan(&flappyRows)
	if flappyRows != 1 {
		t.Errorf("R9: device must have exactly 1 env row after reconnect, got %d", flappyRows)
	}

	devices, err := h.store.ListDevices(context.Background(), uid)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	got := map[uuid.UUID]Device{}
	for _, d := range devices {
		got[d.DeviceID] = d
	}
	if d := got[noEnv]; d.Online || d.LastSeenAt != nil {
		t.Errorf("no-env device: Online=%v LastSeenAt=%v, want false/nil", d.Online, d.LastSeenAt)
	}
	if d := got[onlineDev]; !d.Online || d.LastSeenAt == nil {
		t.Errorf("online device: Online=%v LastSeenAt=%v, want true/non-nil", d.Online, d.LastSeenAt)
	}
	if d := got[flappy]; !d.Online {
		t.Errorf("flappy device should take latest env (online), got Online=%v", d.Online)
	}
}

// TestStore_SetGetDeviceToolPolicy：PATCH 路径 store 层 round-trip + 跨用户拒绝。
func TestStore_SetGetDeviceToolPolicy(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	other := uuid.New()
	deviceID := uuid.New()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, 'laptop', $3, 'biu_dev_y', now() + interval '1 year')`,
		deviceID, uid, []byte("hash-"+deviceID.String()))
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	// 默认 workspace-write。
	if p, _ := h.store.GetDeviceToolPolicy(context.Background(), deviceID); p != "workspace-write" {
		t.Errorf("default policy=%q want workspace-write", p)
	}
	// 本人改为 full。
	if err := h.store.SetDeviceToolPolicy(context.Background(), uid, deviceID, "full"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if p, _ := h.store.GetDeviceToolPolicy(context.Background(), deviceID); p != "full" {
		t.Errorf("policy after set=%q want full", p)
	}
	// 跨用户改 → ErrNotFound（属主校验）。
	if err := h.store.SetDeviceToolPolicy(context.Background(), other, deviceID, "readonly"); err != ErrNotFound {
		t.Errorf("cross-user set err=%v want ErrNotFound", err)
	}
}
