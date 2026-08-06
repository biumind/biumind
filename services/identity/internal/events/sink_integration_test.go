// sink_integration_test.go — W3-5 端到端: publish → JetStream → sink → PG.
//
// 需要真实 NATS + PG. 默认连本地 docker-compose. 设
//   EVENTS_TEST_NATS_URL=skip 或 EVENTS_TEST_DATABASE_URL=skip
// 跳过.
//
// 测试用 unique consumer name 防止干扰生产 sink (主 identity 进程跑的那个).

package events

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	natsjs "github.com/nats-io/nats.go/jetstream"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

func sinkTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("EVENTS_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
	}
	if url == "skip" {
		t.Skip("EVENTS_TEST_DATABASE_URL=skip")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func sinkTestBus(t *testing.T) bus.Bus {
	t.Helper()
	url := os.Getenv("EVENTS_TEST_NATS_URL")
	if url == "" {
		url = "nats://localhost:4222"
	}
	if url == "skip" {
		t.Skip("EVENTS_TEST_NATS_URL=skip")
	}
	b, err := bus.Connect(url, "sink-test", "sinktest")
	if err != nil {
		t.Skipf("NATS unreachable: %v", err)
	}
	if !b.Connected() {
		t.Skip("NATS not connected")
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// TestSinkE2E_PublishToInsert — 起 sink, publish 5 条 ConsumeEvent, 等批量
// flush, SELECT 应该看到 5 行.
func TestSinkE2E_PublishToInsert(t *testing.T) {
	pool := sinkTestPool(t)
	b := sinkTestBus(t)
	js, err := b.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// 用唯一 env 隔离: stream subject 仍是 biumind.>, 但 sink + publisher
	// 都用 sinktest_<rand> 防跟生产 identity 串.
	env := "sinktest_" + uuid.NewString()[:8]

	// 测试用单独 stream, 防 SinkConsumerName 冲突生产 stream.
	streamName := "TEST_BILLING_" + uuid.NewString()[:8]
	if err := js.EnsureStream(context.Background(), bus.StreamSpec{
		Name:     streamName,
		Subjects: []string{bus.Subject(env, "billing", "events") + ".>"},
		MaxAge:   1 * time.Hour,
	}); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}
	t.Cleanup(func() {
		// 不通过 JS 接口就好删, raw nats 操作需要 admin perms; 留给运维.
	})

	// 自定义 sink 用唯一 consumer name + 这个 stream
	sink := newSinkForTest(pool, js, env, streamName)
	flushed := atomic.Int64{}
	sink.SetOnFlush(func(n int) { flushed.Add(int64(n)) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sink.Run(ctx) }()
	time.Sleep(200 * time.Millisecond) // 让 consumer 起来

	pub := NewNATSPublisher(js, env, nil)
	user := uuid.New()
	for i := 0; i < 5; i++ {
		if err := pub.PublishConsume(context.Background(), ConsumeEvent{
			Common:    Common{UserID: user, IdempotencyKey: fmt.Sprintf("e2e-%d", i)},
			LogID:     uuid.New(),
			Amount:    int64(10 * (i + 1)),
			ModelCode: "claude-sonnet-4-6",
			RefType:   "chat_message",
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// 等到 flush. 5 条 < 1000 batch_max → 走 5s ticker.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if flushed.Load() >= 5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if flushed.Load() < 5 {
		t.Fatalf("only %d events flushed in 15s", flushed.Load())
	}

	// SELECT 验证
	var count int
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM billing.events WHERE env = $1 AND user_id = $2`,
		env, user).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("count=%d want 5", count)
	}

	// 抽一行验字段保真
	var amount int64
	var modelCode string
	err = pool.QueryRow(context.Background(),
		`SELECT amount, model_code FROM billing.events
		 WHERE env = $1 AND user_id = $2 AND idempotency_key = 'e2e-0'`,
		env, user).Scan(&amount, &modelCode)
	if err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if amount != 10 || modelCode != "claude-sonnet-4-6" {
		t.Errorf("row: amount=%d model=%q", amount, modelCode)
	}

	// cleanup test rows
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM billing.events WHERE env = $1`, env)
}

// TestSinkE2E_DuplicateInserts — 同一 event_id 重投 ON CONFLICT DO NOTHING
// 不报错, 只插一行.
func TestSinkE2E_DuplicateInserts(t *testing.T) {
	pool := sinkTestPool(t)
	env := "siktest_dup_" + uuid.NewString()[:8]
	user := uuid.New()
	eventID := uuid.New()
	occurred := time.Now().UTC().Truncate(time.Microsecond)

	sink := &Sink{pool: pool, env: env, logger: slog.Default()}
	rows := []rawEvent{
		{
			EventID: eventID, UserID: user, OccurredAt: occurred, Env: env,
			kind:   "consume",
			Amount: int64Ptr(100),
		},
		{
			// 同 event_id 同 occurred_at — 主键冲突, 走 DO NOTHING
			EventID: eventID, UserID: user, OccurredAt: occurred, Env: env,
			kind:    "consume",
			Amount:  int64Ptr(999), // 故意改值, 验证不会被覆盖
			payload: []byte(`{"second":true}`),
		},
	}
	if err := sink.insertBatch(context.Background(), rows); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var amount int64
	err := pool.QueryRow(context.Background(),
		`SELECT amount FROM billing.events WHERE event_id = $1`, eventID).Scan(&amount)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if amount != 100 {
		t.Errorf("dedup failed: amount=%d want 100", amount)
	}
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM billing.events WHERE env = $1`, env)
}

func int64Ptr(v int64) *int64 { return &v }

// newSinkForTest — 给测试用 unique consumer name + 自定义 stream.
func newSinkForTest(pool *pgxpool.Pool, js bus.JetStream, env, streamName string) *Sink {
	// 因为 Sink.Run 写死 StreamName, 测试用 alt struct 走 NATS 测试 stream.
	// 简单起见: 用 testSink wrapper 调 Subscribe 自己实现, 复用 insertBatch.
	return &Sink{
		pool:   pool,
		js:     jsAlias{js: js, streamName: streamName},
		env:    env,
		logger: slog.Default(),
	}
}

// jsAlias — 把 Subscribe 的 stream 强制改写成测试 stream. 其余转发原 js.
type jsAlias struct {
	js         bus.JetStream
	streamName string
}

func (a jsAlias) EnsureStream(ctx context.Context, s bus.StreamSpec) error {
	return a.js.EnsureStream(ctx, s)
}
func (a jsAlias) Publish(ctx context.Context, subj string, p any, h ...bus.Header) error {
	return a.js.Publish(ctx, subj, p, h...)
}
func (a jsAlias) Subscribe(ctx context.Context, spec bus.ConsumerSpec, h bus.JSHandler) (bus.Subscription, error) {
	spec.Stream = a.streamName
	spec.Durable = "test-" + uuid.NewString()[:8]
	return a.js.Subscribe(ctx, spec, h)
}

// jetstream.JetStream 接口要求, 测试不用.
func (a jsAlias) RawJetStream() natsjs.JetStream { return nil }
