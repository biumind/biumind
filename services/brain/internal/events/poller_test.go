// Integration tests against a real Postgres. Skip when DATABASE_URL is
// unset so go test ./... stays green on workstations without infra.
package events

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedEvent inserts directly with published_at = NULL so the poller
// has something to find. Returns the inserted id. We bypass emitEvent
// + the trigger here because we don't care about NOTIFY — the poller
// path under test is independent of LISTEN.
func seedEvent(t *testing.T, pool *pgxpool.Pool, scope, kind string, payload string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, 'system', 'test', $2, $3::jsonb)
		RETURNING id
	`, scope, kind, payload).Scan(&id)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return id
}

// resetPublishedAt nulls out published_at for the rows we just inserted
// (the migration's backfill stamps them with created_at to avoid history
// replay; tests want fresh rows to be unpublished).
func resetPublishedAt(t *testing.T, pool *pgxpool.Pool, ids ...int64) {
	t.Helper()
	if len(ids) == 0 {
		return
	}
	_, err := pool.Exec(context.Background(),
		`UPDATE brain.events SET published_at = NULL WHERE id = ANY($1)`,
		ids)
	if err != nil {
		t.Fatalf("reset published_at: %v", err)
	}
}

func unpublishedCount(t *testing.T, pool *pgxpool.Pool, ids ...int64) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM brain.events
		  WHERE id = ANY($1) AND published_at IS NULL`, ids).Scan(&n)
	if err != nil {
		t.Fatalf("count unpublished: %v", err)
	}
	return n
}

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ─── Tests ──────────────────────────────────────────────

// TestPoller_HappyPath — seed 3 unpublished rows, run one batch, verify
// all three reach the publisher and all three are marked published_at.
func TestPoller_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	mem := &publisher.Memory{}
	p := &Poller{Pool: pool, Publisher: mem, Logger: nopLogger(), Batch: 100}

	scope := fmt.Sprintf("test:poller:%d", time.Now().UnixNano())
	id1 := seedEvent(t, pool, scope, "block.created", `{"i":1}`)
	id2 := seedEvent(t, pool, scope, "block.created", `{"i":2}`)
	id3 := seedEvent(t, pool, scope, "block.created", `{"i":3}`)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM brain.events WHERE id = ANY($1)`,
			[]int64{id1, id2, id3})
	})
	resetPublishedAt(t, pool, id1, id2, id3)

	processed, err := p.processBatch(context.Background())
	if err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if processed < 3 {
		t.Errorf("processed = %d, want ≥ 3", processed)
	}
	if c := unpublishedCount(t, pool, id1, id2, id3); c != 0 {
		t.Errorf("unpublished still %d, want 0", c)
	}
	// Publisher saw all 3 (allow more in case other tests' leftovers
	// run in the same batch — what we care about is *our* events).
	got := 0
	for _, e := range mem.Events {
		if e.Topic == scope {
			got++
		}
	}
	if got != 3 {
		t.Errorf("publisher saw %d events for our scope, want 3", got)
	}
}

// TestPoller_AlreadyPublishedSkipped — rows with published_at NOT NULL
// must be ignored. Otherwise a Listener+Poller race could double-deliver
// every event in steady state.
func TestPoller_AlreadyPublishedSkipped(t *testing.T) {
	pool := newTestPool(t)
	mem := &publisher.Memory{}
	p := &Poller{Pool: pool, Publisher: mem, Logger: nopLogger(), Batch: 100}

	scope := fmt.Sprintf("test:already:%d", time.Now().UnixNano())
	id := seedEvent(t, pool, scope, "x", `{}`)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM brain.events WHERE id = $1`, id)
	})
	// Leave published_at as the seed default (NOT NULL — the column was
	// just added with backfill, but a fresh row inserted *after* the
	// migration has published_at = NULL by default. We need to mark
	// it published explicitly to test the skip.)
	_, err := pool.Exec(context.Background(),
		`UPDATE brain.events SET published_at = now() WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("mark published: %v", err)
	}

	processed, err := p.processBatch(context.Background())
	if err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	for _, e := range mem.Events {
		if e.Topic == scope {
			t.Errorf("already-published row was redelivered: %+v", e)
		}
	}
	_ = processed
}

// TestPoller_PublishFailureLeavesRowPending — when the publisher
// returns an error the row must remain unpublished (so the next tick
// retries) and other successful rows in the same batch must commit.
func TestPoller_PublishFailureLeavesRowPending(t *testing.T) {
	pool := newTestPool(t)
	scope := fmt.Sprintf("test:fail:%d", time.Now().UnixNano())
	idA := seedEvent(t, pool, scope, "ok-1", `{}`)
	idFail := seedEvent(t, pool, scope, "fail", `{}`)
	idB := seedEvent(t, pool, scope, "ok-2", `{}`)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM brain.events WHERE id = ANY($1)`,
			[]int64{idA, idFail, idB})
	})
	resetPublishedAt(t, pool, idA, idFail, idB)

	failOn := "fail"
	pub := &flakyPublisher{failKind: failOn}
	p := &Poller{Pool: pool, Publisher: pub, Logger: nopLogger(), Batch: 100}

	if _, err := p.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	// idFail should still be unpublished; the other two should be done.
	pendingFail := unpublishedCount(t, pool, idFail)
	if pendingFail != 1 {
		t.Errorf("failing row should be unpublished, got count=%d", pendingFail)
	}
	pendingOk := unpublishedCount(t, pool, idA, idB)
	if pendingOk != 0 {
		t.Errorf("ok rows should be published, got pending=%d", pendingOk)
	}

	// Recover: drop the failure injection and retry. The retry should
	// now drain idFail.
	pub.failKind = ""
	if _, err := p.processBatch(context.Background()); err != nil {
		t.Fatalf("retry processBatch: %v", err)
	}
	if c := unpublishedCount(t, pool, idFail); c != 0 {
		t.Errorf("after recovery row should be published, count=%d", c)
	}
}

// TestPoller_MultiReplicaWorkSharingNoDoubleDeliver — two pollers on the
// same pool must not BOTH publish the same row. SKIP LOCKED is the
// guarantee; this test pins it.
func TestPoller_MultiReplicaWorkSharingNoDoubleDeliver(t *testing.T) {
	pool := newTestPool(t)
	scope := fmt.Sprintf("test:multi:%d", time.Now().UnixNano())

	const N = 20
	ids := make([]int64, 0, N)
	for i := 0; i < N; i++ {
		ids = append(ids, seedEvent(t, pool, scope, "x",
			fmt.Sprintf(`{"i":%d}`, i)))
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM brain.events WHERE id = ANY($1)`, ids)
	})
	resetPublishedAt(t, pool, ids...)

	pubA := &countingPublisher{scope: scope}
	pubB := &countingPublisher{scope: scope}

	pA := &Poller{Pool: pool, Publisher: pubA, Logger: nopLogger(), Batch: 10}
	pB := &Poller{Pool: pool, Publisher: pubB, Logger: nopLogger(), Batch: 10}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = pA.processBatch(context.Background())
		_, _ = pA.processBatch(context.Background())
	}()
	go func() {
		defer wg.Done()
		_, _ = pB.processBatch(context.Background())
		_, _ = pB.processBatch(context.Background())
	}()
	wg.Wait()

	total := atomic.LoadInt64(&pubA.count) + atomic.LoadInt64(&pubB.count)
	if total != int64(N) {
		t.Errorf("multi-replica delivery total = %d, want %d (some rows %s)",
			total, N,
			map[bool]string{true: "double-delivered", false: "missed"}[total > int64(N)])
	}
	if c := unpublishedCount(t, pool, ids...); c != 0 {
		t.Errorf("after multi-replica drain, %d rows still unpublished", c)
	}
}

// TestPoller_BadPayloadMarkedPublishedAvoidsLoop — a row whose payload
// fails json.Unmarshal would otherwise loop forever; the poller marks
// it published and moves on.
func TestPoller_BadPayloadMarkedPublishedAvoidsLoop(t *testing.T) {
	pool := newTestPool(t)
	// Garbage-json by inserting a non-object jsonb value. jsonb itself
	// requires valid JSON, but an array doesn't unmarshal into our
	// expected map[string]any → the poller's defensive branch fires.
	scope := fmt.Sprintf("test:bad:%d", time.Now().UnixNano())
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, 'system', 'test', 'x', '"not-an-object"'::jsonb)
		RETURNING id
	`, scope).Scan(&id)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM brain.events WHERE id = $1`, id)
	})
	resetPublishedAt(t, pool, id)

	mem := &publisher.Memory{}
	p := &Poller{Pool: pool, Publisher: mem, Logger: nopLogger(), Batch: 10}
	if _, err := p.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if c := unpublishedCount(t, pool, id); c != 0 {
		t.Errorf("bad-payload row should be marked published to break the loop; pending=%d", c)
	}
}

// ─── helpers ─────────────────────────────────────────

type flakyPublisher struct {
	mu       sync.Mutex
	failKind string // when matched, return error
	events   []publisher.Captured
}

func (f *flakyPublisher) Publish(_ context.Context, topic, kind string,
	payload map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind == f.failKind {
		return errors.New("simulated transient publisher failure")
	}
	f.events = append(f.events, publisher.Captured{
		Topic: topic, Kind: kind, Payload: payload,
	})
	return nil
}

type countingPublisher struct {
	scope string
	count int64
}

func (c *countingPublisher) Publish(_ context.Context, topic, _ string,
	_ map[string]any) error {
	if topic == c.scope {
		atomic.AddInt64(&c.count, 1)
	}
	return nil
}
