// S3-3 real-broker integration tests for Queue.
//
// 这些测试需要真 NATS broker（启 JetStream）。本地无 broker 自动 skip
// —— 跟 packages/go-sdk/biu/bus/jetstream_test.go 同款 brokerAvailable
// 模式。CI 通过 NATS_URL env 注 broker 才跑。
//
// 覆盖 S3-3 DoD 的两个关键场景，fakeJS 测不了：
//
//	1. Enqueue → FetchWork 拿到（端到端 publish + pull）
//	2. 死 worker 自动 redeliver（FetchWork 不 ack → AckWait 后 redeliver）
//	3. 幂等去重（同 workID 重复 Enqueue 只投递一次）
//
// 每个测试用 unique stream name suffix，避免并行 / 残留 state 冲突。
// Cleanup 用 RawJetStream().DeleteStream 删流。

package agentplane

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// natsBrokerOrSkip 跟 bus 包的 brokerAvailable 同款 —— 没 NATS_URL 或者
// dial 失败就 skip。返回 (URL, JetStream handle, cleanup)。
func natsBrokerOrSkip(t *testing.T) (string, bus.Bus, bus.JetStream) {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://localhost:4222"
	}
	host := url
	if i := len("nats://"); len(url) > i {
		host = url[i:]
	}
	c, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
	if err != nil {
		t.Skipf("no NATS broker at %s; skipping (set NATS_URL to override)", url)
	}
	c.Close()

	b, err := bus.Connect(url, "agentplane-real-test", "test")
	if err != nil {
		t.Fatalf("bus.Connect: %v", err)
	}
	js, err := b.JetStream()
	if err != nil {
		_ = b.Close()
		t.Skipf("JetStream not available: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return url, b, js
}

// uniqStreamSuffix 用 t.Name + 随机短串避免并行测试碰撞 + 残留流互斥。
func uniqStreamSuffix(t *testing.T) string {
	t.Helper()
	return uuid.New().String()[:8]
}

// withTestWorkStream 起一个测试专用 work stream（subject 前缀也唯一），
// 把原 const 引用换成测试专用名字 —— 这样测试不会污染生产用 BIU_AGENT_WORK
// stream 的 dedupe 历史，并且 DeleteStream 不影响其他东西。
//
// 关键：直接 raw JS API 建流，subject 用 `biu.work.test.<suffix>.>`；
// 然后构造一个 fakeNamesQueue 包装真 JS 但路由到测试 subject 上 —— 方便。
//
// 但 Queue.EnqueueWork 写死了 WorkSubjectPrefix 常量；生产代码改测试用
// 不合适。所以更简单：测试直接用真常量 + 测试 stream name = WorkStreamName
// + suffix（这样 subject filter `biu.work.>` 会包到所有真路径，但每个
// 测试 cleanup 删自己的流）。
//
// 还更糟 —— 多测试同时跑会通过 `biu.work.>` 同一 subject 抢夺消息。所以
// 干脆每测试一个 unique consumer durable 名 + 用 unique env_id（uuid.New()）
// 让 subject 天然隔离：`biu.work.<env-uuid>` 各自不冲。
//
// 结论：复用真 BIU_AGENT_WORK stream（EnsureWorkStream 幂等），靠 env_id
// uuid 做 subject 隔离，cleanup 删 per-env durable consumer。
func ensureWorkStreamReal(t *testing.T, js bus.JetStream) *Queue {
	t.Helper()
	q := NewQueue(js)
	if err := q.EnsureWorkStream(context.Background()); err != nil {
		t.Fatalf("EnsureWorkStream: %v", err)
	}
	return q
}

// purgeWorkSubject 删一个 env 的 durable consumer + purge 该 subject 上
// 残留消息，防止上一次跑挂在 dedupe 窗口里影响下一次。
func purgeWorkSubject(t *testing.T, js bus.JetStream, envID uuid.UUID) {
	t.Helper()
	raw := js.RawJetStream()
	if raw == nil {
		return
	}
	ctx := context.Background()
	durable := "worker-" + envID.String()
	_ = raw.DeleteConsumer(ctx, WorkStreamName, durable)
	stream, err := raw.Stream(ctx, WorkStreamName)
	if err != nil {
		return
	}
	_ = stream.Purge(ctx, jetstream.WithPurgeSubject(WorkSubjectPrefix+envID.String()))
}

// TestQueueReal_LongPollGetsNotified ：另一个 goroutine 50ms 后 Enqueue
// → FetchWork 在 wait 内拿到。验证端到端 publish + pull-fetch 真的串起来。
func TestQueueReal_LongPollGetsNotified(t *testing.T) {
	_, _, js := natsBrokerOrSkip(t)
	q := ensureWorkStreamReal(t, js)

	envID := uuid.New()
	t.Cleanup(func() { purgeWorkSubject(t, js, envID) })

	workID := "wq-" + uniqStreamSuffix(t)
	payload := map[string]any{"prompt": "hi", "stamp": time.Now().UnixNano()}

	// 一个 goroutine 50ms 后投递；同时 FetchWork 阻塞 wait
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := q.EnqueueWork(context.Background(), envID, workID, payload); err != nil {
			t.Errorf("Enqueue: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := q.FetchWork(ctx, envID, 2*time.Second)
	if err != nil {
		t.Fatalf("FetchWork: %v", err)
	}
	if got == nil {
		t.Fatal("FetchWork returned nil; expected work delivered")
	}
	defer func() { _ = got.Ack() }()

	var decoded map[string]any
	if err := json.Unmarshal(got.Body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded["prompt"] != "hi" {
		t.Errorf("body lost: %+v", decoded)
	}
}

// TestQueueReal_FetchEmptyTimesOut ：subject 没消息时 FetchWork 在 wait 后
// 返回 (nil, nil)，**不**返错。worker 端 pollLoop 依赖这个语义判断"空轮询
// 继续下一轮"。
func TestQueueReal_FetchEmptyTimesOut(t *testing.T) {
	_, _, js := natsBrokerOrSkip(t)
	q := ensureWorkStreamReal(t, js)

	envID := uuid.New() // fresh env_id → 永远没消息
	t.Cleanup(func() { purgeWorkSubject(t, js, envID) })

	start := time.Now()
	got, err := q.FetchWork(context.Background(), envID, 300*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchWork: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil work on empty queue, got %+v", got)
	}
	if elapsed < 250*time.Millisecond {
		t.Errorf("returned too fast: %v (want ≥250ms wait)", elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("returned too slow: %v (want ≤1.5s)", elapsed)
	}
}

// TestQueueReal_DeadWorkerAutoRedeliver ：worker A 拿了 work 但**不** ack
// （模拟挂掉）→ AckWait 后 worker B FetchWork 拿到同条消息。
//
// 这测试要等 AckWait（生产 60s）；改 consumer 在测试里造低 AckWait 麻烦
// （Queue.FetchWork 写死了 60s）。这里我们手动构造低 AckWait consumer 直
// 接调 raw JS，验证机制本身工作 —— Queue.FetchWork 的 60s 是同一机制只是
// 时间长。
//
// 不修改生产代码的折中：调用 Queue.EnqueueWork（生产路径）+ 自己创低
// AckWait consumer fetch + 不 ack + 等 AckWait + 再 fetch。
func TestQueueReal_DeadWorkerAutoRedeliver(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping AckWait redeliver test in -short mode")
	}
	_, _, js := natsBrokerOrSkip(t)
	q := ensureWorkStreamReal(t, js)

	envID := uuid.New()
	t.Cleanup(func() { purgeWorkSubject(t, js, envID) })

	workID := "wd-" + uniqStreamSuffix(t)
	if err := q.EnqueueWork(context.Background(), envID, workID,
		map[string]any{"who": "redeliver"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// 用 raw JS 起一个 AckWait=1s 的 durable consumer（测试专用名字，不
	// 跟 Queue.FetchWork 用的 worker-<env_id> 冲）—— 让 redeliver 在秒级
	// 发生而不是 60s。
	raw := js.RawJetStream()
	if raw == nil {
		t.Skip("RawJetStream unavailable; cannot run redeliver test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	durable := "test-redeliver-" + uniqStreamSuffix(t)
	cons, err := raw.CreateOrUpdateConsumer(ctx, WorkStreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		Name:          durable,
		FilterSubject: WorkSubjectPrefix + envID.String(),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       1 * time.Second,
		MaxDeliver:    5,
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	t.Cleanup(func() { _ = raw.DeleteConsumer(context.Background(), WorkStreamName, durable) })

	// 第一次 fetch —— 拿到但**不** ack
	batch1, err := cons.Fetch(1, jetstream.FetchMaxWait(1*time.Second))
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	var seen1 bool
	for msg := range batch1.Messages() {
		seen1 = true
		_ = msg // 故意不 ack 模拟死 worker
	}
	if !seen1 {
		t.Fatal("first fetch got no message")
	}

	// 等 AckWait 触发 redeliver（1s + buffer）
	time.Sleep(1500 * time.Millisecond)

	// 第二次 fetch —— 应该拿到同一条消息（redeliver）
	batch2, err := cons.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("redeliver fetch: %v", err)
	}
	var seen2 bool
	for msg := range batch2.Messages() {
		seen2 = true
		// 这次 ack 防止后续 redeliver 污染下个测试
		_ = msg.Ack()
	}
	if !seen2 {
		t.Errorf("expected redelivered message after AckWait")
	}
}

// TestQueueReal_Idempotency ：同 workID 在 dedupeWindow（10min）内重复
// Enqueue 只投递一次。关键性 —— biu daemon retry / brain 重启重投都靠这个。
func TestQueueReal_Idempotency(t *testing.T) {
	_, _, js := natsBrokerOrSkip(t)
	q := ensureWorkStreamReal(t, js)

	envID := uuid.New()
	t.Cleanup(func() { purgeWorkSubject(t, js, envID) })

	workID := "wi-" + uniqStreamSuffix(t)
	for i := 0; i < 3; i++ {
		if err := q.EnqueueWork(context.Background(), envID, workID,
			map[string]any{"attempt": i}); err != nil {
			t.Fatalf("Enqueue #%d: %v", i, err)
		}
	}

	// 第一次 fetch 应该拿到一条
	got, err := q.FetchWork(context.Background(), envID, 1*time.Second)
	if err != nil {
		t.Fatalf("FetchWork: %v", err)
	}
	if got == nil {
		t.Fatal("expected one delivered work")
	}
	_ = got.Ack()

	// 第二次 fetch 应该没东西（dedupe 窗口里其他两条被 broker 吃掉了）
	got2, err := q.FetchWork(context.Background(), envID, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("second FetchWork: %v", err)
	}
	if got2 != nil {
		t.Errorf("idempotency broken: got second message %s", got2.Body)
		_ = got2.Ack()
	}
}
