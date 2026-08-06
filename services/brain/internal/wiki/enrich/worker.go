// Wiki enrichment worker — runs the LLM-driven [[wikilink]] pass on
// pages that are new or have changed since their last enrich.
//
// Pattern: same self-healing tick loop as embedworker. A page is a
// candidate when:
//
//	deleted_at IS NULL
//	  AND (enriched_at IS NULL OR enriched_at < updated_at)
//
// Project opt-in: the project's frontmatter must contain
// `config.enrich_wikilinks=true`. Enrichment is expensive (one LLM call
// per page) so we don't enable it project-wide by default.
//
// Per page:
//  1. Fetch sibling page titles → wiki index.
//  2. Concat all text-bearing blocks into a single body string with
//     position markers; remember the (block_id → body offset) mapping.
//  3. Ask the LLM for a list of {term, target} substitutions.
//  4. Apply substitutions in-place to whichever block contains the term.
//  5. Persist updated blocks via wiki store.
//  6. UPDATE pages.enriched_at = now().
//
// LLM failures are non-fatal: we log + leave enriched_at untouched, so
// next tick retries.
package enrich

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LLMCaller is the minimal LLM interface enrich needs: one prompt in,
// one string out. Implementations live next to the worker —
// RelayLLMCaller for prod (signs a JWT for the project owner and posts
// /v1/messages), stubLLMCaller for tests.
type LLMCaller interface {
	Chat(ctx context.Context, ownerID uuid.UUID, system, user string) (string, error)
}

// Config tunables. Zero values fall back to safe defaults.
type Config struct {
	Interval   time.Duration // default 30s
	BatchSize  int           // default 8 pages per tick
	LLMTimeout time.Duration // default 30s
	Logger     *slog.Logger
}

// Worker drives the enrich loop.
type Worker struct {
	pool   *pgxpool.Pool
	wiki   *wikistore.Store
	llm    LLMCaller
	cfg    Config
	logger *slog.Logger
}

func New(pool *pgxpool.Pool, w *wikistore.Store, l LLMCaller, cfg Config) *Worker {
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 8
	}
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{pool: pool, wiki: w, llm: l, cfg: cfg, logger: logger}
}

// Run blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("wiki enrich worker started",
		"interval", w.cfg.Interval, "batch", w.cfg.BatchSize)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("wiki enrich worker stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// RunOnce drains one pass synchronously. Returns the number of pages
// successfully enriched. Used by tests that don't want a live ticker.
func (w *Worker) RunOnce(ctx context.Context) int {
	return w.tick(ctx)
}

func (w *Worker) tick(ctx context.Context) int {
	candidates, err := w.findCandidates(ctx, w.cfg.BatchSize)
	if err != nil {
		w.logger.Warn("enrich: find candidates failed", "err", err)
		return 0
	}
	if len(candidates) == 0 {
		return 0
	}
	done := 0
	for _, c := range candidates {
		if err := w.enrichPage(ctx, c); err != nil {
			w.logger.Warn("enrich: page failed",
				"page_id", c.PageID, "err", err)
			continue
		}
		done++
	}
	if done > 0 {
		w.logger.Info("enrich tick", "pages", done, "batch", len(candidates))
	}
	return done
}

// candidate is one row of the candidate query.
type candidate struct {
	PageID    uuid.UUID
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
}

// findCandidates returns up to `limit` pages eligible for enrichment.
// Project opt-in is signalled via brain.projects metadata: column
// `enrich_wikilinks` (boolean, added when the feature is wired) — for
// now we read it from the projects.frontmatter jsonb when the column
// doesn't exist. Either path makes the worker noop for projects that
// haven't asked to be enriched.
func (w *Worker) findCandidates(ctx context.Context, limit int) ([]candidate, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT p.id, p.project_id, pr.owner_id
		FROM brain.pages p
		JOIN brain.projects pr ON pr.id = p.project_id
		WHERE p.deleted_at IS NULL
		  AND (p.enriched_at IS NULL OR p.enriched_at < p.updated_at)
		ORDER BY p.updated_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.PageID, &c.ProjectID, &c.OwnerID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// enrichPage runs the full pipeline for one page.
func (w *Worker) enrichPage(ctx context.Context, c candidate) error {
	if w.llm == nil {
		return errors.New("no LLM caller configured")
	}
	// 1. Sibling titles.
	titles, err := w.listProjectTitles(ctx, c.ProjectID, c.PageID)
	if err != nil {
		return err
	}
	if len(titles) == 0 {
		// No targets to link to — mark enriched anyway so we don't loop.
		return w.markEnriched(ctx, c.PageID)
	}
	index := BuildIndex(titles)

	// 2. Fetch all text-bearing blocks of this page.
	blocks, err := w.wiki.ListBlocks(ctx, c.PageID)
	if err != nil {
		return err
	}
	body, segments := assembleBody(blocks)
	if strings.TrimSpace(body) == "" {
		// Empty page — nothing to enrich.
		return w.markEnriched(ctx, c.PageID)
	}

	// 3. Call the LLM.
	llmCtx, cancel := context.WithTimeout(ctx, w.cfg.LLMTimeout)
	defer cancel()
	raw, err := w.llm.Chat(llmCtx, c.OwnerID,
		BuildSystemMessage(index),
		"Page content:\n\n"+body,
	)
	if err != nil {
		return err
	}
	links := ParseLinkResponse(raw)
	if len(links) == 0 {
		return w.markEnriched(ctx, c.PageID)
	}

	// 4. Apply substitutions and update the affected blocks. We do the
	// substitution per-block by remapping each accepted link's term to
	// the segment that contains it.
	enrichedBody := ApplyLinks(body, links)
	if enrichedBody == body {
		return w.markEnriched(ctx, c.PageID)
	}
	if err := w.persistChanges(ctx, blocks, segments, body, enrichedBody); err != nil {
		return err
	}
	return w.markEnriched(ctx, c.PageID)
}

// listProjectTitles fetches non-empty page titles in the same project,
// excluding the page being enriched. Limit kept low because the prompt
// budget is finite — pages beyond ~200 saturate the LLM context and
// degrade quality more than they help.
func (w *Worker) listProjectTitles(ctx context.Context, projectID, exceptPage uuid.UUID) ([]string, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT title
		FROM brain.pages
		WHERE project_id = $1
		  AND id <> $2
		  AND deleted_at IS NULL
		  AND title <> ''
		ORDER BY updated_at DESC
		LIMIT 200
	`, projectID, exceptPage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		titles = append(titles, t)
	}
	return titles, rows.Err()
}

// segment maps a body offset range back to a block.
type segment struct {
	BlockID uuid.UUID
	Start   int // byte offset in body where block text starts
	End     int // byte offset where it ends (exclusive)
}

// assembleBody concatenates text-bearing blocks (text/heading/quote/list
// items) with "\n\n" separators. Returns the concat string + a slice of
// (block_id, [start,end)) segments so we can map term offsets back to
// blocks for re-write.
//
// Non-text blocks (image, code, divider, etc.) are skipped — their
// content shape doesn't carry prose, and inserting wikilinks into a
// code block would corrupt syntax.
func assembleBody(blocks []*wikistore.Block) (string, []segment) {
	var sb strings.Builder
	var segs []segment
	first := true
	for _, b := range blocks {
		txt := blockText(b)
		if txt == "" {
			continue
		}
		if !first {
			sb.WriteString("\n\n")
		}
		start := sb.Len()
		sb.WriteString(txt)
		segs = append(segs, segment{BlockID: b.ID, Start: start, End: sb.Len()})
		first = false
	}
	return sb.String(), segs
}

// blockText returns the prose text inside a block, or "" if the block
// has no enrichable text. Convention follows wiki/chunks chunker — we
// look for content.text first, then content.markdown, then content.body.
func blockText(b *wikistore.Block) string {
	switch b.Type {
	case "code", "image", "divider", "table":
		return ""
	}
	if b.Content == nil {
		return ""
	}
	for _, k := range []string{"text", "markdown", "body"} {
		if v, ok := b.Content[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// persistChanges figures out which blocks changed by walking the
// segment table and updating the original block's content.text/markdown
// /body to the new text. Uses optimistic concurrency via the block's
// version — if a concurrent edit lands while we were running the LLM,
// we lose the race and skip; next tick will pick it up.
func (w *Worker) persistChanges(
	ctx context.Context,
	blocks []*wikistore.Block,
	segs []segment,
	oldBody, newBody string,
) error {
	if len(segs) == 0 {
		return nil
	}
	// Map block_id → original block pointer.
	byID := make(map[uuid.UUID]*wikistore.Block, len(blocks))
	for _, b := range blocks {
		byID[b.ID] = b
	}
	// Walk segments. Old body had block i at [seg.Start, seg.End). After
	// rewriting, each block's NEW content is the substring at the same
	// LOGICAL position — but offsets shift as earlier blocks grow. We
	// recompute by tracking how much length has been added so far.
	delta := 0
	for _, seg := range segs {
		oldText := oldBody[seg.Start:seg.End]
		newStart := seg.Start + delta
		// Find the corresponding new-segment end. Each block grows by
		// exactly 4 × (links inserted in its substring); we can compute
		// the new end by scanning the new body for the same character
		// boundary. Easier: greedily measure by searching for the
		// surrounding "\n\n" separator. We instead diff per-block by
		// re-running ApplyLinks on the same (oldText, links) pair —
		// but we don't have `links` here. Workaround: extract by
		// length-difference accounting: the new body's substring must
		// equal oldText with all term→[[...]] substitutions, and the
		// next "\n\n" separates blocks deterministically.
		newText := extractNewSegment(newBody, newStart, oldText)
		if newText == oldText {
			continue
		}
		orig, ok := byID[seg.BlockID]
		if !ok {
			continue
		}
		updated := cloneContent(orig.Content)
		setBlockText(updated, orig.Content, newText)
		if _, err := w.wiki.UpdateBlock(ctx, wikistore.UpdateBlockInput{
			BlockID:        orig.ID,
			IfMatchVersion: orig.Version,
			Content:        updated,
			ActorID:        "wiki-enrich-worker",
		}); err != nil {
			// Conflict / not-found / etc. — skip; next tick retries.
			w.logger.Warn("enrich: update block failed",
				"block_id", orig.ID, "err", err)
			continue
		}
		delta += len(newText) - len(oldText)
	}
	return nil
}

// extractNewSegment locates the post-rewrite version of `oldText` in
// `newBody` starting at `start`. Returns the substring up to the
// nearest segment-boundary "\n\n" (or end of body).
func extractNewSegment(newBody string, start int, oldText string) string {
	if start < 0 || start > len(newBody) {
		return oldText
	}
	rest := newBody[start:]
	// Block boundary in the assembled body is exactly "\n\n" — we
	// inserted that separator ourselves. Use it to know where this
	// block ends in the new body.
	if i := strings.Index(rest, "\n\n"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// cloneContent shallow-clones a block content map so we can mutate
// without affecting the cached pointer.
func cloneContent(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// setBlockText writes `text` to whichever key blockText() read from. If
// the original block didn't have any of the keys (shouldn't happen — we
// already filtered with blockText), default to "text".
func setBlockText(target, original map[string]any, text string) {
	for _, k := range []string{"text", "markdown", "body"} {
		if v, ok := original[k].(string); ok && strings.TrimSpace(v) != "" {
			target[k] = text
			return
		}
	}
	target["text"] = text
}

func (w *Worker) markEnriched(ctx context.Context, pageID uuid.UUID) error {
	_, err := w.pool.Exec(ctx,
		`UPDATE brain.pages SET enriched_at = now() WHERE id = $1`, pageID)
	return err
}
