// Vision-caption worker.
//
// Tick loop:
//
//  1. SELECT blocks WHERE deleted_at IS NULL AND
//     (captioned_at IS NULL OR captioned_at < updated_at)
//     ORDER BY updated_at ASC LIMIT N.
//  2. For each block:
//     - parse `![alt](url)` refs out of content.text
//     - drop ones whose alt is real → no work needed
//     - for each remaining URL: cache lookup; on miss fetch image
//     bytes, call vision LLM, write cache row
//     - apply captions back into content.text via ApplyCaptions
//     - UpdateBlock with IfMatchVersion (lose-the-race-skip)
//     - UPDATE captioned_at = now() either way (so noop blocks
//     with no images don't get re-scanned every tick)
//
// All failures are logged and skipped — next tick retries. Cache
// inserts use ON CONFLICT DO NOTHING so concurrent workers can't
// trip the PK.
package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// jsonUnmarshal is a tiny shim so worker.go doesn't grow a json import
// beyond this single use.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Caller is the minimal vision-LLM interface the worker needs.
//
//	ownerID: the project's owner — brain mints a JWT for them so model-relay
//	  resolves their BYOK / pool credentials.
//	imageBytes: raw bytes of the image (caller already fetched).
//	mediaType: MIME type ("image/png", "image/jpeg", ...).
type Caller interface {
	Caption(ctx context.Context, ownerID uuid.UUID, imageBytes []byte, mediaType string) (string, error)
}

// Config tunables. Zero values fall back to safe defaults.
type Config struct {
	Interval     time.Duration // default 60s
	BatchSize    int           // default 8 blocks per tick
	LLMTimeout   time.Duration // default 60s (vision is slow)
	FetchTimeout time.Duration // default 15s
	MaxImageMB   int           // default 5 MB; bigger images skipped
	Logger       *slog.Logger
	// HTTP is the client used to fetch image bytes. Tests inject a
	// stub; production gets http.DefaultClient with FetchTimeout
	// applied per-request.
	HTTP *http.Client
}

type Worker struct {
	pool   *pgxpool.Pool
	wiki   *wikistore.Store
	caller Caller
	cfg    Config
	logger *slog.Logger
	hc     *http.Client
}

func New(pool *pgxpool.Pool, w *wikistore.Store, c Caller, cfg Config) *Worker {
	if cfg.Interval == 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 8
	}
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 60 * time.Second
	}
	if cfg.FetchTimeout == 0 {
		cfg.FetchTimeout = 15 * time.Second
	}
	if cfg.MaxImageMB == 0 {
		cfg.MaxImageMB = 5
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: cfg.FetchTimeout}
	}
	return &Worker{pool: pool, wiki: w, caller: c, cfg: cfg, logger: logger, hc: hc}
}

func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("vision caption worker started",
		"interval", w.cfg.Interval, "batch", w.cfg.BatchSize)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("vision caption worker stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// RunOnce drains one pass synchronously. Returns the number of blocks
// successfully processed (with or without rewriting; just means the
// worker reached the end of the tick for that block).
func (w *Worker) RunOnce(ctx context.Context) int {
	return w.tick(ctx)
}

type candidate struct {
	BlockID   uuid.UUID
	PageID    uuid.UUID
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Text      string
	Version   int
}

func (w *Worker) tick(ctx context.Context) int {
	cands, err := w.findCandidates(ctx, w.cfg.BatchSize)
	if err != nil {
		w.logger.Warn("vision: find candidates failed", "err", err)
		return 0
	}
	if len(cands) == 0 {
		return 0
	}
	done := 0
	for _, c := range cands {
		if err := w.processBlock(ctx, c); err != nil {
			w.logger.Warn("vision: block failed",
				"block_id", c.BlockID, "err", err)
			continue
		}
		done++
	}
	if done > 0 {
		w.logger.Info("vision tick", "blocks", done, "batch", len(cands))
	}
	return done
}

// findCandidates pulls blocks needing caption work, joining onto pages
// and projects so we have ownerID handy for the LLM JWT mint.
func (w *Worker) findCandidates(ctx context.Context, limit int) ([]candidate, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT b.id, b.page_id, p.project_id, pr.owner_id,
		       COALESCE(b.content->>'text', ''), b.version
		FROM brain.blocks b
		JOIN brain.pages    p  ON p.id  = b.page_id
		JOIN brain.projects pr ON pr.id = p.project_id
		WHERE b.deleted_at IS NULL
		  AND (b.captioned_at IS NULL OR b.captioned_at < b.updated_at)
		  AND b.content->>'text' LIKE '%![%'
		ORDER BY b.updated_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.BlockID, &c.PageID, &c.ProjectID, &c.OwnerID,
			&c.Text, &c.Version); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (w *Worker) processBlock(ctx context.Context, c candidate) error {
	refs := FindImages(c.Text)
	// Filter to URLs whose alt is a placeholder — those are the only
	// ones we'd substitute. Order by first-occurrence; dedupe URL.
	urls := make([]string, 0, len(refs))
	seen := map[string]struct{}{}
	for _, r := range refs {
		if !NeedsCaption(r.Alt) {
			continue
		}
		if _, dup := seen[r.URL]; dup {
			continue
		}
		seen[r.URL] = struct{}{}
		urls = append(urls, r.URL)
	}
	if len(urls) == 0 {
		// Block has no work; mark captioned_at so we don't re-scan.
		return w.markCaptioned(ctx, c.BlockID)
	}

	// Build the caption map. Cache hits are cheap; misses each cost a
	// network round-trip + a vision LLM call.
	captions := make(map[string]string, len(urls))
	for _, u := range urls {
		cap, err := w.captionForURL(ctx, c.OwnerID, u)
		if err != nil {
			// Skip this URL but keep going — partial enrichment is
			// strictly better than skipping the whole block.
			w.logger.Warn("vision: caption failed",
				"url", u, "err", err)
			continue
		}
		if cap != "" {
			captions[u] = cap
		}
	}
	if len(captions) == 0 {
		return w.markCaptioned(ctx, c.BlockID)
	}
	newText, changed := ApplyCaptions(c.Text, captions)
	if !changed {
		return w.markCaptioned(ctx, c.BlockID)
	}
	// Re-fetch the block to know its current Content shape (we only
	// stored .text in `c`; preserve everything else like .url / .lang).
	curContent, curVersion, err := w.fetchBlockContent(ctx, c.BlockID)
	if err != nil {
		return err
	}
	if curVersion != c.Version {
		// Concurrent edit — leave the block alone, next tick retries.
		return nil
	}
	updated := cloneContent(curContent)
	updated["text"] = newText
	if _, err := w.wiki.UpdateBlock(ctx, wikistore.UpdateBlockInput{
		BlockID:        c.BlockID,
		IfMatchVersion: curVersion,
		Content:        updated,
		ActorID:        "vision-caption-worker",
	}); err != nil {
		return err
	}
	return w.markCaptioned(ctx, c.BlockID)
}

// fetchBlockContent reads the current jsonb content + version of a block.
// We don't expose a Store.GetBlock helper because UpdateBlock already
// re-reads inside its tx; the worker just needs a snapshot for the
// concurrency guard.
func (w *Worker) fetchBlockContent(ctx context.Context, id uuid.UUID) (map[string]any, int, error) {
	var version int
	var raw []byte
	err := w.pool.QueryRow(ctx, `
		SELECT content, version FROM brain.blocks
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&raw, &version)
	if err != nil {
		return nil, 0, err
	}
	var content map[string]any
	if err := jsonUnmarshal(raw, &content); err != nil {
		return nil, 0, err
	}
	if content == nil {
		content = map[string]any{}
	}
	return content, version, nil
}

// captionForURL returns the caption for `url` — cache lookup first,
// vision LLM on miss. Cache writes use ON CONFLICT DO NOTHING so the
// same URL processed by two concurrent workers won't trip the PK.
func (w *Worker) captionForURL(ctx context.Context, ownerID uuid.UUID, url string) (string, error) {
	hash := HashURL(url)
	var cached string
	err := w.pool.QueryRow(ctx, `
		SELECT caption FROM brain.image_captions WHERE url_hash = $1
	`, hash).Scan(&cached)
	if err == nil && strings.TrimSpace(cached) != "" {
		return cached, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("cache lookup: %w", err)
	}
	// Miss → fetch + caption.
	imgBytes, mediaType, err := w.fetchImage(ctx, url)
	if err != nil {
		return "", err
	}
	llmCtx, cancel := context.WithTimeout(ctx, w.cfg.LLMTimeout)
	defer cancel()
	caption, err := w.caller.Caption(llmCtx, ownerID, imgBytes, mediaType)
	if err != nil {
		return "", err
	}
	caption = strings.TrimSpace(caption)
	if caption == "" {
		return "", errors.New("empty caption")
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO brain.image_captions (url_hash, url, caption, model)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (url_hash) DO NOTHING
	`, hash, url, caption, "vision"); err != nil {
		// Cache write failure is non-fatal — we still got a caption.
		w.logger.Warn("vision: cache insert failed", "url", url, "err", err)
	}
	return caption, nil
}

// fetchImage GETs the URL. Hard caps at MaxImageMB; bigger images are
// rejected rather than streamed because (a) the vision LLM has its own
// limit and (b) a 50MB poster image isn't a knowledge artefact.
func (w *Worker) fetchImage(ctx context.Context, url string) ([]byte, string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, "", fmt.Errorf("unsupported scheme: %s", url)
	}
	fctx, cancel := context.WithTimeout(ctx, w.cfg.FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "biumind-vision-worker/1.0")
	resp, err := w.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("fetch %d", resp.StatusCode)
	}
	max := int64(w.cfg.MaxImageMB) * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > max {
		return nil, "", fmt.Errorf("image > %dMB", w.cfg.MaxImageMB)
	}
	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "image/png"
	}
	// Strip charset / boundary if present.
	if i := strings.Index(mediaType, ";"); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return body, mediaType, nil
}

func (w *Worker) markCaptioned(ctx context.Context, blockID uuid.UUID) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE brain.blocks SET captioned_at = now() WHERE id = $1
	`, blockID)
	return err
}

// cloneContent shallow-copies a block content map.
func cloneContent(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// EncodeBase64 is a small wrapper used by the model-relay caller — kept here
// because it's the only encoding the worker needs and avoids dragging
// the encoding/base64 import into relay_caller.go's smaller surface.
func EncodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
