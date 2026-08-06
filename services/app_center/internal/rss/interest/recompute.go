// Package interest — daily cron that recomputes per-user interest
// centroids and topic frequencies from the reading_log ledger.
//
// The Today picker (internal/rss/today) reads user_interests by
// user_id; this package is the writer. Recompute is idempotent and
// runs in a single goroutine with a 24h tick from main.go. Per-user
// reads + writes are independent so a long-running batch doesn't hold
// any lock — we UPSERT one user at a time.

package interest

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

const (
	// Lookback window: 30d of behaviour. Older signals decay out.
	defaultLookbackDays = 30

	// Minimum entries with embeddings before we trust the centroid.
	// Below this we leave interest_centroid NULL so the picker falls
	// back to global popularity for the user.
	minSamplesForCentroid = 5

	// Top-K topics retained.
	topicsK = 5
)

type Recomputer struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	LookbackDays int
}

func New(pool *pgxpool.Pool) *Recomputer {
	return &Recomputer{
		Pool:         pool,
		Logger:       slog.Default(),
		LookbackDays: defaultLookbackDays,
	}
}

// Run kicks off a single-goroutine 24h ticker. Caller cancels via ctx.
// First tick fires immediately so a fresh boot doesn't wait 24h to
// surface interests for the first time.
func (r *Recomputer) Run(ctx context.Context) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	r.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Recomputer) runOnce(ctx context.Context) {
	users, err := r.activeUsers(ctx)
	if err != nil {
		r.Logger.Warn("interest: list active users", "err", err.Error())
		return
	}
	if len(users) == 0 {
		return
	}
	ok := 0
	for _, u := range users {
		if err := r.recomputeUser(ctx, u); err != nil {
			r.Logger.Warn("interest: recompute user", "user", u, "err", err.Error())
			continue
		}
		ok++
	}
	r.Logger.Info("interest: recompute tick", "considered", len(users), "ok", ok)
}

func (r *Recomputer) activeUsers(ctx context.Context) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT DISTINCT user_id
		  FROM rss.reading_log
		 WHERE created_at > now() - make_interval(days => $1)`,
		r.LookbackDays)
	if err != nil {
		return nil, fmt.Errorf("interest: query active users: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// recomputeUser pulls the user's positive engagement events
// (read_full / starred / wiki / task) within lookback, takes the mean
// of the entries' embeddings, and counts ai_topics frequency. UPSERTs
// into user_interests.
//
// We deliberately exclude "opened" — opening doesn't mean the user
// liked the entry, and "dismissed" is excluded as a future negative
// signal (M3 may use it for inverse weighting; v2 keeps it simple).
func (r *Recomputer) recomputeUser(ctx context.Context, userID string) error {
	rows, err := r.Pool.Query(ctx, `
		SELECT e.embedding, e.ai_topics
		  FROM rss.reading_log l
		  JOIN rss.entries     e ON e.id = l.entry_id
		 WHERE l.user_id = $1
		   AND l.event IN ('read_full','starred','wiki','task','shared')
		   AND l.created_at > now() - make_interval(days => $2)
		   AND e.embedding IS NOT NULL`,
		userID, r.LookbackDays)
	if err != nil {
		return fmt.Errorf("query engagement: %w", err)
	}
	defer rows.Close()

	var sumVec []float32
	topicCount := map[string]int{}
	samples := 0
	for rows.Next() {
		var emb pgvector.Vector
		var topics []string
		if err := rows.Scan(&emb, &topics); err != nil {
			return err
		}
		v := emb.Slice()
		if len(v) == 0 {
			continue
		}
		if sumVec == nil {
			sumVec = make([]float32, len(v))
		}
		if len(sumVec) != len(v) {
			continue // shape skew, skip
		}
		for i, x := range v {
			sumVec[i] += x
		}
		samples++
		for _, t := range topics {
			if t != "" {
				topicCount[t]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	topTopics := topNTopics(topicCount, topicsK)
	if samples < minSamplesForCentroid {
		// Not enough signal — write topics-only row so picker knows
		// the user exists but no centroid; centroid stays NULL.
		_, err = r.Pool.Exec(ctx, `
			INSERT INTO rss.user_interests (user_id, interest_centroid, top_topics, sample_count, updated_at)
			VALUES ($1, NULL, $2, $3, now())
			ON CONFLICT (user_id) DO UPDATE
			   SET interest_centroid = NULL,
			       top_topics        = EXCLUDED.top_topics,
			       sample_count      = EXCLUDED.sample_count,
			       updated_at        = now()`,
			userID, topTopics, samples)
		return err
	}

	for i := range sumVec {
		sumVec[i] /= float32(samples)
	}
	centroid := pgvector.NewVector(sumVec)
	_, err = r.Pool.Exec(ctx, `
		INSERT INTO rss.user_interests (user_id, interest_centroid, top_topics, sample_count, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id) DO UPDATE
		   SET interest_centroid = EXCLUDED.interest_centroid,
		       top_topics        = EXCLUDED.top_topics,
		       sample_count      = EXCLUDED.sample_count,
		       updated_at        = now()`,
		userID, centroid, topTopics, samples)
	return err
}

func topNTopics(counts map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k // tie-break for determinism
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.k
	}
	return out
}

// LogEvent is the small writer shared by client-driven endpoints
// (entries_mark_read / entries_star / entries_to_wiki etc.). Kept
// here because the interest package owns the reading_log schema.
//
// Not transactional with whatever caused the event — best-effort. A
// dropped log row at most marginally biases the next interest tick.
func LogEvent(ctx context.Context, pool *pgxpool.Pool, userID, entryID, event string, seconds int) error {
	if pool == nil {
		return nil
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO rss.reading_log (user_id, entry_id, event, seconds)
		VALUES ($1, $2, $3, $4)`,
		userID, entryID, event, seconds)
	return err
}
