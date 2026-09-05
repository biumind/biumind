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
	"fmt"
	"log/slog"
	"strings"
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
	// lastExhausted dedupes the poison-pill backlog log (single-goroutine
	// tick, no lock needed).
	lastExhausted int64
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
// UPDATE SKIP LOCKED, embed, write back in one tx.
//
// Embedding runs in provider batches (providerBatchSize texts per
// EmbedBatch call — one HTTP round trip instead of N). If the batch
// call fails (or returns a mismatched vector count) the group degrades
// to the per-chunk path, so nothing is silently dropped.
//
// Two anti-poison-pill guards (unchanged from the per-chunk era):
//
//  1. Oversize inputs ("input too long" / context-length rejections) are
//     retried at half length within the same per-chunk timeout — same idea
//     as reference/llm_wiki embedding.ts fetchEmbedding, rewritten here.
//  2. Every failure increments wiki_chunks.embed_attempts (committed in
//     the same tx); chunks that reach chunks.MaxEmbedAttempts stop being
//     reclaimed, so one bad chunk can't be retried forever every tick.
func (w *Worker) embedPass(ctx context.Context) int {
	pending, tx, err := w.chunks.ClaimUnembedded(ctx, w.cfg.EmbedBatch)
	if err != nil {
		w.logger.Warn("wiki embed: claim failed", "err", err)
		return 0
	}
	if len(pending) == 0 {
		_ = tx.Rollback(ctx)
		w.logExhausted(ctx)
		return 0
	}

	vecs := make(map[uuid.UUID][]float32, len(pending))
	var failed []uuid.UUID
	for start := 0; start < len(pending); start += providerBatchSize {
		end := start + providerBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		w.embedGroup(ctx, pending[start:end], vecs, &failed)
	}
	if len(vecs) == 0 && len(failed) == 0 {
		_ = tx.Rollback(ctx)
		return 0
	}
	// Failure bookkeeping rides the same tx so a crash mid-batch doesn't
	// lose attempt counts (the rows stay locked until commit).
	if err := w.chunks.MarkEmbedFailures(ctx, tx, failed); err != nil {
		w.logger.Warn("wiki embed: mark failures failed", "err", err)
		_ = tx.Rollback(ctx)
		return 0
	}
	if err := w.chunks.SetEmbeddings(ctx, tx, vecs); err != nil {
		w.logger.Warn("wiki embed: commit failed", "err", err)
		return 0
	}
	w.logger.Info("wiki embed batch",
		"claimed", len(pending), "embedded", len(vecs), "failed", len(failed))
	return len(vecs)
}

// providerBatchSize is the number of chunk texts per EmbedBatch call.
// 32 aligns with common provider practice (OpenAI accepts up to 2048
// array elements; self-hosted TEI/vLLM comfortably handle 32) and keeps
// one call's payload + latency inside the per-batch EmbedTO budget.
const providerBatchSize = 32

// embedGroup embeds one provider batch into vecs/failed. Tries one
// EmbedBatch call; on error or a mismatched vector count degrades to
// per-chunk embedWithHalve (which also handles oversize halving). The
// batch call shares the per-chunk EmbedTO budget — it's one HTTP round
// trip, and a slow response falls back to the per-chunk path anyway.
func (w *Worker) embedGroup(
	ctx context.Context,
	group []chunks.Pending,
	vecs map[uuid.UUID][]float32,
	failed *[]uuid.UUID,
) {
	texts := make([]string, len(group))
	for i, p := range group {
		texts[i] = p.Text
	}
	ec, cancel := context.WithTimeout(ctx, w.cfg.EmbedTO)
	batchVecs, err := w.embedder.EmbedBatch(ec, texts)
	cancel()
	if err == nil && len(batchVecs) != len(group) {
		err = fmt.Errorf("embed: batch returned %d vectors for %d texts", len(batchVecs), len(group))
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.logger.Warn("wiki embed: batch call failed, falling back to per-chunk",
				"batch", len(group), "err", err)
		}
		w.embedGroupSingles(ctx, group, vecs, failed)
		return
	}
	for i, p := range group {
		v := batchVecs[i]
		if len(v) != w.embedder.Dim() {
			w.logger.Warn("wiki embed: bad dim",
				"chunk_id", p.ID, "got", len(v), "want", w.embedder.Dim())
			w.failChunk(p, failed)
			continue
		}
		vecs[p.ID] = v
	}
}

// embedGroupSingles is the per-chunk degradation path: one Embed call
// per chunk (with oversize halving), used when the batch call fails.
func (w *Worker) embedGroupSingles(
	ctx context.Context,
	group []chunks.Pending,
	vecs map[uuid.UUID][]float32,
	failed *[]uuid.UUID,
) {
	for _, p := range group {
		ec, cancel := context.WithTimeout(ctx, w.cfg.EmbedTO)
		v, err := w.embedWithHalve(ec, p.ID, p.Text)
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				w.logger.Warn("wiki embed: provider error",
					"chunk_id", p.ID, "err", err)
			}
			w.failChunk(p, failed)
			continue
		}
		if len(v) != w.embedder.Dim() {
			w.logger.Warn("wiki embed: bad dim",
				"chunk_id", p.ID, "got", len(v), "want", w.embedder.Dim())
			w.failChunk(p, failed)
			continue
		}
		vecs[p.ID] = v
	}
}

// failChunk records a chunk as failed for this pass and surfaces the
// poison-pill transition when it reaches MaxEmbedAttempts.
func (w *Worker) failChunk(p chunks.Pending, failed *[]uuid.UUID) {
	*failed = append(*failed, p.ID)
	if p.Attempts+1 >= chunks.MaxEmbedAttempts {
		w.logger.Warn("wiki embed: chunk exhausted retries, giving up (poison pill)",
			"chunk_id", p.ID, "attempts", p.Attempts+1)
	}
}

// maxHalveRetries caps how many times one oversize input is halved before
// giving up (4 halvings = down to 1/16 of the original text).
const maxHalveRetries = 4

// minHalveRunes stops halving once the input is down to a stub — if a
// provider still rejects ~30 runes as "too long" the problem is not the
// input size.
const minHalveRunes = 32

// embedWithHalve embeds text; on an oversize rejection it retries with
// the rune-halved text up to maxHalveRetries times. The returned vector
// represents the (possibly truncated) text that actually got through —
// a safety net, not the main line of defence (chunker config should keep
// chunks under the provider limit).
func (w *Worker) embedWithHalve(ctx context.Context, chunkID uuid.UUID, text string) ([]float32, error) {
	v, err := w.embedder.Embed(ctx, text)
	for i := 0; i < maxHalveRetries && err != nil && looksLikeOversizeError(err); i++ {
		runes := []rune(text)
		if len(runes) <= minHalveRunes {
			break
		}
		text = string(runes[:len(runes)/2])
		w.logger.Info("wiki embed: oversize input, retrying at half length",
			"chunk_id", chunkID, "runes", len(runes)/2)
		v, err = w.embedder.Embed(ctx, text)
	}
	return v, err
}

// looksLikeOversizeError heuristically matches "input too long / exceeds
// model context / payload too large" rejections from OpenAI-compatible
// providers (incl. HTTP 413 — the embed package folds status + body into
// the error string). Safer to over-match than under-match: a false
// positive just means a retry at half size, which fails the same way.
func looksLikeOversizeError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, sub := range []string{
		"413",
		"too long",
		"maximum context",
		"max_tokens",
		"max tokens",
		"context length",
		"token limit",
		"exceeds",
		"input length",
		"payload too large",
		"request too large",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// logExhausted surfaces the poison-pill backlog when the claim queue runs
// dry. Logged only when the count changes so a stuck backlog doesn't spam
// every tick.
func (w *Worker) logExhausted(ctx context.Context) {
	n, err := w.chunks.CountEmbedExhausted(ctx)
	if err != nil {
		return
	}
	if n == w.lastExhausted {
		return
	}
	w.lastExhausted = n
	if n > 0 {
		w.logger.Warn("wiki embed: poison-pill chunks skipped (retries exhausted)",
			"count", n, "max_attempts", chunks.MaxEmbedAttempts)
	}
}
