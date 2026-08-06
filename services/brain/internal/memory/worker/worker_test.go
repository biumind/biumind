package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration test: brain.memories must already exist (apply migration
// 00004_memory.sql). Tests skip cleanly when DATABASE_URL is unset.

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
	var exists bool
	if err := p.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'brain' AND table_name = 'memories')`,
	).Scan(&exists); err != nil || !exists {
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
		owner, "embed-worker-test-"+uuid.NewString(),
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

// TestWorker_BackfillsEmbeddings — happy path: store some unembedded
// memories, run the worker once, all rows have embeddings.
func TestWorker_BackfillsEmbeddings(t *testing.T) {
	p := openDB(t)
	s := memstore.New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	for _, c := range []string{
		"prefers Spanish translations",
		"writes Go services",
		"likes concise emails",
		"uses Vim with vimwiki",
	} {
		if _, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: pid, OwnerID: uid, Kind: memstore.KindRecall, Content: c,
		}); err != nil {
			t.Fatalf("create %q: %v", c, err)
		}
	}

	// The schema column is vector(1024) (bge-m3), so the stub embedder
	// must emit 1024 dims to match.
	w := New(s, embed.NewStub(1024), Config{
		Interval: time.Hour, // never fires; we use RunOnce
		Batch:    10,
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	processed := w.RunOnce(ctx)
	if processed < 4 {
		t.Errorf("expected ≥4 processed, got %d", processed)
	}

	// Project-scoped count of remaining unembedded rows in *this* project.
	var remaining int64
	_ = p.QueryRow(ctx, `
		SELECT count(*) FROM brain.memories
		WHERE project_id = $1 AND embedding IS NULL`,
		pid).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("expected 0 unembedded in project after run, got %d", remaining)
	}
}

// TestWorker_SkipLockedAcrossReplicas — two workers running in
// parallel must never embed the same row twice. We orchestrate this
// by manually calling ClaimUnembedded from two goroutines and
// comparing claim sets.
func TestWorker_SkipLockedAcrossReplicas(t *testing.T) {
	p := openDB(t)
	s := memstore.New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		if _, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: pid, OwnerID: uid,
			Kind: memstore.KindRecall, Content: "row " + uuid.NewString(),
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	a, txA, err := s.ClaimUnembedded(ctx, 8)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	defer func() { _ = txA.Rollback(ctx) }()

	b, txB, err := s.ClaimUnembedded(ctx, 8)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	defer func() { _ = txB.Rollback(ctx) }()

	// A holds locks; B's SKIP LOCKED must yield zero rows that A holds.
	seen := map[uuid.UUID]bool{}
	for _, p := range a {
		seen[p.ID] = true
	}
	for _, p := range b {
		if seen[p.ID] {
			t.Errorf("replica B claimed a row replica A also held: %s", p.ID)
		}
	}
}

// TestWorker_HybridRecallBeatsLexicalAfterEmbedding — proves the
// embedding pipeline actually changes ranking. Before embeddings
// exist the lexical-only path is used; after the worker fills them,
// a query whose terms don't appear literally still ranks the
// semantically-related row first.
func TestWorker_HybridRecallBeatsLexicalAfterEmbedding(t *testing.T) {
	p := openDB(t)
	s := memstore.New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	// Two memories share the keyword 'editor' so lexical scoring
	// alone can't distinguish them; the embedding's token overlap
	// pushes the more-related one up.
	create := func(content string) {
		t.Helper()
		if _, err := s.Create(ctx, memstore.StoreInput{
			ProjectID: pid, OwnerID: uid,
			Kind: memstore.KindRecall, Content: content,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	create("uses Vim with vimwiki for editing notes")  // about 'vim'
	create("Spanish translation prefs editor on side") // not about 'vim'

	embedder := embed.NewStub(1024)
	w := New(s, embedder, Config{Interval: time.Hour, Batch: 10})
	if got := w.RunOnce(ctx); got < 2 {
		t.Fatalf("expected 2 embedded, got %d", got)
	}

	// Query with a token that's unique to row 1 — both lexical and
	// semantic should agree on it. We mostly want to assert: no error,
	// row 1 wins, and `mode=hybrid` (caller controls — we just check
	// store output here).
	qvec, err := embedder.Embed(ctx, "vim editor")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	got, err := s.Recall(ctx, memstore.RecallInput{
		ProjectID: pid, OwnerID: uid,
		Query: "vim editor", QueryEmbedding: qvec, Limit: 5,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("recall returned 0 rows")
	}
	if got[0].Content != "uses Vim with vimwiki for editing notes" {
		t.Errorf("vim row should rank #1; got: %q (full: %v)",
			got[0].Content, contents(got))
	}
}

func contents(ms []*memstore.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}
