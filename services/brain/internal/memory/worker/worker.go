// Package worker runs the memory embedding backfill loop.
//
// On a poll tick the worker:
//
//  1. Claims up to `batch` un-embedded memory rows with FOR UPDATE SKIP
//     LOCKED so sibling replicas don't double-embed the same content.
//  2. Calls Embedder.Embed for each row's content. Per-row errors are
//     logged and the row is skipped — the next tick will retry.
//  3. Commits the produced vectors back into brain.memories.embedding,
//     releasing the row locks.
//
// The loop runs as a single goroutine started by Brain main. It exits
// when the supplied context is cancelled. No retries beyond the next
// poll tick — if the embedder is sick, we want the queue to drain
// once it recovers, not hot-loop on a broken provider.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	"github.com/google/uuid"
)

type Config struct {
	Interval time.Duration // default 5s
	Batch    int           // default 32; cap on rows claimed per tick
	EmbedTO  time.Duration // per-row embed timeout; default 15s
	Logger   *slog.Logger
}

type Worker struct {
	store    *memstore.Store
	embedder embed.Embedder
	cfg      Config
	logger   *slog.Logger
}

func New(s *memstore.Store, e embed.Embedder, cfg Config) *Worker {
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Batch == 0 {
		cfg.Batch = 32
	}
	if cfg.EmbedTO == 0 {
		cfg.EmbedTO = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: s, embedder: e, cfg: cfg, logger: logger}
}

// Run blocks until ctx is cancelled. Errors during ticks are logged
// but never abort the loop; the worker is best-effort by design.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("memory embed worker started",
		"interval", w.cfg.Interval, "batch", w.cfg.Batch,
		"model", w.embedder.Model(), "dim", w.embedder.Dim())

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()

	// Run one tick immediately so a fresh process drains any backlog
	// without waiting for the first interval.
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory embed worker stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// RunOnce is exposed for tests so they can drain the queue
// deterministically without spinning a real ticker.
func (w *Worker) RunOnce(ctx context.Context) (processed int) {
	return w.tick(ctx)
}

func (w *Worker) tick(ctx context.Context) int {
	pending, tx, err := w.store.ClaimUnembedded(ctx, w.cfg.Batch)
	if err != nil {
		w.logger.Warn("memory embed: claim failed", "err", err)
		return 0
	}
	if len(pending) == 0 {
		// commit the empty tx so the snapshot doesn't leak.
		_ = tx.Rollback(ctx)
		return 0
	}
	w.logger.DebugContext(ctx, "memory embed: claimed",
		"batch_target", w.cfg.Batch, "pending", len(pending))

	vecs := make(map[uuid.UUID][]float32, len(pending))
	for _, p := range pending {
		ec, cancel := context.WithTimeout(ctx, w.cfg.EmbedTO)
		v, err := w.embedder.Embed(ec, p.Content)
		cancel()
		if err != nil {
			// Per-row failure: skip; next tick retries.
			if !errors.Is(err, context.Canceled) {
				w.logger.Warn("memory embed: provider error",
					"memory_id", p.ID, "err", err)
			}
			continue
		}
		if len(v) != w.embedder.Dim() {
			w.logger.Warn("memory embed: bad dim",
				"memory_id", p.ID, "got", len(v), "want", w.embedder.Dim())
			continue
		}
		vecs[p.ID] = v
	}

	if len(vecs) == 0 {
		_ = tx.Rollback(ctx)
		metrics.RecordEmbedBatch(0, len(pending))
		return 0
	}
	if err := w.store.SetEmbeddings(ctx, tx, vecs); err != nil {
		w.logger.Warn("memory embed: commit failed", "err", err)
		metrics.RecordEmbedBatch(0, len(pending))
		return 0
	}
	w.logger.Info("memory embed batch", "claimed", len(pending), "embedded", len(vecs))
	metrics.RecordEmbedBatch(len(vecs), len(pending)-len(vecs))
	// Refresh the pending gauge so dashboards see the backlog drain
	// in real-time rather than waiting for the next scrape interval.
	if pendingCount, err := w.store.CountUnembedded(ctx); err == nil {
		metrics.SetEmbedPending(pendingCount)
	}
	return len(vecs)
}
