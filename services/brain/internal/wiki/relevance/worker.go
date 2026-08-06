// Periodic relevance worker.
//
// Per tick: list every project with ≥2 live pages, build the project
// graph from pages + blocks, run ScoreAll, ReplaceProject. Per-project
// failures log + skip; one bad project doesn't poison the loop.
//
// Cadence default: 6h. Relevance changes whenever blocks change, but
// re-scoring is cheap enough (one query + in-memory linear-algebra
// pass) that a 6h cadence keeps the data fresh without burning compute.
// Operators with high-churn projects can drop to 1h via env.
//
// Coexistence with dedup/lint/sweep workers: independent. Relevance
// writes a different table; the only shared resource is brain.pages
// + brain.blocks (read-only for all four workers).
package relevance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkerConfig struct {
	Interval          time.Duration // default 6h; min 5m
	ScoreOpts         ScoreOptions
	PerProjectTimeout time.Duration // default 60s
	Logger            *slog.Logger
}

type Worker struct {
	pool   *pgxpool.Pool
	store  *Store
	cfg    WorkerConfig
	logger *slog.Logger
}

func NewWorker(pool *pgxpool.Pool, store *Store, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.Interval < 5*time.Minute {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.PerProjectTimeout <= 0 {
		cfg.PerProjectTimeout = 60 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{pool: pool, store: store, cfg: cfg, logger: logger}
}

func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("relevance worker started",
		"interval", w.cfg.Interval,
		"min_score", w.cfg.ScoreOpts.withDefaults().MinScore,
		"max_neighbours_per_page", w.cfg.ScoreOpts.withDefaults().MaxNeighborsPerPage)

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("relevance worker stopped")
			return
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce scans every candidate project once. Test entry point.
func (w *Worker) RunOnce(ctx context.Context) {
	projects, err := w.candidateProjects(ctx)
	if err != nil {
		w.logger.Warn("relevance: project query failed", "err", err)
		return
	}
	if len(projects) == 0 {
		return
	}
	totalPairs := 0
	for _, pid := range projects {
		n, err := w.scanProject(ctx, pid)
		if err != nil {
			w.logger.Warn("relevance: scan failed",
				"project_id", pid, "err", err)
			continue
		}
		totalPairs += n
	}
	if totalPairs > 0 {
		w.logger.Info("relevance tick",
			"projects", len(projects), "pairs_total", totalPairs)
	}
}

func (w *Worker) candidateProjects(ctx context.Context) ([]uuid.UUID, error) {
	// Same shape as dedup/lint workers: projects with ≥2 live pages.
	// Single page → no pairs to score.
	rows, err := w.pool.Query(ctx, `
		SELECT pr.id
		  FROM brain.projects pr
		 WHERE (
		   SELECT count(*) FROM brain.pages p
		    WHERE p.project_id = pr.id AND p.deleted_at IS NULL
		 ) >= 2
	`)
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

func (w *Worker) scanProject(ctx context.Context, projectID uuid.UUID) (int, error) {
	pctx, cancel := context.WithTimeout(ctx, w.cfg.PerProjectTimeout)
	defer cancel()
	graph, err := w.loadGraph(pctx, projectID)
	if err != nil {
		return 0, err
	}
	if len(graph.Pages) < 2 {
		// Project has live pages but they all dropped from the wikilink
		// graph build (shouldn't happen, but defensive).
		return 0, nil
	}
	pairs := ScoreAll(graph, w.cfg.ScoreOpts)
	written, err := w.store.ReplaceProject(pctx, projectID, pairs)
	if err != nil {
		return written, err
	}

	// Louvain community detection on the relevance edge graph
	// (P2-tail-4). Uses the same pairs we just wrote — they're
	// already pruned to the strongest signal per page. Failures
	// here are non-fatal; relevance still works without communities.
	if cerr := w.assignCommunities(pctx, projectID, graph, pairs); cerr != nil {
		w.logger.Warn("relevance: community detection failed",
			"project_id", projectID, "err", cerr)
	}
	return written, nil
}

// assignCommunities runs Louvain on the relevance edges and writes
// the result to brain.pages.community_id. One UPDATE per community
// keeps the round-trip count bounded; the initial NULL sweep clears
// pages whose community was lost since the last tick (page deleted,
// edges weakened below threshold).
func (w *Worker) assignCommunities(
	ctx context.Context,
	projectID uuid.UUID,
	graph *ProjectGraph,
	pairs []PairScore,
) error {
	if len(pairs) == 0 {
		return nil
	}
	nodes := make([]uuid.UUID, 0, len(graph.Pages))
	for id := range graph.Pages {
		nodes = append(nodes, id)
	}
	edges := make([]Edge, 0, len(pairs))
	for _, p := range pairs {
		edges = append(edges, Edge{A: p.PageA, B: p.PageB, Weight: float64(p.Score)})
	}
	res := DetectCommunities(nodes, edges, LouvainOptions{})

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE brain.pages SET community_id = NULL
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID); err != nil {
		return fmt.Errorf("clear community: %w", err)
	}
	byCommunity := map[int][]uuid.UUID{}
	for id, c := range res.Community {
		if c < 0 {
			continue
		}
		byCommunity[c] = append(byCommunity[c], id)
	}
	for c, pageIDs := range byCommunity {
		if _, err := tx.Exec(ctx, `
			UPDATE brain.pages SET community_id = $1
			 WHERE project_id = $2 AND id = ANY($3)
		`, c, projectID, pageIDs); err != nil {
			return fmt.Errorf("set community %d: %w", c, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.logger.Info("relevance: communities assigned",
		"project_id", projectID,
		"communities", len(byCommunity),
		"modularity", res.Modularity)
	return nil
}

// loadGraph builds the ProjectGraph from a project's live pages +
// blocks. One query for pages (title + frontmatter), one query for
// blocks (text + caption); we then resolve [[wikilinks]] in-memory
// against the title→id map.
func (w *Worker) loadGraph(ctx context.Context, projectID uuid.UUID) (*ProjectGraph, error) {
	pageRows, err := w.pool.Query(ctx, `
		SELECT id, title, frontmatter
		  FROM brain.pages
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer pageRows.Close()

	pages := make(map[uuid.UUID]*PageNode)
	titleToID := make(map[string]uuid.UUID)
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
		pageType, _ := fm["type"].(string)
		norm := strings.TrimSpace(strings.ToLower(title))
		pages[id] = &PageNode{
			ID:          id,
			NormTitle:   norm,
			Type:        pageType,
			OutgoingIDs: map[uuid.UUID]struct{}{},
			Sources:     map[uuid.UUID]struct{}{},
		}
		if norm != "" {
			// First-seen wins on duplicate titles — collisions are
			// rare and this matches lint/sweep's resolution policy.
			if _, dup := titleToID[norm]; !dup {
				titleToID[norm] = id
			}
		}
		pageIDs = append(pageIDs, id)
	}
	if err := pageRows.Err(); err != nil {
		return nil, err
	}
	if len(pageIDs) == 0 {
		return &ProjectGraph{Pages: pages}, nil
	}

	// Block fetch — one query, ANY($1) on page_id keeps it to a single
	// round trip even with thousands of pages.
	blockRows, err := w.pool.Query(ctx, `
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
	defer blockRows.Close()
	for blockRows.Next() {
		var (
			pageID  uuid.UUID
			text    *string
			caption *string
		)
		if err := blockRows.Scan(&pageID, &text, &caption); err != nil {
			return nil, err
		}
		page, ok := pages[pageID]
		if !ok {
			continue
		}
		body := ""
		if text != nil {
			body = *text
		}
		if caption != nil {
			body += "\n" + *caption
		}
		for _, target := range ResolveWikilinks(body, titleToID, pageID) {
			page.OutgoingIDs[target] = struct{}{}
		}
	}
	if err := blockRows.Err(); err != nil {
		return nil, err
	}

	// P1-4: page→source 归属（source overlap 信号）。一次查 page_sources
	// 填每页 Sources 集合（webclip 抓取 + upload 文件，Phase 3 后覆盖全）。
	srcRows, err := w.pool.Query(ctx, `
		SELECT ps.page_id, ps.source_id
		  FROM brain.page_sources ps
		  JOIN brain.pages p ON p.id = ps.page_id
		 WHERE p.project_id = $1 AND p.deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer srcRows.Close()
	for srcRows.Next() {
		var pageID, sourceID uuid.UUID
		if err := srcRows.Scan(&pageID, &sourceID); err != nil {
			return nil, err
		}
		if page, ok := pages[pageID]; ok {
			page.Sources[sourceID] = struct{}{}
		}
	}
	return &ProjectGraph{Pages: pages}, srcRows.Err()
}

// Diagnose returns a one-line summary string for /healthz.
func (w *Worker) Diagnose(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	projects, err := w.candidateProjects(pctx)
	if err != nil {
		return fmt.Sprintf("relevance: query failed: %v", err)
	}
	return fmt.Sprintf("relevance: %d candidate projects", len(projects))
}
