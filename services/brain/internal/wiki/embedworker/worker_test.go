package embedworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/wiki/chunks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── fakes ─────────────────────────────────────────────────────

// halveFakeEmbedder rejects inputs longer than maxRunes with an
// oversize-shaped error and records every input length it saw, so tests
// can assert the halving sequence.
type halveFakeEmbedder struct {
	maxRunes int
	dim      int

	mu      sync.Mutex
	lengths []int
}

func (f *halveFakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	n := len([]rune(text))
	f.mu.Lock()
	f.lengths = append(f.lengths, n)
	f.mu.Unlock()
	if n > f.maxRunes {
		return nil, fmt.Errorf("embed: 400 Bad Request: input exceeds the maximum context length")
	}
	v := make([]float32, f.dim)
	v[0] = 1
	return v, nil
}

func (f *halveFakeEmbedder) Dim() int      { return f.dim }
func (f *halveFakeEmbedder) Model() string { return "halve-fake" }

func (f *halveFakeEmbedder) seen() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.lengths...)
}

// alwaysFailEmbedder never succeeds — drives the poison-pill path.
type alwaysFailEmbedder struct{ dim int }

func (f alwaysFailEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("embed: 500 Internal Server Error: provider exploded")
}
func (f alwaysFailEmbedder) Dim() int      { return f.dim }
func (f alwaysFailEmbedder) Model() string { return "always-fail" }

// ─── unit: oversize detection ──────────────────────────────────

func TestLooksLikeOversizeError(t *testing.T) {
	oversize := []string{
		"embed: 413 Payload Too Large: ",
		"embed: 400 Bad Request: This model's maximum context length is 8192 tokens",
		"input is too long for this model",
		"embed: provider error: max_tokens exceeded",
		"request exceeds the token limit",
		"input length 9000 is greater than the limit",
		"embed: 400 Bad Request: payload too large",
	}
	for _, msg := range oversize {
		if !looksLikeOversizeError(errors.New(msg)) {
			t.Errorf("looksLikeOversizeError(%q) = false, want true", msg)
		}
	}
	benign := []string{
		"embed: 401 Unauthorized: bad api key",
		"embed: 429 Too Many Requests: slow down",
		"embed: dial tcp: connection refused",
		"embed: provider returned 512 dims, want 1024",
	}
	for _, msg := range benign {
		if looksLikeOversizeError(errors.New(msg)) {
			t.Errorf("looksLikeOversizeError(%q) = true, want false", msg)
		}
	}
	if looksLikeOversizeError(nil) {
		t.Errorf("looksLikeOversizeError(nil) = true, want false")
	}
}

// ─── unit: halving retry ───────────────────────────────────────

func testWorker(e *halveFakeEmbedder) *Worker {
	return &Worker{
		embedder: e,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestEmbedWithHalve_SucceedsAfterHalving(t *testing.T) {
	fake := &halveFakeEmbedder{maxRunes: 100, dim: 4}
	w := testWorker(fake)

	text := strings.Repeat("字", 400) // 400 runes → 200 → 100 (fits)
	v, err := w.embedWithHalve(t.Context(), uuid.New(), text)
	if err != nil {
		t.Fatalf("embedWithHalve: %v", err)
	}
	if len(v) != 4 {
		t.Fatalf("dim = %d, want 4", len(v))
	}
	want := []int{400, 200, 100}
	got := fake.seen()
	if len(got) != len(want) {
		t.Fatalf("attempt lengths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt lengths = %v, want %v", got, want)
		}
	}
}

func TestEmbedWithHalve_GivesUpAfterMaxRetries(t *testing.T) {
	fake := &halveFakeEmbedder{maxRunes: 10, dim: 4}
	w := testWorker(fake)

	text := strings.Repeat("x", 1000) // halving 4× still > 10
	_, err := w.embedWithHalve(t.Context(), uuid.New(), text)
	if err == nil {
		t.Fatal("expected error after exhausting halve retries")
	}
	// 1 initial + maxHalveRetries halved attempts, no more.
	if n := len(fake.seen()); n != 1+maxHalveRetries {
		t.Errorf("attempts = %d, want %d", n, 1+maxHalveRetries)
	}
}

// embedFunc adapts a closure to embed.Embedder for one-off fakes.
type embedFunc func(ctx context.Context, text string) ([]float32, error)

func (f embedFunc) Embed(ctx context.Context, text string) ([]float32, error) {
	return f(ctx, text)
}
func (f embedFunc) Dim() int      { return 4 }
func (f embedFunc) Model() string { return "func" }

func TestEmbedWithHalve_NonOversizeErrorNotRetried(t *testing.T) {
	calls := 0
	w := &Worker{
		embedder: embedFunc(func(_ context.Context, _ string) ([]float32, error) {
			calls++
			return nil, errors.New("embed: 401 Unauthorized: bad api key")
		}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := w.embedWithHalve(t.Context(), uuid.New(), strings.Repeat("x", 1000))
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want 1 (non-oversize errors are not retried)", calls)
	}
}

func TestEmbedWithHalve_StopsAtMinRunes(t *testing.T) {
	fake := &halveFakeEmbedder{maxRunes: 1, dim: 4}
	w := testWorker(fake)

	// 40 runes → halve to 20 → below minHalveRunes(32) threshold check on
	// next iteration stops further halving.
	_, err := w.embedWithHalve(t.Context(), uuid.New(), strings.Repeat("x", 40))
	if err == nil {
		t.Fatal("expected error")
	}
	if n := len(fake.seen()); n > 2 {
		t.Errorf("attempts = %d, want ≤ 2 (minHalveRunes floor)", n)
	}
}

// ─── integration: poison-pill bookkeeping (needs DATABASE_URL) ──

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
	var hasCol bool
	if err := p.QueryRow(context.Background(), `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		   WHERE table_schema = 'brain' AND table_name = 'wiki_chunks'
		     AND column_name = 'embed_attempts')`).Scan(&hasCol); err != nil || !hasCol {
		t.Skip("brain.wiki_chunks.embed_attempts not present; apply migrations/00008_embed_retry.sql first")
	}
	return p
}

// TestEmbedPass_PoisonPillGivesUp: a chunk whose embed always fails must
// accrue embed_attempts and stop being reclaimed once it hits
// MaxEmbedAttempts.
func TestEmbedPass_PoisonPillGivesUp(t *testing.T) {
	p := openDB(t)
	ctx := context.Background()

	// Seed project + page + one chunk row directly (blocks not needed —
	// block_id is nullable, and the rechunk pass only touches pages with
	// blocks, so it leaves our fixture alone).
	var pid, pageID, chunkID uuid.UUID
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		uuid.New(), "embed-poison-test-"+uuid.NewString()).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), `DELETE FROM brain.projects WHERE id = $1`, pid)
	})
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.pages (project_id, title) VALUES ($1, $2) RETURNING id`,
		pid, "poison page").Scan(&pageID); err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.wiki_chunks (project_id, page_id, ord, text)
		 VALUES ($1, $2, 0, $3) RETURNING id`,
		pid, pageID, "chunk that will never embed").Scan(&chunkID); err != nil {
		t.Fatalf("seed chunk: %v", err)
	}

	w := New(p, nil, chunks.New(p), alwaysFailEmbedder{dim: 1024}, Config{
		Interval: time.Hour, // never fires; we drive RunOnce manually
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	attempts := func() int {
		var n int
		if err := p.QueryRow(ctx,
			`SELECT embed_attempts FROM brain.wiki_chunks WHERE id = $1`,
			chunkID).Scan(&n); err != nil {
			t.Fatalf("read attempts: %v", err)
		}
		return n
	}

	// Each pass claims the chunk, fails, records the attempt.
	for i := 1; i <= chunks.MaxEmbedAttempts; i++ {
		if _, got := w.RunOnce(ctx); got != 0 {
			t.Fatalf("pass %d embedded %d chunks, want 0", i, got)
		}
		if got := attempts(); got != i {
			t.Fatalf("after pass %d embed_attempts = %d", i, got)
		}
	}

	// Next pass: chunk is a poison pill — no longer claimed, count frozen.
	if _, got := w.RunOnce(ctx); got != 0 {
		t.Fatalf("post-exhaustion pass embedded %d, want 0", got)
	}
	if got := attempts(); got != chunks.MaxEmbedAttempts {
		t.Errorf("attempts after exhaustion = %d, want %d (frozen)", got, chunks.MaxEmbedAttempts)
	}

	// Observability: the exhausted backlog counts exactly our fixture row
	// (other test rows in a shared DB could inflate this — scope by page).
	var exhausted int64
	if err := p.QueryRow(ctx,
		`SELECT count(*) FROM brain.wiki_chunks
		  WHERE page_id = $1 AND embedding IS NULL AND embed_attempts >= $2`,
		pageID, chunks.MaxEmbedAttempts).Scan(&exhausted); err != nil {
		t.Fatalf("count exhausted: %v", err)
	}
	if exhausted != 1 {
		t.Errorf("exhausted chunks for page = %d, want 1", exhausted)
	}
}

// TestEmbedPass_OversizeHalvedEndToEnd: with a provider that rejects long
// inputs, a too-long chunk still gets embedded via halving (no failure
// recorded).
func TestEmbedPass_OversizeHalvedEndToEnd(t *testing.T) {
	p := openDB(t)
	ctx := context.Background()

	var pid, pageID, chunkID uuid.UUID
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		uuid.New(), "embed-halve-test-"+uuid.NewString()).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), `DELETE FROM brain.projects WHERE id = $1`, pid)
	})
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.pages (project_id, title) VALUES ($1, $2) RETURNING id`,
		pid, "halve page").Scan(&pageID); err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if err := p.QueryRow(ctx,
		`INSERT INTO brain.wiki_chunks (project_id, page_id, ord, text)
		 VALUES ($1, $2, 0, $3) RETURNING id`,
		pid, pageID, strings.Repeat("long ", 400)).Scan(&chunkID); err != nil {
		t.Fatalf("seed chunk: %v", err)
	}

	fake := &halveFakeEmbedder{maxRunes: 500, dim: 1024}
	w := New(p, nil, chunks.New(p), fake, Config{
		Interval: time.Hour,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if _, got := w.RunOnce(ctx); got != 1 {
		t.Fatalf("embedded = %d, want 1 (via halving)", got)
	}
	var isNull bool
	if err := p.QueryRow(ctx,
		`SELECT embedding IS NULL FROM brain.wiki_chunks WHERE id = $1`,
		chunkID).Scan(&isNull); err != nil {
		t.Fatalf("read embedding: %v", err)
	}
	if isNull {
		t.Errorf("embedding still NULL after halving retry")
	}
	var attempts int
	if err := p.QueryRow(ctx,
		`SELECT embed_attempts FROM brain.wiki_chunks WHERE id = $1`,
		chunkID).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts != 0 {
		t.Errorf("embed_attempts = %d, want 0 (halving succeeded, no failure)", attempts)
	}
	if n := len(fake.seen()); n < 2 {
		t.Errorf("provider attempts = %d, want ≥ 2 (halved at least once)", n)
	}
}
