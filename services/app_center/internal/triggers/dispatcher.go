// Cron dispatcher.
//
// One goroutine per app_center process polls scheduler_jobs every
// PollInterval seconds. The query uses FOR UPDATE SKIP LOCKED so
// multiple replicas safely share work — exactly one process claims
// each due row, even when many replicas race.
//
// Concurrency model:
//
//	tick → BEGIN tx
//	     → SELECT * FROM scheduler_jobs
//	         WHERE enabled AND kind='cron' AND next_run ≤ now()
//	         AND (locked_until IS NULL OR locked_until ≤ now())
//	         ORDER BY next_run
//	         LIMIT BatchSize
//	         FOR UPDATE SKIP LOCKED
//	     → for each row:
//	           UPDATE locked_until = now() + LockTTL
//	     → COMMIT (releases the SELECT FOR UPDATE locks)
//	     → for each claimed row (now in our local slice):
//	           dispatch via Registry.DispatchOnTrigger
//	           record invocation
//	           UPDATE next_run, last_run_at, last_status
//
// We split into two transactions deliberately: the SELECT...FOR
// UPDATE is short (just stamps locked_until); the actual dispatch
// happens outside any tx so a slow App handler doesn't hold a
// connection. locked_until is the mutex; even if we crash mid-
// dispatch, another replica picks up the row after lock TTL.

package triggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dispatcher is the cron poller / fire engine.
type Dispatcher struct {
	Pool         *pgxpool.Pool
	Registry     *biuapp.Registry
	Logger       *slog.Logger
	PollInterval time.Duration // default 30s
	LockTTL      time.Duration // default 5m
	BatchSize    int           // default 50
	HandlerWait  time.Duration // default 60s — wall-clock cap on a single dispatch

	wg sync.WaitGroup
}

// Run drives the polling loop until ctx is cancelled. Returns when
// ctx is done; safe to invoke from a goroutine in service main.
//
// The function is idempotent at the schema level — it doesn't
// allocate any persistent resources, just reads and updates
// scheduler_jobs. So an immediate restart of the dispatcher (after
// crash, deploy) picks up where it left off when locked_until
// expires.
func (d *Dispatcher) Run(ctx context.Context) {
	d.applyDefaults()

	// First tick fires immediately; subsequent ones every PollInterval.
	// This avoids a "first tick after PollInterval" cold start when the
	// service comes up with a backlog of overdue jobs.
	d.tick(ctx)

	t := time.NewTicker(d.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			d.wg.Wait()
			d.Logger.Info("dispatcher: stopped")
			return
		case <-t.C:
			d.tick(ctx)
		}
	}
}

// tick is one polling pass. Errors are logged + swallowed so one bad
// row / one DB hiccup doesn't kill the whole dispatcher.
//
// 跟 Run 之外的入口 (e.g. 单元测试 d.tick(ctx)) 共用此函数, 同样需要
// defaults — 否则 BatchSize=0 让 SQL LIMIT 0 直接拿不到任何行,
// HandlerWait=0 让 fire 的 ctx 立即超时, Logger=nil 在 error path
// panic。把 zero-value 兜底统一在这里, Run() 之外也安全。
func (d *Dispatcher) tick(ctx context.Context) {
	d.applyDefaults()
	jobs, err := d.claim(ctx)
	if err != nil {
		d.Logger.Warn("dispatcher: claim", "err", err)
		return
	}
	for _, j := range jobs {
		d.wg.Add(1)
		go func(j claimedJob) {
			defer d.wg.Done()
			d.fire(ctx, j)
		}(j)
	}
}

// applyDefaults 给零值字段填默认 — 测试入口 d.tick(ctx) / d.fire(ctx)
// 共用, Run() 也调一次。幂等。
func (d *Dispatcher) applyDefaults() {
	if d.PollInterval == 0 {
		d.PollInterval = 30 * time.Second
	}
	if d.LockTTL == 0 {
		d.LockTTL = 5 * time.Minute
	}
	if d.BatchSize == 0 {
		d.BatchSize = 50
	}
	if d.HandlerWait == 0 {
		d.HandlerWait = 60 * time.Second
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
}

// claimedJob is the in-process representation of a row claimed for
// dispatch. We deliberately don't reuse the schema-mapped struct
// here — claim only needs a small subset, and decoupling keeps the
// hot SQL narrow.
type claimedJob struct {
	ID         uuid.UUID
	InstallID  uuid.UUID
	Identifier string
	Name       string
	Action     string
	Input      []byte
	CronExpr   string
}

// claim runs the SELECT FOR UPDATE SKIP LOCKED + UPDATE locked_until
// in one tx and returns the rows to dispatch.
func (d *Dispatcher) claim(ctx context.Context) ([]claimedJob, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()
	rows, err := tx.Query(ctx, `
		SELECT id, install_id, identifier, name, action, input, cron_expr
		  FROM app_center.scheduler_jobs
		 WHERE enabled = true
		   AND kind = 'cron'
		   AND next_run IS NOT NULL
		   AND next_run <= $1
		   AND (locked_until IS NULL OR locked_until <= $1)
		 ORDER BY next_run
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED
	`, now, d.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}

	var out []claimedJob
	for rows.Next() {
		var j claimedJob
		var cronExpr *string
		if err := rows.Scan(&j.ID, &j.InstallID, &j.Identifier,
			&j.Name, &j.Action, &j.Input, &cronExpr); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan: %w", err)
		}
		if cronExpr != nil {
			j.CronExpr = *cronExpr
		}
		out = append(out, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iter: %w", err)
	}

	if len(out) == 0 {
		return nil, tx.Commit(ctx)
	}

	// Stamp locked_until on each claimed id. We use ANY($1) so a single
	// UPDATE handles the whole batch.
	ids := make([]uuid.UUID, len(out))
	for i, j := range out {
		ids[i] = j.ID
	}
	lockUntil := now.Add(d.LockTTL)
	if _, err := tx.Exec(ctx, `
		UPDATE app_center.scheduler_jobs
		   SET locked_until = $1, updated_at = $2
		 WHERE id = ANY($3)
	`, lockUntil, now, ids); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return out, nil
}

// fire dispatches one claimed job, then updates next_run + status.
// Errors are recorded on the row, never propagated — the dispatcher
// is best-effort: a bad job logs + retries on schedule, doesn't take
// down the loop.
func (d *Dispatcher) fire(ctx context.Context, j claimedJob) {
	cctx, cancel := context.WithTimeout(ctx, d.HandlerWait)
	defer cancel()

	ev := biuapp.TriggerEvent{
		TriggerKind: biuapp.TriggerCron,
		Name:        j.Name,
		Action:      j.Action,
		Input:       j.Input,
		FiredAt:     time.Now().UTC(),
		Install: biuapp.Install{
			ID:         j.InstallID.String(),
			Identifier: j.Identifier,
		},
	}

	start := time.Now()
	dispatchErr := d.Registry.DispatchOnTrigger(cctx, j.Identifier, ev)
	durationMs := int(time.Since(start).Milliseconds())

	status := "ok"
	errMsg := ""
	if dispatchErr != nil {
		status = "error"
		errMsg = dispatchErr.Error()
		if errors.Is(dispatchErr, context.DeadlineExceeded) {
			status = "timeout"
		}
	}

	// Compute next_run from cron_expr anchored to now (NOT to the
	// firing's nominal time) so a slow handler doesn't make the
	// dispatcher miss the following tick.
	nextRun := time.Now().UTC()
	if j.CronExpr != "" {
		if n, err := NextRun(j.CronExpr, nextRun); err == nil {
			nextRun = n
		}
	}

	if err := d.recordOutcome(ctx, j, status, errMsg, durationMs, nextRun); err != nil {
		d.Logger.Warn("dispatcher: record outcome", "job", j.ID.String(), "err", err)
	}
}

// recordOutcome closes one tx that:
//  1. updates scheduler_jobs (next_run, last_run_at, last_status, ...)
//  2. inserts the audit row in app_center.invocations
//  3. writes a single events row (kind=app.trigger_fired)
func (d *Dispatcher) recordOutcome(ctx context.Context, j claimedJob, status, errMsg string, durationMs int, nextRun time.Time) error {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()
	consecFailDelta := 0
	if status == "ok" {
		consecFailDelta = -9999 // sentinel: reset to 0
	} else {
		consecFailDelta = 1
	}

	if _, err := tx.Exec(ctx, `
		UPDATE app_center.scheduler_jobs
		   SET next_run = $1,
		       last_run_at = $2,
		       last_status = $3,
		       last_error = $4,
		       locked_until = NULL,
		       consecutive_failures = CASE WHEN $5 = -9999 THEN 0 ELSE consecutive_failures + $5 END,
		       updated_at = $2
		 WHERE id = $6
	`, nextRun, now, status, errMsg, consecFailDelta, j.ID); err != nil {
		return fmt.Errorf("update job: %w", err)
	}

	// Audit row.
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.invocations
			(install_id, app_id, identifier, action,
			 caller, caller_id, trace_id,
			 duration_ms, status, error_code)
		VALUES ($1, $2, $3, $4,
		        'scheduler', $5, '',
		        $6, $7, $8)
	`, j.InstallID, "app_"+j.Identifier, j.Identifier, j.Action,
		j.Name, durationMs, status,
		statusToErrCode(status, errMsg)); err != nil {
		return fmt.Errorf("audit: %w", err)
	}

	// Event.
	payload := map[string]any{
		"trigger_name": j.Name,
		"action":       j.Action,
		"status":       status,
		"duration_ms":  durationMs,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	plBytes, _ := json.Marshal(payload)
	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   j.InstallID.String(),
		ActorType: events.ActorScheduler,
		ActorID:   j.Name,
		Type:      events.AppTriggerFired,
		Payload:   plBytes,
	}); err != nil {
		return fmt.Errorf("events: %w", err)
	}

	return tx.Commit(ctx)
}

func statusToErrCode(status, errMsg string) string {
	if status == "ok" {
		return ""
	}
	if status == "timeout" {
		return "timeout"
	}
	// Truncate errMsg to keep the column compact.
	if len(errMsg) > 80 {
		return errMsg[:80]
	}
	return errMsg
}
