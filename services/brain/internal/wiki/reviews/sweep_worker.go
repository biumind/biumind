// Periodic sweep worker.
//
// Per tick: list every project with live pages, build the title-graph
// (which titles are linked-to by which other pages), then run SweepAll
// per page and Upsert findings as kind=sweep review_items.
//
// Cadence default: 24h. Sweep findings change very slowly (a page
// being "stale" is a creeping condition, not a discrete event), so a
// daily tick stays well under the user's noise budget. The 24h
// default also matches the "morning audit" mental model — operators
// scheduling crons separately can disable in-process by setting
// SWEEP_INTERVAL_HOURS=0.
//
// Coexistence:
//   - dedup worker (P2-D-1)  kind=dedup
//   - lint worker  (P2-D-2)  kind=lint
//   - sweep worker (P2-D-3)  kind=sweep
//
// They share review_items but never fight: each kind has its own
// dedupe_key namespace and its own UI/MCP filter.
package reviews

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SweepWorkerConfig struct {
	Interval          time.Duration // default 24h; min 1h
	StaleAfterDays    int           // default 90
	OrphanAfterDays   int           // default 60
	MaxOpenPerProject int           // default 200
	PerProjectTimeout time.Duration // default 60s
	// Filter is the optional LLM precision filter (P2-tail-3). nil ⇒
	// NoopFilter (rules-only behaviour identical to pre-filter).
	Filter LLMFilter
	Logger *slog.Logger
}

type SweepWorker struct {
	pool   *pgxpool.Pool
	store  *Store
	cfg    SweepWorkerConfig
	filter LLMFilter
	logger *slog.Logger
}

func NewSweepWorker(pool *pgxpool.Pool, store *Store, cfg SweepWorkerConfig) *SweepWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.Interval < time.Hour {
		cfg.Interval = time.Hour
	}
	if cfg.StaleAfterDays <= 0 {
		cfg.StaleAfterDays = 90
	}
	if cfg.OrphanAfterDays <= 0 {
		cfg.OrphanAfterDays = 60
	}
	if cfg.MaxOpenPerProject <= 0 {
		cfg.MaxOpenPerProject = 200
	}
	if cfg.PerProjectTimeout <= 0 {
		cfg.PerProjectTimeout = 60 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	filter := cfg.Filter
	if filter == nil {
		filter = NoopFilter{}
	}
	return &SweepWorker{pool: pool, store: store, cfg: cfg, filter: filter, logger: logger}
}

func (w *SweepWorker) Run(ctx context.Context) {
	w.logger.Info("sweep worker started",
		"interval", w.cfg.Interval,
		"stale_after_days", w.cfg.StaleAfterDays,
		"orphan_after_days", w.cfg.OrphanAfterDays)

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("sweep worker stopped")
			return
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce scans every candidate project once. Test entry point.
func (w *SweepWorker) RunOnce(ctx context.Context) {
	projects, err := w.candidateProjects(ctx)
	if err != nil {
		w.logger.Warn("sweep: project query failed", "err", err)
		return
	}
	if len(projects) == 0 {
		return
	}
	now := time.Now()
	totalCreated := 0
	for _, p := range projects {
		open, cerr := w.store.CountOpen(ctx, p.id)
		if cerr != nil {
			w.logger.Warn("sweep: count open failed",
				"project_id", p.id, "err", cerr)
			continue
		}
		if open >= w.cfg.MaxOpenPerProject {
			continue
		}
		totalCreated += w.scanProject(ctx, p, now)
	}
	if totalCreated > 0 {
		w.logger.Info("sweep tick",
			"projects", len(projects), "new_findings", totalCreated)
	}
}

type sweepProject struct {
	id      uuid.UUID
	ownerID uuid.UUID
}

func (w *SweepWorker) candidateProjects(ctx context.Context) ([]sweepProject, error) {
	// Same shape as lint's candidate query — projects with at least one
	// live page. Nothing to sweep otherwise.
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
	var out []sweepProject
	for rows.Next() {
		var p sweepProject
		if err := rows.Scan(&p.id, &p.ownerID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (w *SweepWorker) scanProject(ctx context.Context, p sweepProject, now time.Time) int {
	pctx, cancel := context.WithTimeout(ctx, w.cfg.PerProjectTimeout)
	defer cancel()

	pages, err := w.fetchPages(pctx, p.id)
	if err != nil {
		w.logger.Warn("sweep: page fetch failed",
			"project_id", p.id, "err", err)
		return 0
	}
	if len(pages) == 0 {
		return 0
	}
	titleByID := make(map[uuid.UUID]string, len(pages))
	idByTitle := make(map[string]uuid.UUID, len(pages))
	for _, pg := range pages {
		t := strings.TrimSpace(strings.ToLower(pg.Title))
		if t == "" {
			continue
		}
		titleByID[pg.ID] = t
		// First seen wins on duplicate titles — orphan flag will
		// then under-count in the rare collision case, which is fine
		// (under-flagging beats wrongly clearing an orphan).
		if _, exists := idByTitle[t]; !exists {
			idByTitle[t] = pg.ID
		}
	}

	incoming, err := w.fetchIncomingLinkCounts(pctx, p.id, idByTitle)
	if err != nil {
		w.logger.Warn("sweep: link graph build failed",
			"project_id", p.id, "err", err)
		// We can still run stale_page (which doesn't need link counts)
		// — feed zeros so orphan over-flags briefly. Better than
		// skipping the whole tick.
		incoming = map[uuid.UUID]int{}
	}

	created := 0
	for _, pg := range pages {
		findings := SweepAll(SweepInput{
			Page: SweepPageView{
				ID: pg.ID, Title: pg.Title, UpdatedAt: pg.UpdatedAt,
			},
			IncomingLinks:   incoming[pg.ID],
			Now:             now,
			StaleAfterDays:  w.cfg.StaleAfterDays,
			OrphanAfterDays: w.cfg.OrphanAfterDays,
		})
		// LLM filter: orphaned_page benefits, stale_page passes through.
		if len(findings) > 0 {
			if filtered, ferr := w.filter.FilterFindings(pctx, p.ownerID, findings); ferr != nil {
				w.logger.Warn("sweep: llm filter errored, keeping all",
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
				Kind:        KindSweep,
				Title:       f.Title,
				Description: f.Description,
				PageIDs:     []uuid.UUID{f.PageID},
				Payload:     payload,
				DedupeKey:   SweepDedupeKey(f.PageID, f.RuleID),
			})
			if uerr != nil {
				w.logger.Warn("sweep: upsert failed",
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

type sweepPageRow struct {
	ID        uuid.UUID
	Title     string
	UpdatedAt time.Time
}

func (w *SweepWorker) fetchPages(ctx context.Context, projectID uuid.UUID) ([]sweepPageRow, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT id, title, updated_at
		  FROM brain.pages
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sweepPageRow
	for rows.Next() {
		var p sweepPageRow
		if err := rows.Scan(&p.ID, &p.Title, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// fetchIncomingLinkCounts builds the [target_page_id → count of distinct
// referencing pages] map by scanning every live block's text content
// for [[target]] mentions and resolving against `idByTitle`.
//
// We do the regex match in Go, not in Postgres, because:
//  1. Postgres regex over a jsonb column doesn't use any index — it's
//     a sequential scan either way.
//  2. The counted-set semantics (distinct referencing pages, not
//     total link count) is awkward in SQL with our jsonb shape and
//     easy in Go via a set per target.
//  3. Future rules will want richer graph data (e.g. who links to
//     whom) and the Go path positions us to add it incrementally.
func (w *SweepWorker) fetchIncomingLinkCounts(
	ctx context.Context,
	projectID uuid.UUID,
	idByTitle map[string]uuid.UUID,
) (map[uuid.UUID]int, error) {
	if len(idByTitle) == 0 {
		return map[uuid.UUID]int{}, nil
	}
	rows, err := w.pool.Query(ctx, `
		SELECT b.page_id, b.content->>'text', b.content->>'caption'
		  FROM brain.blocks b
		  JOIN brain.pages p ON p.id = b.page_id
		 WHERE p.project_id = $1
		   AND p.deleted_at IS NULL
		   AND b.deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// referencers[target_page_id] = set of source_page_id pages that
	// link to target. We count len(set) at the end, so two wikilinks
	// to the same target from one source page count as one referrer.
	referencers := map[uuid.UUID]map[uuid.UUID]struct{}{}
	for rows.Next() {
		var (
			sourcePage uuid.UUID
			text       *string
			caption    *string
		)
		if err := rows.Scan(&sourcePage, &text, &caption); err != nil {
			return nil, err
		}
		body := ""
		if text != nil {
			body = *text
		}
		if caption != nil {
			body += "\n" + *caption
		}
		if !strings.Contains(body, "[[") {
			continue
		}
		matches := wikilinkRE.FindAllStringSubmatch(body, -1)
		for _, m := range matches {
			target := strings.TrimSpace(strings.ToLower(m[1]))
			if target == "" {
				continue
			}
			targetID, ok := idByTitle[target]
			if !ok {
				continue // dead wikilink — lint covers it
			}
			if targetID == sourcePage {
				// Self-reference shouldn't count as an incoming link.
				continue
			}
			set, exists := referencers[targetID]
			if !exists {
				set = map[uuid.UUID]struct{}{}
				referencers[targetID] = set
			}
			set[sourcePage] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]int, len(referencers))
	for k, v := range referencers {
		out[k] = len(v)
	}
	return out, nil
}

// Diagnose returns a one-line summary string for /healthz / smoke tooling.
func (w *SweepWorker) Diagnose(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	projects, err := w.candidateProjects(pctx)
	if err != nil {
		return fmt.Sprintf("sweep: query failed: %v", err)
	}
	return fmt.Sprintf("sweep: %d candidate projects", len(projects))
}
