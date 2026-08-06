// Postgres-backed Limiter — same Limiter contract as the in-memory
// implementation, but the count is stored in a shared table so multiple
// service replicas see the same budget.
//
// Schema (applied by ensureSchemaOnce):
//
//	CREATE TABLE quota_buckets (
//	    bucket       text       NOT NULL,
//	    key          text       NOT NULL,
//	    window_end   timestamptz NOT NULL,
//	    count        bigint     NOT NULL DEFAULT 0,
//	    PRIMARY KEY (bucket, key)
//	);
//
// On every CheckAndReserve a single CTE-pipelined statement does:
//  1. UPSERT a row, rolling window_end forward when expired.
//  2. Conditional UPDATE that adds n only if it still fits the limit.
//  3. Return the resulting count + window_end + whether the increment
//     succeeded.
//
// Trade-off vs in-memory:
//   - Adds a Postgres round-trip per request (typically <1 ms locally).
//   - Survives replica deaths and rebalancing — quota is real, not
//     "best effort across replicas".
//   - Refund is a simple UPDATE; clamped at zero so concurrent refunds
//     never go negative.
package quota

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PGStore is the database surface this limiter needs. *pgxpool.Pool
// satisfies it directly; tests may provide a stub.
type PGStore interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type pgLimiter struct {
	db    PGStore
	specs map[string]Spec
	now   func() time.Time

	once   sync.Once
	schema error
}

// NewPGLimiter — production constructor. Caller is responsible for
// opening the pgxpool. The schema is created lazily on the first
// CheckAndReserve call (idempotent CREATE TABLE IF NOT EXISTS).
func NewPGLimiter(db PGStore, specs map[string]Spec) Limiter {
	if specs == nil {
		specs = map[string]Spec{}
	}
	return &pgLimiter{db: db, specs: specs, now: time.Now}
}

// ensureSchemaOnce runs CREATE TABLE IF NOT EXISTS. Concurrent
// first-calls from sibling replicas can race on Postgres's pg_type
// catalog (a long-standing limitation: concurrent CREATE TABLE
// IF NOT EXISTS can throw "duplicate key violates
// pg_type_typname_nsp_index" or "tuple concurrently updated" even
// though the statement is idempotent). We catch those races and
// re-verify the table exists.
func (l *pgLimiter) ensureSchemaOnce(ctx context.Context) error {
	l.once.Do(func() {
		_, err := l.db.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS quota_buckets (
				bucket     text       NOT NULL,
				key        text       NOT NULL,
				window_end timestamptz NOT NULL,
				count      bigint     NOT NULL DEFAULT 0,
				PRIMARY KEY (bucket, key)
			)`)
		if err != nil && isConcurrentSchemaRace(err) {
			// Sibling replica won the create; verify the table is now
			// present and treat the race as benign.
			var exists bool
			verr := l.db.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.tables
					WHERE table_name = 'quota_buckets'
				)`).Scan(&exists)
			if verr == nil && exists {
				err = nil
			}
		}
		l.schema = err
	})
	return l.schema
}

// isConcurrentSchemaRace recognises the Postgres error codes that
// indicate two sessions tried to create the same object at once.
func isConcurrentSchemaRace(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "23505", // unique_violation (e.g. pg_type catalog race)
		"42P07", // duplicate_table
		"XX000": // internal_error (sometimes used for catalog tuple races)
		return true
	}
	return false
}

func (l *pgLimiter) CheckAndReserve(bucket, key string, n int64) Decision {
	spec, ok := l.specs[bucket]
	if !ok {
		return Decision{Allow: true, Bucket: bucket}
	}
	// n=0 is "peek" — never mutates state.
	if n == 0 {
		return l.Snapshot(bucket, key)
	}
	// n exceeds the entire window's limit — can never fit.
	if n > spec.Limit {
		s := l.Snapshot(bucket, key)
		s.Allow = false
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := l.ensureSchemaOnce(ctx); err != nil {
		// Fail open — losing a quota check is preferable to losing the
		// whole request when Postgres is mid-restart.
		return Decision{Allow: true, Limit: spec.Limit, Bucket: bucket,
			Reset: l.now().Add(spec.Window)}
	}

	now := l.now()
	windowEnd := now.Add(spec.Window)

	// Single-statement atomic reserve. Always attempt INSERT VALUES
	// (..., count=n) so that ON CONFLICT fires when a row exists, and
	// fresh rows start at count=n. The WHERE on DO UPDATE filters out
	// rejections (count+n > limit AND window not yet expired) — when
	// it filters, RETURNING is empty and the scan hits ErrNoRows, which
	// signals "denied".
	var (
		gotCount  int64
		gotWindow time.Time
	)
	row := l.db.QueryRow(ctx, `
		INSERT INTO quota_buckets (bucket, key, window_end, count)
		VALUES ($1, $2, $3, $5)
		ON CONFLICT (bucket, key) DO UPDATE
			SET window_end = CASE
				WHEN quota_buckets.window_end < $4 THEN $3
				ELSE quota_buckets.window_end
			END,
			count = CASE
				WHEN quota_buckets.window_end < $4 THEN $5
				ELSE quota_buckets.count + $5
			END
			WHERE quota_buckets.window_end < $4
			   OR quota_buckets.count + $5 <= $6
		RETURNING count, window_end
	`, bucket, key, windowEnd, now, n, spec.Limit)

	err := row.Scan(&gotCount, &gotWindow)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Denied: the WHERE filtered out the update. Read current
			// state to populate Limit / Remaining / Reset for the
			// rejection.
			denied := l.Snapshot(bucket, key)
			denied.Allow = false
			return denied
		}
		// Any other error: fail open.
		return Decision{Allow: true, Limit: spec.Limit, Bucket: bucket,
			Reset: windowEnd}
	}

	remaining := spec.Limit - gotCount
	if remaining < 0 {
		remaining = 0
	}
	return Decision{
		Allow:     true,
		Limit:     spec.Limit,
		Remaining: remaining,
		Reset:     gotWindow,
		Bucket:    bucket,
	}
}

func (l *pgLimiter) Refund(bucket, key string, n int64) {
	if n <= 0 {
		return
	}
	if _, ok := l.specs[bucket]; !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.db.Exec(ctx, `
		UPDATE quota_buckets
		SET count = GREATEST(count - $3, 0)
		WHERE bucket = $1 AND key = $2`,
		bucket, key, n)
}

func (l *pgLimiter) Snapshot(bucket, key string) Decision {
	spec, ok := l.specs[bucket]
	if !ok {
		return Decision{Allow: true, Bucket: bucket}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.ensureSchemaOnce(ctx); err != nil {
		return Decision{Allow: true, Limit: spec.Limit, Bucket: bucket,
			Reset: l.now().Add(spec.Window)}
	}
	var (
		count  int64
		window time.Time
	)
	row := l.db.QueryRow(ctx,
		`SELECT count, window_end FROM quota_buckets WHERE bucket=$1 AND key=$2`,
		bucket, key)
	if err := row.Scan(&count, &window); err != nil {
		return Decision{
			Allow:     true,
			Limit:     spec.Limit,
			Remaining: spec.Limit,
			Reset:     l.now().Add(spec.Window),
			Bucket:    bucket,
		}
	}
	if l.now().After(window) {
		count = 0
		window = l.now().Add(spec.Window)
	}
	remaining := spec.Limit - count
	if remaining < 0 {
		remaining = 0
	}
	return Decision{
		Allow:     count < spec.Limit,
		Limit:     spec.Limit,
		Remaining: remaining,
		Reset:     window,
		Bucket:    bucket,
	}
}

// withClock — test helper to advance time deterministically.
func (l *pgLimiter) withClock(now func() time.Time) *pgLimiter {
	l.now = now
	return l
}
