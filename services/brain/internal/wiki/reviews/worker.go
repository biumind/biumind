// Periodic dedup worker.
//
// On a tick: list every project with at least N embedded chunks, run
// FindDedupCandidates against each, write new pairs through Upsert.
// Per-project failures are logged and the next project is processed
// — one bad project mustn't poison the loop.
//
// Cadence default: 6h. Dedup is a low-urgency suggestion stream;
// running more often produces noise without giving the user time to
// act. Operators can override via config when running aggressive
// curation workflows.
//
// Skip rules:
//   * project with < 2 pages embedded → skip (no pairs possible)
//   * project with > MaxOpenPerProject open dedup reviews → skip
//     (queue is full; resolve some first)
//
// Coexistence with the wiki embed worker (services/brain/internal/wiki/
// embedworker): we run independently. The embed worker decides when
// chunks have embeddings; dedup only sees the result. A project mid-
// embed will produce no pairs because most chunks are still NULL —
// the next dedup tick after embedding completes catches them.
package reviews

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkerConfig struct {
	Interval           time.Duration // default 6h; min 5m
	DedupOpts          DedupOptions
	MaxOpenPerProject  int           // skip projects whose open dedup queue exceeds this; default 100
	PerProjectTimeout  time.Duration // per-project budget; default 30s
	// Filter is the optional LLM precision filter. nil ⇒ NoopFilter
	// (rule-only behaviour, identical to pre-P2-D-LLM).
	Filter LLMFilter
	Logger *slog.Logger
}

type Worker struct {
	pool   *pgxpool.Pool
	store  *Store
	cfg    WorkerConfig
	filter LLMFilter
	logger *slog.Logger
}

func NewWorker(pool *pgxpool.Pool, store *Store, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.Interval < 5*time.Minute {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.MaxOpenPerProject <= 0 {
		cfg.MaxOpenPerProject = 100
	}
	if cfg.PerProjectTimeout <= 0 {
		cfg.PerProjectTimeout = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	filter := cfg.Filter
	if filter == nil {
		filter = NoopFilter{}
	}
	return &Worker{pool: pool, store: store, cfg: cfg, filter: filter, logger: logger}
}

// Run blocks until ctx is cancelled. The first tick fires after the
// configured interval — startup tick would mass-rescan immediately
// after every deploy, which is more annoying than useful for a
// suggestion-only feature. Operators wanting an immediate run can
// invoke RunOnce.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("dedup worker started",
		"interval", w.cfg.Interval,
		"max_distance", w.cfg.DedupOpts.withDefaults().MaxDistance,
		"max_pairs_per_project", w.cfg.DedupOpts.withDefaults().MaxPairsPerProject)

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("dedup worker stopped")
			return
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce drains a single tick. Tests use this to advance state
// deterministically; the binary's reconcile-once admin endpoint can
// also call it for ad-hoc resync after a bulk import.
func (w *Worker) RunOnce(ctx context.Context) {
	projects, err := w.candidateProjects(ctx)
	if err != nil {
		w.logger.Warn("dedup: candidate query failed", "err", err)
		return
	}
	if len(projects) == 0 {
		return
	}
	totalCreated := 0
	totalSkipped := 0
	for _, p := range projects {
		open, err := w.store.CountOpen(ctx, p.id)
		if err != nil {
			w.logger.Warn("dedup: count open failed",
				"project_id", p.id, "err", err)
			continue
		}
		if open >= w.cfg.MaxOpenPerProject {
			w.logger.Debug("dedup: project queue full, skipping",
				"project_id", p.id, "open", open)
			continue
		}
		c, s := w.scanProject(ctx, p)
		totalCreated += c
		totalSkipped += s
	}
	if totalCreated > 0 || totalSkipped > 0 {
		w.logger.Info("dedup tick",
			"projects", len(projects),
			"created", totalCreated,
			"skipped_existing", totalSkipped)
	}
}

type candidateProject struct {
	id      uuid.UUID
	ownerID uuid.UUID
}

// candidateProjects lists projects with ≥2 pages that have embedded
// chunks. Filtering at the SQL level avoids paying for a no-op tick
// on a project that hasn't had ingest yet.
func (w *Worker) candidateProjects(ctx context.Context) ([]candidateProject, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT pr.id, pr.owner_id
		FROM brain.projects pr
		WHERE EXISTS (
		  SELECT 1
		  FROM (
		    SELECT DISTINCT page_id FROM brain.wiki_chunks
		    WHERE project_id = pr.id AND embedding IS NOT NULL
		  ) p
		  HAVING count(*) >= 2
		)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidateProject
	for rows.Next() {
		var p candidateProject
		if err := rows.Scan(&p.id, &p.ownerID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (w *Worker) scanProject(ctx context.Context, p candidateProject) (created, skipped int) {
	pctx, cancel := context.WithTimeout(ctx, w.cfg.PerProjectTimeout)
	defer cancel()
	pairs, err := FindDedupCandidates(pctx, w.pool, p.id, w.cfg.DedupOpts)
	if err != nil {
		w.logger.Warn("dedup: scan failed",
			"project_id", p.id, "err", err)
		return 0, 0
	}
	if len(pairs) == 0 {
		return 0, 0
	}

	// LLM precision filter — drops "merely related" pairs before they
	// hit the review queue. Errors degrade to passthrough so a model-relay
	// outage doesn't lose findings (recall over precision).
	beforeFilter := len(pairs)
	filtered, ferr := w.filter.FilterDedup(pctx, p.ownerID, pairs)
	if ferr != nil {
		w.logger.Warn("dedup: llm filter errored, keeping all",
			"project_id", p.id, "err", ferr)
		filtered = pairs
	}
	dropped := beforeFilter - len(filtered)

	created, skipped, err = WritePairs(pctx, w.store, p.id, p.ownerID, filtered, w.logger)
	if err != nil {
		w.logger.Warn("dedup: write failed",
			"project_id", p.id, "err", err)
	}
	if dropped > 0 {
		w.logger.Info("dedup: llm dropped pairs",
			"project_id", p.id, "kept", len(filtered),
			"dropped", dropped)
	}
	return created, skipped
}

// Diagnose returns a one-line summary string. Used by /healthz / smoke
// tooling to confirm the worker is making sense without diving into
// metrics. We don't expose internals; the format is intentionally
// machine-greppable but not parseable.
func (w *Worker) Diagnose(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	projects, err := w.candidateProjects(pctx)
	if err != nil {
		return fmt.Sprintf("dedup: query failed: %v", err)
	}
	return fmt.Sprintf("dedup: %d candidate projects", len(projects))
}
