package agentplane

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

// R9：device token 注册按 device_id UPSERT —— 同一设备重连复用既有 env_id，
// 不 churn 新行。这是关掉「在飞 work 丢失 + consumer/行泄漏 + re-register 风暴」
// 的承重事实：env_id 稳定 → JetStream durable worker-<envID> 稳定 → 旧的在飞
// work 被 AckWait redeliver 给重连 worker（见 TestQueueReal_DeadWorkerAutoRedeliver
// 证明机制 + TestQueueReal_ChurnedEnvIDOrphansWork 反证 churn 的损失）。
func TestRegisterEnvironment_DeviceUpsert(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	uid := uuid.New()
	devID := uuid.New()

	first, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "mac-1",
		Capabilities: []string{"sandbox"}, PublicKey: bytes.Repeat([]byte{1}, 32),
		DeviceID: &devID,
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	// 模拟断线：标 offline。
	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_environments SET state='offline' WHERE environment_id=$1`, first.EnvironmentID); err != nil {
		t.Fatalf("mark offline: %v", err)
	}

	// 同 device 重连，上报新的 machine_name / capabilities / pubkey。
	second, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "mac-2",
		Capabilities: []string{"sandbox", "vision"}, PublicKey: bytes.Repeat([]byte{2}, 32),
		DeviceID: &devID,
	})
	if err != nil {
		t.Fatalf("second register: %v", err)
	}

	// env_id 必须复用（不 churn）。
	if second.EnvironmentID != first.EnvironmentID {
		t.Fatalf("env_id churned: first=%v second=%v (want stable reuse)",
			first.EnvironmentID, second.EnvironmentID)
	}
	// 该 device 仅一行 environment。
	var cnt int
	_ = h.pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_environments WHERE device_id=$1`, devID).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("device environment rows=%d want 1", cnt)
	}
	// state 拉回 online；元数据刷新（pubkey 必须刷新，否则 BYOK 用旧 key 封装）。
	if second.State != "online" {
		t.Errorf("state=%q want online", second.State)
	}
	if second.MachineName != "mac-2" {
		t.Errorf("machine_name=%q want mac-2 (refreshed)", second.MachineName)
	}
	if !bytes.Equal(second.PublicKey, bytes.Repeat([]byte{2}, 32)) {
		t.Errorf("public_key not refreshed on upsert")
	}
	// created_at 保留首次注册时间（DO UPDATE 不写 created_at）。
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at changed on upsert: %v → %v", first.CreatedAt, second.CreatedAt)
	}
}

// 无 device（runtime 池 / PAT / JWT，device_id IS NULL）保持 INSERT 语义 ——
// 每次注册独立 env_id。runtime 每副本须是独立 environment 供 PickRuntimeEnvironment
// 负载均衡，绝不能被 upsert 合并。partial unique 约束（WHERE device_id IS NOT NULL）
// 不约束 nil device，故此处不冲突。
func TestRegisterEnvironment_NoDeviceInserts(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	uid := uuid.New()

	mk := func() *Environment {
		env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
			UserID: &uid, WorkerKind: "runtime", MachineName: "pod", PoolTag: "default",
		})
		if err != nil {
			t.Fatalf("register no-device: %v", err)
		}
		return env
	}
	a, b := mk(), mk()
	if a.EnvironmentID == b.EnvironmentID {
		t.Fatalf("no-device registers must be distinct env_ids (runtime pool semantics), got %v twice",
			a.EnvironmentID)
	}
}
