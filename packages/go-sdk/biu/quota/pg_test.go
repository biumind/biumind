package quota

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dsn returns a test Postgres DSN. Tests SKIP when QUOTA_TEST_DATABASE_URL
// is unset so CI and laptop runs without docker-compose still pass.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("QUOTA_TEST_DATABASE_URL")
	if d == "" {
		t.Skip("set QUOTA_TEST_DATABASE_URL=postgres://... to run pg limiter tests")
	}
	return d
}

// freshPool opens a pool to a unique schema so parallel test runs don't
// stomp on each other. The schema is dropped on cleanup.
func freshPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	schema := fmt.Sprintf("quota_test_%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		pool.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
		pool.Close()
		t.Fatalf("set search_path: %v", err)
	}
	// pgxpool returns connections from a pool, so search_path on the
	// pool isn't sticky. Re-acquire with an exec config — easier path is
	// a fresh DSN with options.
	pool.Close()

	dropDSN := dsn(t)
	cfg, err := pgxpool.ParseConfig(dropDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}

	cleanup := func() {
		// Open a fresh connection without search_path to drop the schema.
		raw, err := pgxpool.New(context.Background(), dropDSN)
		if err == nil {
			_, _ = raw.Exec(context.Background(),
				fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
			raw.Close()
		}
		pool.Close()
	}
	return pool, cleanup
}

func TestPGLimiter_HappyPathAndExceed(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, map[string]Spec{
		"hub.rpm": {Window: time.Minute, Limit: 3, Unit: "requests"},
	})

	for i := 0; i < 3; i++ {
		d := l.CheckAndReserve("hub.rpm", "vk-1", 1)
		if !d.Allow {
			t.Fatalf("call %d should succeed: %+v", i, d)
		}
		if d.Remaining != int64(2-i) {
			t.Errorf("call %d remaining=%d want %d", i, d.Remaining, 2-i)
		}
	}
	d := l.CheckAndReserve("hub.rpm", "vk-1", 1)
	if d.Allow {
		t.Errorf("4th call should be denied: %+v", d)
	}
	if d.Remaining != 0 || d.Limit != 3 {
		t.Errorf("denied decision: %+v", d)
	}
}

func TestPGLimiter_KeysAreIndependent(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, map[string]Spec{
		"daily": {Window: 24 * time.Hour, Limit: 1},
	})
	if !l.CheckAndReserve("daily", "alice", 1).Allow {
		t.Fatal("alice 1st")
	}
	if l.CheckAndReserve("daily", "alice", 1).Allow {
		t.Error("alice 2nd should deny")
	}
	if !l.CheckAndReserve("daily", "bob", 1).Allow {
		t.Error("bob shouldn't share alice's bucket")
	}
}

func TestPGLimiter_BucketWithoutSpecAlwaysAllows(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, nil)
	d := l.CheckAndReserve("nonexistent", "k", 999)
	if !d.Allow {
		t.Error("unspec'd bucket should allow")
	}
	if d.Limit != 0 {
		t.Error("unspec'd bucket should report no limit")
	}
}

func TestPGLimiter_WindowRollover(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, map[string]Spec{
		"hub.rpm": {Window: 100 * time.Millisecond, Limit: 1},
	}).(*pgLimiter)

	if !l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Fatal("first call")
	}
	if l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Error("within-window second should deny")
	}
	// Advance the limiter's clock past the window. We don't sleep —
	// withClock stamps every now() forward.
	future := time.Now().Add(1 * time.Second)
	l.withClock(func() time.Time { return future })
	if !l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Error("post-rollover should allow")
	}
}

func TestPGLimiter_RefundClampsAtZero(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, map[string]Spec{
		"x": {Window: time.Minute, Limit: 10},
	})
	l.CheckAndReserve("x", "k", 5)
	l.Refund("x", "k", 999)
	d := l.Snapshot("x", "k")
	if d.Remaining != 10 {
		t.Errorf("over-refund should clamp; got remaining=%d", d.Remaining)
	}
}

func TestPGLimiter_SnapshotDoesNotIncrement(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	l := NewPGLimiter(pool, map[string]Spec{
		"x": {Window: time.Minute, Limit: 5},
	})
	for i := 0; i < 10; i++ {
		l.Snapshot("x", "k")
	}
	if !l.CheckAndReserve("x", "k", 5).Allow {
		t.Error("snapshot must not consume")
	}
}

// TestPGLimiter_MultiReplica simulates two service replicas hammering
// the same (bucket, key) concurrently. Total allowances must equal the
// limit even though no in-process mutex coordinates them — Postgres is
// the source of truth.
func TestPGLimiter_MultiReplica(t *testing.T) {
	pool, cleanup := freshPool(t)
	defer cleanup()

	specs := map[string]Spec{"x": {Window: time.Minute, Limit: 100}}
	replica1 := NewPGLimiter(pool, specs)
	replica2 := NewPGLimiter(pool, specs)

	var allowed, denied int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		l := replica1
		if i%2 == 0 {
			l = replica2
		}
		go func() {
			defer wg.Done()
			if l.CheckAndReserve("x", "k", 1).Allow {
				atomic.AddInt64(&allowed, 1)
			} else {
				atomic.AddInt64(&denied, 1)
			}
		}()
	}
	wg.Wait()

	if allowed != 100 || denied != 100 {
		t.Errorf("multi-replica: want 100 allowed / 100 denied, got %d/%d",
			allowed, denied)
	}
}
