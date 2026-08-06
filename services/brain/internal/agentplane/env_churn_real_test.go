package agentplane

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestQueueReal_ChurnedEnvIDOrphansWork 反证 env_id churn 的损失：work 路由绑
// biu.work.<envID>，worker 用 durable worker-<envID> + FilterSubject 拉取。若
// 重启换了 env_id（churn），新 env_id 的 worker 拉的是另一个 subject —— 旧
// subject 上的在飞 work 成孤儿，JetStream redeliver（投同名 durable）也够不到。
//
// 这正是 R9 把 env_id 按 device_id 稳定化（RegisterEnvironment upsert 复用
// env_id）要消除的窗口。配合 TestRegisterEnvironment_DeviceUpsert（证明重连
// 复用 env_id）+ TestQueueReal_DeadWorkerAutoRedeliver（证明同 durable redeliver
// 机制），三者合起来说明：稳定 env_id → 同 durable → 旧在飞 work 回到重连 worker。
func TestQueueReal_ChurnedEnvIDOrphansWork(t *testing.T) {
	_, _, js := natsBrokerOrSkip(t)
	q := ensureWorkStreamReal(t, js)

	envA := uuid.New() // 重启前
	envB := uuid.New() // churn 后的新 env_id
	t.Cleanup(func() {
		purgeWorkSubject(t, js, envA)
		purgeWorkSubject(t, js, envB)
	})

	workID := "churn-" + uniqStreamSuffix(t)
	if err := q.EnqueueWork(context.Background(), envA, workID,
		map[string]any{"prompt": "in-flight"}); err != nil {
		t.Fatalf("enqueue to envA: %v", err)
	}

	// churn：新 env_id 的 worker 拉不到旧 subject 的消息 → 在飞 work 丢失。
	got, err := q.FetchWork(context.Background(), envB, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("fetch envB: %v", err)
	}
	if got != nil {
		t.Fatalf("churned env_id must NOT see envA's work (this is the loss R9 fixes), got %q", got.Body)
	}

	// 稳定 env_id（回到 envA，正是 upsert 复用 env_id 后的行为）→ 拿得到。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err = q.FetchWork(ctx, envA, 2*time.Second)
	if err != nil {
		t.Fatalf("fetch envA: %v", err)
	}
	if got == nil {
		t.Fatal("stable env_id should fetch its own in-flight work")
	}
	_ = got.Ack()
}
