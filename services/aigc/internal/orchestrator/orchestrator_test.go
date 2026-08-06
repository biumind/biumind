package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── 测试基础设施 ─────────────────────────────────────

func dbURL() string {
	if v := os.Getenv("AIGC_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool)
}

func ensureSeed(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	_ = s.UpsertProvider(ctx, store.UpsertProviderArgs{
		Code: "orch-prov", Name: "Orch Test", BaseURL: "https://test.local",
		Priority: 1, Enabled: true,
	})
	_ = s.UpsertModel(ctx, store.UpsertModelArgs{
		Code: "orch-model", Type: "image", DisplayName: "Orch Test",
		ProviderCode: "orch-prov", PriceCredits: 30,
		Config: []byte(`{}`), Enabled: true, SortOrder: 99,
	})
}

// fakeBus 是只为本包测试用的内存 Bus, 记录 Publish, 不实际订阅.
type fakeBus struct {
	mu     sync.Mutex
	pubs   []fakePub
	subErr error // Subscribe 强制返错时填这里
}

type fakePub struct {
	Subject string
	Body    []byte
}

func (b *fakeBus) Publish(ctx context.Context, subject string, payload any, _ ...bus.Header) error {
	body, _ := json.Marshal(payload)
	b.mu.Lock()
	b.pubs = append(b.pubs, fakePub{Subject: subject, Body: body})
	b.mu.Unlock()
	return nil
}
func (b *fakeBus) Subscribe(_ string, _ bus.Handler) (bus.Subscription, error) {
	if b.subErr != nil {
		return nil, b.subErr
	}
	return fakeSub{}, nil
}
func (b *fakeBus) JetStream() (bus.JetStream, error) { return nil, errors.New("noop") }
func (b *fakeBus) Close() error                      { return nil }
func (b *fakeBus) Connected() bool                   { return true }

func (b *fakeBus) lastPub() *fakePub {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pubs) == 0 {
		return nil
	}
	cp := b.pubs[len(b.pubs)-1]
	return &cp
}

type fakeSub struct{}

func (fakeSub) Drain() error { return nil }

func newOrchestrator(t *testing.T) (*Orchestrator, *fakeBus, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	ensureSeed(t, st)
	fb := &fakeBus{}
	return &Orchestrator{
		Store:  st,
		Bus:    fb,
		Env:    "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, fb, st
}

func newPendingTask(t *testing.T, s *store.Store, parentSHA, lineageOp string) *store.Task {
	t.Helper()
	uid := uuid.New()
	task, err := s.CreateTask(context.Background(), store.CreateTaskArgs{
		UserID: uid, Type: "image",
		ModelCode: "orch-model", ProviderCode: "orch-prov",
		Prompt: "p", CostCredits: 30,
		ParentSHA: parentSHA, LineageOp: lineageOp,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// ════════════════════════════════════════════════════════════
// Apply: queued / running / completed / failed
// ════════════════════════════════════════════════════════════

func TestApply_QueuedThenRunning(t *testing.T) {
	o, fb, st := newOrchestrator(t)
	task := newPendingTask(t, st, "", "")
	ctx := context.Background()

	// queued
	if err := o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "queued",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTask(ctx, task.ID)
	if got.Status != "queued" || got.QueuedAt == nil {
		t.Fatalf("after queued: %+v", got)
	}

	// running 50%
	if err := o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "running", Progress: 50,
		ExternalTaskID: "vendor-xyz",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask(ctx, task.ID)
	if got.Status != "running" || got.Progress != 50 || got.StartedAt == nil ||
		got.ExternalTaskID != "vendor-xyz" {
		t.Fatalf("after running: %+v", got)
	}

	// fan-out 至少 2 次 (每次 Apply 都 publish)
	if len(fb.pubs) < 2 {
		t.Errorf("fanout calls = %d, want >= 2", len(fb.pubs))
	}
	last := fb.lastPub()
	if last.Subject != "biumind.test.aigc.task.realtime" {
		t.Errorf("subject = %s", last.Subject)
	}
	var wire map[string]any
	_ = json.Unmarshal(last.Body, &wire)
	if !startsWith(wire["topic"].(string), "aigc.user.") {
		t.Errorf("topic = %v", wire["topic"])
	}
	if wire["kind"] != "aigc.task.update" {
		t.Errorf("kind = %v", wire["kind"])
	}
}

func TestApply_Completed_WritesOutputs(t *testing.T) {
	o, _, st := newOrchestrator(t)
	task := newPendingTask(t, st, "", "")
	ctx := context.Background()

	ev := &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "completed", Progress: 100,
		Outputs: []OutputEntry{
			{
				Idx: 0, Kind: "image", SHA256: "sha-abc",
				StorageURL: "cas:sha-abc",
				StorageKey: "outputs/sh/a/sha-abc.png",
				Width:      1024, Height: 1024, FileSize: 234567,
				Blurhash: "L6PZf...",
			},
		},
	}
	if err := o.Apply(ctx, ev); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetTask(ctx, task.ID)
	if got.Status != "completed" || got.Progress != 100 || got.CompletedAt == nil {
		t.Fatalf("task state: %+v", got)
	}
	outs, err := st.ListTaskOutputs(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 || outs[0].SHA256 != "sha-abc" || outs[0].Blurhash != "L6PZf..." {
		t.Fatalf("outputs: %+v", outs)
	}
}

func TestApply_Completed_BuildsLineageEdge(t *testing.T) {
	o, _, st := newOrchestrator(t)
	task := newPendingTask(t, st, "parent-sha-xx", "remix")
	ctx := context.Background()

	if err := o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "completed",
		Outputs: []OutputEntry{
			{Idx: 0, Kind: "image", SHA256: "child-sha-yy",
				StorageURL: "cas:child-sha-yy", StorageKey: "k"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	parents, _ := st.ListParentEdges(ctx, "child-sha-yy")
	if len(parents) != 1 {
		t.Fatalf("parents = %d, want 1", len(parents))
	}
	if parents[0].ParentSHA != "parent-sha-xx" || parents[0].Op != "remix" {
		t.Errorf("edge wrong: %+v", parents[0])
	}
}

func TestApply_DefaultLineageOp(t *testing.T) {
	o, _, st := newOrchestrator(t)
	task := newPendingTask(t, st, "p-sha", "") // 空 lineage_op 应默认 remix
	ctx := context.Background()
	_ = o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "completed",
		Outputs: []OutputEntry{{Idx: 0, Kind: "image", SHA256: "c-sha",
			StorageURL: "cas:c-sha", StorageKey: "k"}},
	})
	parents, _ := st.ListParentEdges(ctx, "c-sha")
	if len(parents) != 1 || parents[0].Op != "remix" {
		t.Errorf("default op: %+v", parents)
	}
}

func TestApply_Failed_RecordsRefund(t *testing.T) {
	o, _, st := newOrchestrator(t)
	task := newPendingTask(t, st, "", "")
	ctx := context.Background()

	if err := o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "failed",
		ErrorCode: "UPSTREAM_TIMEOUT", ErrorMessage: "timed out",
		RefundedCredits: 30,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTask(ctx, task.ID)
	if got.Status != "failed" || got.RefundedCredits != 30 ||
		got.ErrorCode != "UPSTREAM_TIMEOUT" {
		t.Fatalf("after failed: %+v", got)
	}
}

func TestApply_Cancelled(t *testing.T) {
	o, _, st := newOrchestrator(t)
	task := newPendingTask(t, st, "", "")
	ctx := context.Background()

	if err := o.Apply(ctx, &TaskUpdateEvent{
		TaskID: task.ID.String(), Status: "cancelled",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTask(ctx, task.ID)
	if got.Status != "cancelled" || got.CompletedAt == nil {
		t.Fatalf("cancelled: %+v", got)
	}
}

func TestApply_TaskNotFound_Silent(t *testing.T) {
	o, _, _ := newOrchestrator(t)
	// 随便编一个 id
	err := o.Apply(context.Background(), &TaskUpdateEvent{
		TaskID: uuid.New().String(), Status: "running",
	})
	if err != nil {
		t.Errorf("not-found should not error: %v", err)
	}
}

func TestApply_BadTaskID(t *testing.T) {
	o, _, _ := newOrchestrator(t)
	err := o.Apply(context.Background(), &TaskUpdateEvent{
		TaskID: "not-a-uuid", Status: "running",
	})
	if err == nil {
		t.Errorf("bad uuid should error")
	}
}

// ════════════════════════════════════════════════════════════
// Start (订阅启动)
// ════════════════════════════════════════════════════════════

func TestStart_SubErrorReturns(t *testing.T) {
	o, fb, _ := newOrchestrator(t)
	fb.subErr = errors.New("subscribe boom")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := o.Start(ctx); err == nil {
		t.Error("want error when Subscribe fails")
	}
}

func TestStart_GracefulDrain(t *testing.T) {
	o, _, _ := newOrchestrator(t)
	ctx, cancel := context.WithCancel(context.Background())
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	// 等 drain goroutine 跑一下不阻塞 (无断言, 不挂就行)
	time.Sleep(20 * time.Millisecond)
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
