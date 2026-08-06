// app_center.invocations recorder.
//
// Writes one row per Tool call from the apptools-bound closures so
// dashboards can see "what apps did this agent invoke". Mirrors the
// columns in services/app_center/migrations/00003_invocations.sql.
//
// Why this lives in runtime, not app_center: the closure needs to
// run on the agent's hot path, and reaching across services for an
// HTTP /v1/invocations write would add latency + a transport hop. We
// share the schema (it lives under app_center) and write directly.
// app_center is the read side; runtime is one of multiple writers.
//
// M17: each invocations row is paired with one app_center.events row
// (event_type='app.action_invoked') in the same transaction so the
// outbox poller can fan it out to the AG-UI CUSTOM stream — without
// the event row, the audit log accumulates but nobody downstream
// learns about the call.

package apptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxRecorder writes invocation rows through the runtime's pgx pool.
// Construct once per process; safe for concurrent use.
type PgxRecorder struct {
	Pool *pgxpool.Pool
}

func NewPgxRecorder(pool *pgxpool.Pool) *PgxRecorder { return &PgxRecorder{Pool: pool} }

func (r *PgxRecorder) Record(ctx context.Context, inv InvocationRecord) error {
	if r == nil || r.Pool == nil {
		return errors.New("apptools: PgxRecorder not wired")
	}
	// Single tx so the invocations row + outbox event are atomic. A
	// half-written pair (audit row without event, or event without
	// audit) is worse than neither.
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("apptools: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.invocations
			(install_id, app_id, identifier, action,
			 caller, caller_id, trace_id, duration_ms, status, error_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		inv.InstallID, inv.AppID, inv.Identifier, inv.Action,
		inv.Caller, inv.CallerID, inv.TraceID, inv.DurationMs,
		inv.Status, inv.ErrorCode); err != nil {
		return fmt.Errorf("apptools: insert invocation: %w", err)
	}

	// Event payload — small, query-friendly. Don't emit args/results
	// here (PII risk + size); audit table holds the full row already.
	payload, _ := json.Marshal(map[string]any{
		"install_id":  inv.InstallID,
		"identifier":  inv.Identifier,
		"action":      inv.Action,
		"caller":      inv.Caller,
		"status":      inv.Status,
		"duration_ms": inv.DurationMs,
		"error_code":  inv.ErrorCode,
	})
	scope := "install:" + inv.InstallID
	actorType := "agent"
	if inv.Caller == "user" {
		actorType = "user"
	} else if inv.Caller == "webhook" {
		actorType = "webhook"
	} else if inv.Caller == "scheduler" {
		actorType = "scheduler"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.events
			(scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, 'app.action_invoked', $4)
	`, scope, actorType, inv.CallerID, payload); err != nil {
		return fmt.Errorf("apptools: insert event: %w", err)
	}

	return tx.Commit(ctx)
}

// NoopRecorder swallows every record call. Use in tests where the
// schema isn't applied or the audit assertions aren't relevant.
type NoopRecorder struct{}

func (NoopRecorder) Record(_ context.Context, _ InvocationRecord) error { return nil }
