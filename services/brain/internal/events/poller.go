// Package events — Poller is the durability floor for the brain.events
// outbox. Listener (LISTEN/NOTIFY) gives sub-second realtime delivery
// during steady state but is lossy across restarts, broker outages, and
// multi-replica fanout. Poller fills that gap.
//
// Design (transactional outbox pattern):
//
//   1. Wiki writes commit (events row, business data) in the same tx.
//   2. Poller wakes — periodic tick, or LISTEN brain_events nudge —
//      and runs:
//         SELECT … FROM brain.events
//          WHERE published_at IS NULL
//          ORDER BY id
//          LIMIT $batch
//          FOR UPDATE SKIP LOCKED
//      … inside its own tx. SKIP LOCKED gives at-most-one-replica per row.
//   3. For each row: publisher.Publish(scope, kind, payload).
//   4. On success: UPDATE brain.events SET published_at = now()
//      WHERE id = $1. Commit. The row is now considered published.
//   5. On failure: leave the row, commit nothing, the next tick retries.
//
// Combined with the existing Listener: the Listener still races to deliver
// events realtime; if it succeeds, the row is published_at = now() and the
// Poller skips it. If the Listener doesn't (process crash, broker outage,
// notification missed), the Poller catches up later. Both must be safe to
// run concurrently — that's the point of SKIP LOCKED + idempotent publish.
//
// Idempotence note: NATS at-least-once + the downstream graph extractor's
// idempotent UpsertNode/UpsertEdge means a duplicate publish (Listener +
// Poller racing on the same row before either updates published_at) is
// harmless. The cost is one extra publish, not a corrupted graph.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Poller scans the outbox and publishes pending rows.
type Poller struct {
	Pool      *pgxpool.Pool
	Publisher publisher.Publisher
	Logger    *slog.Logger

	// Interval — how often to scan when no NOTIFY nudge arrives.
	// 0 → 5s. Production default trades a small steady-state bandwidth
	// cost for "events stuck < interval after a broker recovery".
	Interval time.Duration

	// Batch — how many rows to claim per scan. 0 → 100. Bigger batches
	// amortize the round-trip but block other replicas longer (rows
	// are FOR UPDATE'd until the poller's tx commits). 100 is a
	// reasonable balance.
	Batch int

	// nudge — buffered chan-of-1 the LISTEN goroutine pings to wake
	// the poller for low-latency delivery. Buffered so a busy LISTEN
	// loop never blocks; spurious wake-ups are cheap (empty SELECT).
	nudge chan struct{}

	// stats — observability without forcing a metrics dep on the
	// poller. Exposed via Stats() for tests + ops dashboards.
	publishedTotal uint64
	failedTotal    uint64
	scansTotal     uint64
}

// Stats — counters since boot. All atomic, safe to read concurrently.
type Stats struct {
	Published uint64
	Failed    uint64
	Scans     uint64
}

func (p *Poller) Stats() Stats {
	return Stats{
		Published: atomic.LoadUint64(&p.publishedTotal),
		Failed:    atomic.LoadUint64(&p.failedTotal),
		Scans:     atomic.LoadUint64(&p.scansTotal),
	}
}

// Run blocks until ctx is cancelled. Spawns the LISTEN nudge goroutine
// and the periodic scan loop. Errors are logged + retried with backoff;
// a poller that simply gives up would be the worst possible failure mode.
func (p *Poller) Run(ctx context.Context) error {
	if p.Pool == nil {
		return errors.New("events.Poller: nil Pool")
	}
	if p.Publisher == nil {
		return errors.New("events.Poller: nil Publisher")
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	if p.Interval == 0 {
		p.Interval = 5 * time.Second
	}
	if p.Batch == 0 {
		p.Batch = 100
	}
	p.nudge = make(chan struct{}, 1)

	p.Logger.Info("events.Poller started",
		"interval", p.Interval, "batch", p.Batch)

	// LISTEN nudge — short-circuit the periodic tick when an INSERT
	// notification lands. Best-effort: if LISTEN fails, the periodic
	// tick alone is still correct, just higher latency.
	go p.listenNudge(ctx)

	tick := time.NewTicker(p.Interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-p.nudge:
		}
		// Drain pending rows in batches until the queue is empty or we
		// hit a non-transient error. Batch loop avoids single-row tick
		// throughput collapse when a backlog accumulates (broker outage).
		for {
			n, err := p.processBatch(ctx)
			if err != nil {
				p.Logger.Warn("events.Poller: batch failed", "err", err)
				break
			}
			if n < p.Batch {
				break // queue empty (or near-empty); wait for next tick/nudge
			}
		}
	}
}

// processBatch runs one transaction:
//
//	BEGIN
//	SELECT … FOR UPDATE SKIP LOCKED LIMIT batch
//	  → for each row: publish + UPDATE published_at
//	COMMIT
//
// Returns the number of rows actually processed (published_at updated).
// A single row's publish failure is logged and skipped; the row is
// left unpublished so the next scan retries it. We do NOT abort the
// whole batch on one failure — the tx still commits the rows that
// did succeed, so partial progress is preserved.
func (p *Poller) processBatch(ctx context.Context) (int, error) {
	atomic.AddUint64(&p.scansTotal, 1)

	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	// Use a fresh context for rollback; the outer ctx may already be
	// cancelled, which would prevent rollback from running cleanly.
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	rows, err := tx.Query(ctx, `
		SELECT id, scope, event_type, payload
		  FROM brain.events
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED
	`, p.Batch)
	if err != nil {
		return 0, err
	}

	type row struct {
		id      int64
		scope   string
		kind    string
		payload []byte
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.scope, &r.kind, &r.payload); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	processed := 0
	for _, r := range batch {
		var inner map[string]any
		if len(r.payload) > 0 {
			if err := json.Unmarshal(r.payload, &inner); err != nil {
				p.Logger.Warn("events.Poller: bad payload json — marking published to avoid loop",
					"id", r.id, "err", err)
				// Bad payload would loop forever — mark published and skip.
				if _, uerr := tx.Exec(ctx,
					`UPDATE brain.events SET published_at = now() WHERE id = $1`,
					r.id); uerr != nil {
					return processed, uerr
				}
				atomic.AddUint64(&p.failedTotal, 1)
				continue
			}
		}
		body := map[string]any{
			"event_id":   r.id,
			"event_type": r.kind,
			"data":       inner,
		}
		if err := p.Publisher.Publish(ctx, r.scope, r.kind, body); err != nil {
			// Transient (broker down etc.) — leave published_at NULL,
			// next tick retries. Don't abort the batch: rows we already
			// successfully marked must commit so they don't redeliver.
			atomic.AddUint64(&p.failedTotal, 1)
			p.Logger.Warn("events.Poller: publish failed; will retry",
				"id", r.id, "scope", r.scope, "err", err)
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE brain.events SET published_at = now() WHERE id = $1`,
			r.id); err != nil {
			return processed, err
		}
		atomic.AddUint64(&p.publishedTotal, 1)
		processed++
	}

	if err := tx.Commit(ctx); err != nil {
		// Commit failure means none of the marks landed; the rows we
		// "published" will redeliver on next scan. Idempotent downstream
		// makes that harmless — log it and let it ride.
		return 0, err
	}
	if processed > 0 {
		p.Logger.Debug("events.Poller: batch", "processed", processed,
			"in_batch", len(batch))
	}
	return processed, nil
}

// listenNudge subscribes to LISTEN brain_events. Each notification
// pings the poller's nudge channel to short-circuit the periodic
// tick. Failure here is best-effort — the periodic tick alone is
// still correct, just higher steady-state latency.
func (p *Poller) listenNudge(ctx context.Context) {
	backoff := 500 * time.Millisecond
	for {
		if err := p.listenNudgeOnce(ctx); err != nil &&
			!errors.Is(err, context.Canceled) {
			p.Logger.Debug("events.Poller: nudge listener cycle ended",
				"err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
}

func (p *Poller) listenNudgeOnce(ctx context.Context) error {
	conn, err := p.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN brain_events"); err != nil {
		return err
	}
	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		// Wake the poller; non-blocking — if a wake-up is already
		// pending, drop ours (one wake-up per scan cycle is plenty).
		select {
		case p.nudge <- struct{}{}:
		default:
		}
	}
}
