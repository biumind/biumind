package consolidator

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	memworker "github.com/biumind/biumind/services/brain/internal/memory/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	var ok bool
	if err := p.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'brain' AND table_name = 'memories')`,
	).Scan(&ok); err != nil || !ok {
		t.Skip("brain.memories not present; apply migrations/00004_memory.sql first")
	}
	return p
}

func seedProject(t *testing.T, p *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	var id uuid.UUID
	err := p.QueryRow(context.Background(),
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		owner, "consolidator-test-"+uuid.NewString(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, id)
	})
	return id, owner
}

func TestDedupe_MergesIdenticalContent(t *testing.T) {
	pool := openDB(t)
	s := memstore.New(pool)
	pid, uid := seedProject(t, pool)
	ctx := context.Background()

	// Two memories with the SAME content → stub embedder produces
	// identical vectors → cosine distance = 0.
	for i := 0; i < 2; i++ {
		m, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: pid, OwnerID: uid,
			Kind: memstore.KindRecall, Content: "use vimwiki",
			Salience: 0.3 + 0.2*float32(i), // 0.3, 0.5
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_ = m
	}
	// Embed them so the dedupe pass has something to compare.
	w := memworker.New(s, embed.NewStub(1024),
		memworker.Config{Interval: time.Hour, Batch: 10})
	if got := w.RunOnce(ctx); got != 2 {
		t.Fatalf("worker: want 2 embedded, got %d", got)
	}

	cons := New(pool, Config{
		Interval: time.Hour, CosineThreshold: 0.05,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	st := cons.RunOnce(ctx)
	if st.MergedRows != 1 {
		t.Errorf("merged rows: got %d, want 1", st.MergedRows)
	}

	// Survivor must be the one with higher salience (0.5).
	var n int
	var survivor float32
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(salience) FROM brain.memories WHERE project_id = $1`,
		pid).Scan(&n, &survivor); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows after dedupe: %d, want 1", n)
	}
	if survivor < 0.5-1e-3 {
		t.Errorf("winner should keep salience ≥ 0.5, got %v", survivor)
	}
}

func TestDedupe_DoesNotMergeDifferentContent(t *testing.T) {
	pool := openDB(t)
	s := memstore.New(pool)
	pid, uid := seedProject(t, pool)
	ctx := context.Background()

	for _, c := range []string{
		"use rust for systems code",
		"prefer Spanish in summaries",
		"deploy via biu push",
	} {
		if _, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: pid, OwnerID: uid,
			Kind: memstore.KindRecall, Content: c,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	w := memworker.New(s, embed.NewStub(1024),
		memworker.Config{Interval: time.Hour, Batch: 10})
	w.RunOnce(ctx)

	cons := New(pool, Config{Interval: time.Hour, CosineThreshold: 0.05})
	st := cons.RunOnce(ctx)
	if st.MergedRows != 0 {
		t.Errorf("disjoint memories must not merge; merged=%d", st.MergedRows)
	}
}

func TestDedupe_DoesNotCrossProjects(t *testing.T) {
	pool := openDB(t)
	s := memstore.New(pool)
	pidA, uidA := seedProject(t, pool)
	pidB, uidB := seedProject(t, pool)
	ctx := context.Background()

	// Same content in two projects — must NOT merge across the
	// project boundary even though embeddings are identical.
	for _, p := range []struct {
		pid uuid.UUID
		uid uuid.UUID
	}{{pidA, uidA}, {pidB, uidB}} {
		if _, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: p.pid, OwnerID: p.uid,
			Kind: memstore.KindRecall, Content: "use vimwiki",
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	w := memworker.New(s, embed.NewStub(1024),
		memworker.Config{Interval: time.Hour, Batch: 10})
	w.RunOnce(ctx)

	cons := New(pool, Config{Interval: time.Hour, CosineThreshold: 0.05})
	st := cons.RunOnce(ctx)
	if st.MergedRows != 0 {
		t.Errorf("cross-project leak: merged=%d", st.MergedRows)
	}
}

func TestDecay_ReducesSalienceOnIdleMemories(t *testing.T) {
	pool := openDB(t)
	s := memstore.New(pool)
	pid, uid := seedProject(t, pool)
	ctx := context.Background()

	m, err := s.Create(ctx, memstore.StoreInput{
		ProjectID: pid, OwnerID: uid,
		Kind: memstore.KindRecall, Content: "old fact",
		Salience: 0.8,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Backdate last_accessed_at so it crosses the idle threshold.
	if _, err := pool.Exec(ctx,
		`UPDATE brain.memories
		    SET last_accessed_at = now() - interval '14 days'
		  WHERE id = $1`, m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	cons := New(pool, Config{
		Interval:    time.Hour,
		DecayPerDay: 0.05, IdleAfterDays: 7,
	})
	st := cons.RunOnce(ctx)
	if st.DecayedRows < 1 {
		t.Errorf("expected ≥1 decayed row, got %d", st.DecayedRows)
	}

	got, _ := s.Get(ctx, uid, m.ID)
	// 14 days idle, 7 day grace, 7 days × 0.05 = 0.35 decay.
	// Allow a little floating-point slack.
	want := float32(0.8 - 7*0.05)
	if got.Salience > want+0.05 {
		t.Errorf("salience not decayed enough: got %v, want ≤ %v",
			got.Salience, want+0.05)
	}
}

func TestDecay_LeavesFreshMemoriesAlone(t *testing.T) {
	pool := openDB(t)
	s := memstore.New(pool)
	pid, uid := seedProject(t, pool)
	ctx := context.Background()

	m, err := s.Create(ctx, memstore.StoreInput{
		ProjectID: pid, OwnerID: uid,
		Kind: memstore.KindRecall, Content: "fresh fact",
		Salience: 0.6,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cons := New(pool, Config{
		Interval:    time.Hour,
		DecayPerDay: 0.05, IdleAfterDays: 7,
	})
	cons.RunOnce(ctx)

	got, _ := s.Get(ctx, uid, m.ID)
	if got.Salience < 0.6-1e-3 {
		t.Errorf("fresh memory decayed: %v", got.Salience)
	}
}
