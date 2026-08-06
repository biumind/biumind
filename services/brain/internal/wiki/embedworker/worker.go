// Package embedworker drives the wiki vector pipeline:
//
//	tick 1: find pages whose chunks are stale → rechunk + replace rows
//	tick 2: claim NULL-embedding chunks → embed → write back
//
// We split these into two phases of one tick (not two daemons) so a
// freshly-rechunked page becomes searchable in the same loop iteration —
// keeps the "edit a block, retrieve it within seconds" UX without
// requiring an event bus.
//
// "Stale" is defined purely by timestamps: a page is stale when ANY of
// its non-deleted blocks has updated_at > MAX(chunks.updated_at) for that
// page, or the page has blocks but zero chunks (first-touch case). This
// formulation is self-healing — restart the worker mid-flight and it
// rebuilds whatever was left half-done; no per-page bookkeeping table
// needed.
//
// Soft-deleted blocks get their updated_at bumped (see wiki/store.go's
// SoftDeleteBlock), so chunk removal is covered by the same query.
//
// Pattern matches services/brain/internal/memory/worker/worker.go and
// reuses biu/embed.Embedder so the same EMBED_PROVIDER configuration
// drives both subsystems.
package embedworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/services/brain/internal/wiki/chunks"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config tunables. Zero values fall back to safe defaults.
type Config struct {
	Interval     time.Duration // default 10s
	RechunkBatch int           // pages per rechunk pass; default 16
	EmbedBatch   int           // chunks per embed pass; default 32
	EmbedTO      time.Duration // per-row embed timeout; default 15s
	ChunkOpts    chunks.Options
	Logger       *slog.Logger
}

type Worker struct {
	pool     *pgxpool.Pool
	wiki     *wikistore.Store
	chunks   *chunks.Store
	embedder embed.Embedder
	cfg      Config
	logger   *slog.Logger
}

func New(pool *pgxpool.Pool, w *wikistore.Store, c *chunks.Store, e embed.Embedder, cfg Config) *Worker {
	if cfg.Interval == 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.RechunkBatch == 0 {
		cfg.RechunkBatch = 16
	}
	if cfg.EmbedBatch == 0 {
		cfg.EmbedBatch = 32
	}
	if cfg.EmbedTO == 0 {
		cfg.EmbedTO = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		pool: pool, wiki: w, chunks: c, embedder: e, cfg: cfg, logger: logger,
	}
}

// Run blocks until ctx is cancelled. The first tick fires immediately so
// a fresh process drains backlog without waiting for the interval.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("wiki embed worker started",
		"interval", w.cfg.Interval,
		"rechunk_batch", w.cfg.RechunkBatch,
		"embed_batch", w.cfg.EmbedBatch,
		"model", w.embedder.Model(), "dim", w.embedder.Dim())

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()

	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("wiki embed worker stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// RunOnce drains one rechunk + embed pass synchronously. Tests use this
// to assert behaviour without spinning a real ticker.
func (w *Worker) RunOnce(ctx context.Context) (rechunked, embedded int) {
	return w.tick(ctx)
}

func (w *Worker) tick(ctx context.Context) (int, int) {
	rechunked := w.rechunkPass(ctx)
	embedded := w.embedPass(ctx)
	return rechunked, embedded
}

// ─── Rechunk pass ──────────────────────────────────────────────

// rechunkPass finds up to RechunkBatch stale pages and rebuilds their
// chunks. Per-page failures are logged and skipped — next tick retries.
func (w *Worker) rechunkPass(ctx context.Context) int {
	pages, err := w.findStalePages(ctx, w.cfg.RechunkBatch)
	if err != nil {
		w.logger.Warn("wiki rechunk: find stale failed", "err", err)
		return 0
	}
	if len(pages) == 0 {
		return 0
	}
	done := 0
	for _, pid := range pages {
		if err := w.rechunkPage(ctx, pid); err != nil {
			w.logger.Warn("wiki rechunk: page failed",
				"page_id", pid, "err", err)
			continue
		}
		done++
	}
	if done > 0 {
		w.logger.Info("wiki rechunk batch", "pages", done)
	}
	return done
}

// findStalePages returns pages whose chunks are out of date relative to
// their blocks (or pages that have content but no chunks yet).
//
// Note: when a page is first created with no blocks (and no title or
// just a title), we don't enqueue it. Title-only chunks would only emit
// if the chunker sees an empty block list with a non-empty title; we
// treat this as a no-op for ranking — wiki BM25 already covers titles
// via brain.pages.tsv.
func (w *Worker) findStalePages(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT p.id FROM brain.pages p
		WHERE p.deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM brain.blocks b
		    WHERE b.page_id = p.id
		      AND b.deleted_at IS NULL
		  )
		  AND (
		    -- max block updated_at exceeds max chunk updated_at
		    COALESCE(
		      (SELECT max(b.updated_at) FROM brain.blocks b
		       WHERE b.page_id = p.id AND b.deleted_at IS NULL),
		      'epoch'::timestamptz)
		    >
		    COALESCE(
		      (SELECT max(c.updated_at) FROM brain.wiki_chunks c
		       WHERE c.page_id = p.id),
		      'epoch'::timestamptz)
		  )
		ORDER BY p.updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// rechunkPage rebuilds the chunk set for one page. Reads the page (for
// title) + blocks (for slices), runs the chunker, then ReplacePage.
func (w *Worker) rechunkPage(ctx context.Context, pageID uuid.UUID) error {
	page, err := w.wiki.GetPage(ctx, pageID)
	if err != nil {
		return err
	}
	blocks, err := w.wiki.ListBlocks(ctx, pageID)
	if err != nil {
		return err
	}
	slices := make([]chunks.BlockSlice, 0, len(blocks))
	for _, b := range blocks {
		text, caption, lang, level, items := extractBlockFields(b.Content)
		// Drop blocks with nothing to embed. Heading blocks carry text
		// (the section title) so they pass through — the chunker uses
		// them to advance the headingPath stack and emits no chunk itself.
		if text == "" && caption == "" && len(items) == 0 {
			continue
		}
		slices = append(slices, chunks.BlockSlice{
			BlockID: b.ID,
			Type:    b.Type,
			Text:    text,
			Caption: caption,
			Level:   level,
			Lang:    lang,
			Items:   items,
		})
	}
	out := chunks.ChunkPage(page.Title, slices, w.cfg.ChunkOpts)
	if _, err := w.chunks.ReplacePage(ctx, page.ProjectID, pageID, out); err != nil {
		return err
	}
	return nil
}

// extractBlockFields pulls the chunker-relevant fields from a block's
// content JSON. content is map[string]any after pgx unmarshal; numbers
// arrive as float64 (so level is read as float64 then truncated), and
// non-string values are silently skipped (the chunker treats empty text
// / items as "nothing to embed" anyway). The block's Type comes from the
// dedicated column (b.Type), not content, so the caller passes it in.
func extractBlockFields(content map[string]any) (text, caption, lang string, level int, items []string) {
	if v, ok := content["text"].(string); ok {
		text = v
	}
	if v, ok := content["caption"].(string); ok {
		caption = v
	}
	if v, ok := content["lang"].(string); ok {
		lang = v
	}
	if n, ok := content["level"].(float64); ok {
		level = int(n)
	}
	if arr, ok := content["items"].([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				items = append(items, s)
			}
		}
	}
	return
}

// ─── Embed pass ────────────────────────────────────────────────

// embedPass mirrors memory/worker/worker.go: claim a batch with FOR
// UPDATE SKIP LOCKED, embed each, write back in one tx.
func (w *Worker) embedPass(ctx context.Context) int {
	pending, tx, err := w.chunks.ClaimUnembedded(ctx, w.cfg.EmbedBatch)
	if err != nil {
		w.logger.Warn("wiki embed: claim failed", "err", err)
		return 0
	}
	if len(pending) == 0 {
		_ = tx.Rollback(ctx)
		return 0
	}

	vecs := make(map[uuid.UUID][]float32, len(pending))
	for _, p := range pending {
		ec, cancel := context.WithTimeout(ctx, w.cfg.EmbedTO)
		v, err := w.embedder.Embed(ec, p.Text)
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				w.logger.Warn("wiki embed: provider error",
					"chunk_id", p.ID, "err", err)
			}
			continue
		}
		if len(v) != w.embedder.Dim() {
			w.logger.Warn("wiki embed: bad dim",
				"chunk_id", p.ID, "got", len(v), "want", w.embedder.Dim())
			continue
		}
		vecs[p.ID] = v
	}
	if len(vecs) == 0 {
		_ = tx.Rollback(ctx)
		return 0
	}
	if err := w.chunks.SetEmbeddings(ctx, tx, vecs); err != nil {
		w.logger.Warn("wiki embed: commit failed", "err", err)
		return 0
	}
	w.logger.Info("wiki embed batch",
		"claimed", len(pending), "embedded", len(vecs))
	return len(vecs)
}
