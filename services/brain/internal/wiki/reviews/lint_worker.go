// Periodic lint worker.
//
// Per tick: list every project, fetch its live pages + blocks, run
// LintAll, write findings via the same Upsert path the dedup worker
// uses. Per-project failures are logged and the loop moves on.
//
// Cadence default: 12h. Lint findings change slowly (rules are
// deterministic; pages don't churn that fast on most users), and a
// noisy queue trains the user to ignore it. Operators with active
// curation workflows can dial to 1h via env.
//
// Coexistence with dedup worker (worker.go): they're independent
// goroutines reading from the same review_items table. dedup writes
// kind=dedup, lint writes kind=lint — UNIQUE on dedupe_key keeps
// them collision-free. Listing in the UI / MCP filters by kind.
package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LintWorkerConfig struct {
	Interval          time.Duration // default 12h; min 5m
	MaxOpenPerProject int           // skip projects with bigger queues; default 200
	PerProjectTimeout time.Duration // default 60s
	// MaxBlocksPerPage caps how many blocks we feed each rule per page.
	// 1000 is well past any realistic wiki page; the cap is a safety
	// belt against runaway content from a misbehaving ingest worker.
	MaxBlocksPerPage int
	// Filter is the optional LLM precision filter (P2-tail-3). nil ⇒
	// NoopFilter (rules-only behaviour identical to pre-filter).
	Filter LLMFilter
	Logger *slog.Logger
}

type LintWorker struct {
	pool   *pgxpool.Pool
	store  *Store
	cfg    LintWorkerConfig
	filter LLMFilter
	logger *slog.Logger
}

func NewLintWorker(pool *pgxpool.Pool, store *Store, cfg LintWorkerConfig) *LintWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 12 * time.Hour
	}
	if cfg.Interval < 5*time.Minute {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.MaxOpenPerProject <= 0 {
		cfg.MaxOpenPerProject = 200
	}
	if cfg.PerProjectTimeout <= 0 {
		cfg.PerProjectTimeout = 60 * time.Second
	}
	if cfg.MaxBlocksPerPage <= 0 {
		cfg.MaxBlocksPerPage = 1000
	}
	filter := cfg.Filter
	if filter == nil {
		filter = NoopFilter{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &LintWorker{pool: pool, store: store, cfg: cfg, filter: filter, logger: logger}
}

func (w *LintWorker) Run(ctx context.Context) {
	w.logger.Info("lint worker started",
		"interval", w.cfg.Interval,
		"max_open_per_project", w.cfg.MaxOpenPerProject)

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("lint worker stopped")
			return
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce drains a single tick. Test entry point; the tick loop just
// schedules calls.
func (w *LintWorker) RunOnce(ctx context.Context) {
	projects, err := w.candidateProjects(ctx)
	if err != nil {
		w.logger.Warn("lint: project query failed", "err", err)
		return
	}
	if len(projects) == 0 {
		return
	}
	totalCreated := 0
	for _, p := range projects {
		open, cerr := w.store.CountOpen(ctx, p.id)
		if cerr != nil {
			w.logger.Warn("lint: count open failed",
				"project_id", p.id, "err", cerr)
			continue
		}
		if open >= w.cfg.MaxOpenPerProject {
			continue
		}
		totalCreated += w.scanProject(ctx, p)
	}
	if totalCreated > 0 {
		w.logger.Info("lint tick",
			"projects", len(projects), "new_findings", totalCreated)
	}
}

type lintProject struct {
	id      uuid.UUID
	ownerID uuid.UUID
}

// candidateProjects returns every project that has at least one live
// page. We accept the false-positive of scanning empty projects (one
// page → 1 lint finding worst-case) over running an EXISTS subquery
// per project.
func (w *LintWorker) candidateProjects(ctx context.Context) ([]lintProject, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT id, owner_id
		  FROM brain.projects
		 WHERE EXISTS (
		   SELECT 1 FROM brain.pages p
		    WHERE p.project_id = brain.projects.id
		      AND p.deleted_at IS NULL
		 )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lintProject
	for rows.Next() {
		var p lintProject
		if err := rows.Scan(&p.id, &p.ownerID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (w *LintWorker) scanProject(ctx context.Context, p lintProject) int {
	pctx, cancel := context.WithTimeout(ctx, w.cfg.PerProjectTimeout)
	defer cancel()

	titles, groups, err := w.fetchPageTitles(pctx, p.id)
	if err != nil {
		w.logger.Warn("lint: title fetch failed",
			"project_id", p.id, "err", err)
		return 0
	}
	views, err := w.fetchPagesWithBlocks(pctx, p.id, w.cfg.MaxBlocksPerPage)
	if err != nil {
		w.logger.Warn("lint: page fetch failed",
			"project_id", p.id, "err", err)
		return 0
	}
	incoming := buildIncomingLinkTitles(views)

	created := 0
	for _, view := range views {
		findings := LintAll(LintInput{
			Page:               view.page,
			Blocks:             view.blocks,
			KnownPageTitles:    titles,
			TitleGroups:        groups,
			IncomingLinkTitles: incoming,
		})
		// LLM precision filter (P2-tail-3). Drops "false-positive"
		// stub_page / orphaned_page findings the model considers
		// not actionable. Errors → keep all (recall over precision).
		if len(findings) > 0 {
			if filtered, ferr := w.filter.FilterFindings(pctx, p.ownerID, findings); ferr != nil {
				w.logger.Warn("lint: llm filter errored, keeping all",
					"project_id", p.id, "err", ferr)
			} else {
				findings = filtered
			}
		}
		for _, f := range findings {
			payload := f.Payload
			if payload == nil {
				payload = map[string]any{}
			}
			payload["rule_id"] = f.RuleID
			_, isNew, uerr := w.store.Upsert(pctx, UpsertInput{
				ProjectID:   p.id,
				OwnerID:     p.ownerID,
				Kind:        KindLint,
				Title:       f.Title,
				Description: f.Description,
				PageIDs:     []uuid.UUID{f.PageID},
				Payload:     payload,
				DedupeKey:   LintDedupeKey(f.PageID, f.RuleID, f.SubKey),
			})
			if uerr != nil {
				w.logger.Warn("lint: upsert failed",
					"project_id", p.id, "rule", f.RuleID, "err", uerr)
				continue
			}
			if isNew {
				created++
			}
		}
	}
	return created
}

// ScanProject runs one structural lint pass for a single project and
// returns the count of newly-created findings. Public so the reviews
// scan endpoint (POST /reviews/scan family=structural) can trigger an
// on-demand re-scan independent of the periodic worker cadence.
// PerProjectTimeout still applies.
func (w *LintWorker) ScanProject(ctx context.Context, projectID, ownerID uuid.UUID) (int, error) {
	return w.scanProject(ctx, lintProject{id: projectID, ownerID: ownerID}), nil
}

// fetchPageTitles loads { lowercase(title): {} } for every live page
// in the project (used by dead_wikilink) AND a title→pageIDs grouping
// (used by duplicate_title). One round trip; the two maps fall out of
// the same scan.
func (w *LintWorker) fetchPageTitles(
	ctx context.Context, projectID uuid.UUID,
) (map[string]struct{}, map[string][]uuid.UUID, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT id, title FROM brain.pages
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	titles := map[string]struct{}{}
	groups := map[string][]uuid.UUID{}
	for rows.Next() {
		var (
			id    uuid.UUID
			title string
		)
		if err := rows.Scan(&id, &title); err != nil {
			return nil, nil, err
		}
		key := strings.TrimSpace(strings.ToLower(title))
		if key == "" {
			continue
		}
		titles[key] = struct{}{}
		groups[key] = append(groups[key], id)
	}
	return titles, groups, rows.Err()
}

// buildIncomingLinkTitles scans every block's text for [[wikilink]]
// targets and returns the lowercased+trimmed set. Feeds orphan_page.
// Reuses wikilinkRE (same regex dead_wikilink applies per-page) so the
// "what counts as a wikilink" definition stays singular. Operates on
// the pages+blocks already fetched by fetchPagesWithBlocks — zero extra
// DB round trips.
func buildIncomingLinkTitles(views []lintPageView) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range views {
		for _, b := range v.blocks {
			for _, m := range wikilinkRE.FindAllStringSubmatch(b.Text, -1) {
				target := strings.TrimSpace(m[1])
				if target == "" {
					continue
				}
				out[strings.ToLower(target)] = struct{}{}
			}
		}
	}
	return out
}

type lintPageView struct {
	page   PageView
	blocks []BlockView
}

// fetchPagesWithBlocks pulls every live page in the project plus its
// live blocks (capped per-page). One round trip per project: pages
// first, then blocks in a single query keyed by page_id IN (...).
func (w *LintWorker) fetchPagesWithBlocks(
	ctx context.Context,
	projectID uuid.UUID,
	maxBlocksPerPage int,
) ([]lintPageView, error) {
	pageRows, err := w.pool.Query(ctx, `
		SELECT id, title, frontmatter
		  FROM brain.pages
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer pageRows.Close()

	views := map[uuid.UUID]*lintPageView{}
	var pageIDs []uuid.UUID
	for pageRows.Next() {
		var (
			id    uuid.UUID
			title string
			fmRaw []byte
		)
		if err := pageRows.Scan(&id, &title, &fmRaw); err != nil {
			return nil, err
		}
		var fm map[string]any
		if len(fmRaw) > 0 {
			_ = json.Unmarshal(fmRaw, &fm)
		}
		views[id] = &lintPageView{
			page: PageView{ID: id, Title: title, Frontmatter: fm},
		}
		pageIDs = append(pageIDs, id)
	}
	if err := pageRows.Err(); err != nil {
		return nil, err
	}
	if len(pageIDs) == 0 {
		return nil, nil
	}

	// Block fetch — one query, ORDER BY page + position so we can
	// truncate to maxBlocksPerPage in a single linear pass.
	blockRows, err := w.pool.Query(ctx, `
		SELECT id, page_id, type, content
		  FROM brain.blocks
		 WHERE page_id = ANY($1) AND deleted_at IS NULL
		 ORDER BY page_id, position
	`, pageIDs)
	if err != nil {
		return nil, err
	}
	defer blockRows.Close()

	for blockRows.Next() {
		var (
			id     uuid.UUID
			pageID uuid.UUID
			typ    string
			cRaw   []byte
		)
		if err := blockRows.Scan(&id, &pageID, &typ, &cRaw); err != nil {
			return nil, err
		}
		view := views[pageID]
		if view == nil {
			continue
		}
		if len(view.blocks) >= maxBlocksPerPage {
			continue
		}
		var content map[string]any
		if len(cRaw) > 0 {
			_ = json.Unmarshal(cRaw, &content)
		}
		view.blocks = append(view.blocks, BlockView{
			ID:      id,
			Type:    typ,
			Text:    stringField(content, "text"),
			Caption: stringField(content, "caption"),
		})
	}
	if err := blockRows.Err(); err != nil {
		return nil, err
	}

	out := make([]lintPageView, 0, len(views))
	for _, v := range views {
		out = append(out, *v)
	}
	return out, nil
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Diagnose returns a one-line summary string for /healthz / smoke
// tooling. Same shape as Worker.Diagnose for consistency.
func (w *LintWorker) Diagnose(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	projects, err := w.candidateProjects(pctx)
	if err != nil {
		return fmt.Sprintf("lint: query failed: %v", err)
	}
	return fmt.Sprintf("lint: %d candidate projects", len(projects))
}
