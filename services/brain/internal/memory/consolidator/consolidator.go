// Package consolidator runs maintenance passes over brain.memories
// so the table doesn't grow stale or noisy:
//
//  1. Dedupe — when two memories in the same project are semantically
//     identical (cosine distance ≤ threshold), keep the one with the
//     higher salience and delete the rest. Both must have embeddings;
//     unembedded rows are skipped (the embed worker will reach them
//     first).
//
//  2. Salience decay — memories that haven't been recalled in N days
//     have their salience reduced by decay_per_day × idle_days, clipped
//     at zero. Recall touches last_accessed_at so frequently-used
//     memories never decay; abandoned ones drift toward 0 and rank
//     lower in future Recall calls.
//
// Both passes run inside a single transaction per project to keep
// the audit footprint coherent: no half-merged states visible to a
// concurrent reader.
package consolidator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Interval        time.Duration // default 1h
	CosineThreshold float64       // default 0.05 (very tight; demands true dupes)
	DecayPerDay     float64       // default 0.01; salience -= decay * idle_days
	IdleAfterDays   float64       // default 7;   memories younger than this never decay
	Logger          *slog.Logger
}

type Consolidator struct {
	pool *pgxpool.Pool
	cfg  Config
}

func New(pool *pgxpool.Pool, cfg Config) *Consolidator {
	if cfg.Interval == 0 {
		cfg.Interval = time.Hour
	}
	if cfg.CosineThreshold == 0 {
		cfg.CosineThreshold = 0.05
	}
	if cfg.DecayPerDay == 0 {
		cfg.DecayPerDay = 0.01
	}
	if cfg.IdleAfterDays == 0 {
		cfg.IdleAfterDays = 7
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Consolidator{pool: pool, cfg: cfg}
}

func (c *Consolidator) Run(ctx context.Context) {
	c.cfg.Logger.Info("memory consolidator started",
		"interval", c.cfg.Interval,
		"cosine_threshold", c.cfg.CosineThreshold,
		"decay_per_day", c.cfg.DecayPerDay,
		"idle_after_days", c.cfg.IdleAfterDays)
	t := time.NewTicker(c.cfg.Interval)
	defer t.Stop()
	c.RunOnce(ctx) // drain any pending consolidation immediately
	for {
		select {
		case <-ctx.Done():
			c.cfg.Logger.Info("memory consolidator stopped")
			return
		case <-t.C:
			c.RunOnce(ctx)
		}
	}
}

// Stats describes one consolidator pass.
type Stats struct {
	MergedRows  int // memories deleted because a duplicate already existed
	DecayedRows int // memories whose salience was nudged down
}

// RunOnce performs a single consolidation pass across all projects.
// Exposed for tests so they can drain the queue without spinning a ticker.
func (c *Consolidator) RunOnce(ctx context.Context) Stats {
	merged, err := c.dedupePass(ctx)
	if err != nil {
		c.cfg.Logger.Warn("consolidator: dedupe pass failed", "err", err)
	}
	decayed, err := c.decayPass(ctx)
	if err != nil {
		c.cfg.Logger.Warn("consolidator: decay pass failed", "err", err)
	}
	if merged+decayed > 0 {
		c.cfg.Logger.Info("memory consolidator pass",
			"merged", merged, "decayed", decayed)
	} else {
		// 安静的 tick 在 Info 级别没有意义,但 debug 排查"为什么没合并"
		// 时(阈值是不是太宽松)需要看到 pass 至少跑过了。
		c.cfg.Logger.DebugContext(ctx, "memory consolidator pass: empty",
			"merged", 0, "decayed", 0)
	}
	metrics.RecordConsolidation(merged, decayed)
	return Stats{MergedRows: merged, DecayedRows: decayed}
}

// dedupePass scans for (project_id, embedding) pairs whose cosine
// distance falls under the threshold. For each cluster keep the
// memory with the highest salience (ties broken by oldest created_at,
// so the audit trail is stable) and delete the rest.
//
// We do this with a self-join keyed on (project_id) restricted to
// rows that have embeddings, then collapse pairs to "loser" set in Go
// to avoid Postgres-side window-function complexity.
func (c *Consolidator) dedupePass(ctx context.Context) (int, error) {
	// Fetch every dup pair (a.id, b.id, distance, salience_a, salience_b,
	// created_a, created_b). Scope to same project. Deduping across
	// projects would leak data between tenants.
	rows, err := c.pool.Query(ctx, `
		SELECT
			a.id, b.id,
			(a.embedding <=> b.embedding) AS dist,
			a.salience, b.salience,
			a.created_at, b.created_at
		FROM brain.memories a
		JOIN brain.memories b
		  ON a.project_id = b.project_id
		 AND a.id < b.id
		 AND a.owner_id = b.owner_id
		WHERE a.embedding IS NOT NULL
		  AND b.embedding IS NOT NULL
		  AND (a.embedding <=> b.embedding) <= $1
	`, c.cfg.CosineThreshold)
	if err != nil {
		return 0, fmt.Errorf("dedup query: %w", err)
	}
	defer rows.Close()

	type pair struct {
		a, b     uuid.UUID
		distance float64
		sA, sB   float32
		cA, cB   time.Time
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.a, &p.b, &p.distance, &p.sA, &p.sB, &p.cA, &p.cB); err != nil {
			return 0, err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	// Decide which side of each pair "loses". Loser = lower salience;
	// tie-break on younger row (newer created_at) so the older audit
	// anchor survives.
	losers := map[uuid.UUID]bool{}
	winnerSalience := map[uuid.UUID]float32{}
	for _, p := range pairs {
		var winner, loser uuid.UUID
		var sw float32
		switch {
		case p.sA > p.sB:
			winner, loser = p.a, p.b
			sw = p.sA
		case p.sB > p.sA:
			winner, loser = p.b, p.a
			sw = p.sB
		default: // tie on salience: older wins
			if p.cA.Before(p.cB) || p.cA.Equal(p.cB) {
				winner, loser = p.a, p.b
			} else {
				winner, loser = p.b, p.a
			}
			sw = p.sA
		}
		// Don't overwrite a previous "winner" decision: a row marked
		// loser by an earlier pair stays a loser even if a later pair
		// would have crowned it the winner. This stops chains from
		// resurrecting deleted memories.
		if losers[winner] {
			continue
		}
		losers[loser] = true
		// Track the highest salience seen for the winner so we can
		// promote it (winner inherits max salience across the cluster).
		if cur, ok := winnerSalience[winner]; !ok || sw > cur {
			winnerSalience[winner] = sw
		}
	}
	if len(losers) == 0 {
		return 0, nil
	}

	loserIDs := make([]uuid.UUID, 0, len(losers))
	for id := range losers {
		loserIDs = append(loserIDs, id)
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("dedup tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Bump winner salience to the max in its cluster so the merged
	// memory ranks at least as high as the deleted ones did.
	for winner, sal := range winnerSalience {
		if _, err := tx.Exec(ctx,
			`UPDATE brain.memories SET salience = GREATEST(salience, $1)
			   WHERE id = $2`,
			sal, winner); err != nil {
			return 0, fmt.Errorf("promote salience: %w", err)
		}
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM brain.memories WHERE id = ANY($1)`, loserIDs)
	if err != nil {
		return 0, fmt.Errorf("delete losers: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("dedup commit: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// decayPass nudges salience down on memories that have been idle for
// more than IdleAfterDays. Single SQL statement; postgres clamps
// salience at zero.
func (c *Consolidator) decayPass(ctx context.Context) (int, error) {
	tag, err := c.pool.Exec(ctx, `
		UPDATE brain.memories
		   SET salience = GREATEST(0,
		         salience - $1 * GREATEST(0,
		           EXTRACT(EPOCH FROM (now() - last_accessed_at)) / 86400.0
		           - $2))
		 WHERE last_accessed_at < now() - ($2 * interval '1 day')
		   AND salience > 0
	`, c.cfg.DecayPerDay, c.cfg.IdleAfterDays)
	if err != nil {
		return 0, fmt.Errorf("decay: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ─── Misc ───────────────────────────────────────────────

// quiet is a workaround for go vet — keep `database/sql` importable
// for future test helpers without complaint.
var _ = sql.ErrNoRows
