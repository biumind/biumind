// Outbox poller — the durability floor for app_center.events.
//
// Pattern lifted from services/brain/internal/events/poller.go (the
// original transactional outbox); kept self-contained here so any
// future schema/scope tweaks stay inside services/app_center without
// touching brain.
//
// Lifecycle:
//
//	BEGIN
//	SELECT id, scope, event_type, payload
//	  FROM app_center.events
//	 WHERE published_at IS NULL
//	 ORDER BY id
//	 LIMIT $batch
//	 FOR UPDATE SKIP LOCKED
//	→ for each row:
//	     publisher.Publish(TopicForScope(scope), KindFor(event_type), payload)
//	     UPDATE app_center.events SET published_at = now() WHERE id = $1
//	COMMIT
//
// LISTEN nudge: the migration installs a trigger that fires
// `pg_notify('app_center_events', ...)` after every INSERT. The
// listener loop drains those notifications and pings the poller's
// nudge channel for sub-second realtime delivery in steady state.
// The periodic tick is the safety net — it would catch up after a
// broker outage / process restart even if all NOTIFY signals were
// dropped.

package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Poller struct {
	Pool      *pgxpool.Pool
	Publisher Publisher
	Logger    *slog.Logger

	// Interval — periodic tick; default 5s.
	Interval time.Duration

	// Batch — rows per scan; default 100.
	Batch int

	nudge chan struct{}

	publishedTotal uint64
	failedTotal    uint64
	scansTotal     uint64
}

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

// Run blocks until ctx is canceled.
func (p *Poller) Run(ctx context.Context) error {
	if p.Pool == nil {
		return errors.New("outbox.Poller: nil Pool")
	}
	if p.Publisher == nil {
		return errors.New("outbox.Poller: nil Publisher")
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

	p.Logger.Info("app_center outbox poller started",
		"interval", p.Interval, "batch", p.Batch)

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
		// Drain in batches until empty or transient error.
		for {
			n, err := p.processBatch(ctx)
			if err != nil {
				p.Logger.Warn("outbox.Poller: batch failed", "err", err)
				break
			}
			if n < p.Batch {
				break
			}
		}
	}
}

// processBatch claims, publishes, and stamps one batch.
func (p *Poller) processBatch(ctx context.Context) (int, error) {
	atomic.AddUint64(&p.scansTotal, 1)

	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	rows, err := tx.Query(ctx, `
		SELECT id, scope, event_type, payload
		  FROM app_center.events
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
				p.Logger.Warn("outbox.Poller: bad payload — marking published to avoid loop",
					"id", r.id, "err", err)
				if _, uerr := tx.Exec(ctx,
					`UPDATE app_center.events SET published_at = now() WHERE id = $1`,
					r.id); uerr != nil {
					return processed, uerr
				}
				atomic.AddUint64(&p.failedTotal, 1)
				continue
			}
		}
		topic := TopicForScope(r.scope)
		kind := KindFor(r.kind)
		body := map[string]any{
			"event_id":   r.id,
			"event_type": r.kind,
			"data":       inner,
		}
		if err := p.Publisher.Publish(ctx, topic, kind, body); err != nil {
			// Transient — leave row, next tick retries.
			atomic.AddUint64(&p.failedTotal, 1)
			p.Logger.Warn("outbox.Poller: publish failed; will retry",
				"id", r.id, "topic", topic, "err", err)
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE app_center.events SET published_at = now() WHERE id = $1`,
			r.id); err != nil {
			return processed, err
		}
		atomic.AddUint64(&p.publishedTotal, 1)
		processed++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if processed > 0 {
		p.Logger.Debug("outbox.Poller: batch", "processed", processed,
			"in_batch", len(batch))
	}
	return processed, nil
}

// listenNudge subscribes to LISTEN app_center_events.
// Failure here is best-effort — periodic tick alone is correct, just
// higher steady-state latency.
func (p *Poller) listenNudge(ctx context.Context) {
	backoff := 500 * time.Millisecond
	for {
		if err := p.listenNudgeOnce(ctx); err != nil &&
			!errors.Is(err, context.Canceled) {
			p.Logger.Debug("outbox.Poller: nudge cycle ended",
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

	if _, err := conn.Exec(ctx, "LISTEN app_center_events"); err != nil {
		return err
	}
	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		select {
		case p.nudge <- struct{}{}:
		default:
		}
	}
}
