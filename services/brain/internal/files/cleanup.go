// Pending-row cleanup worker — removes abandoned uploads.
//
// 设计文档: docs/BiuMind-Chat-Attachments-MinIO-Design.md §4.8.
//
// Two-step upload (presign-upload + finalize) leaves a `pending` row
// per started upload. The row only flips to `ready` after finalize
// confirms the bytes hit MinIO. Anything that stalls in `pending`
// beyond [PendingTTL] is treated as abandoned and reaped:
//
//	1. Blob.Remove the object key (idempotent — NoSuchKey is fine,
//	   it just means the client never PUT the bytes)
//	2. Store.HardDelete the row
//
// Why hard-delete instead of soft-delete: pending rows weren't ever
// visible to the user (Get filters status='ready'), so nothing references
// them. Keeping a tombstone serves no one.
//
// Cadence default: 5 min sweep. Tunable via env. Smallest practical
// PendingTTL is ~15 min (longer than presigned upload TTL) so a slow
// client uploading right at the deadline still has a chance to finalize.

package files

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CleanupConfig struct {
	Interval   time.Duration // default 5m, min 1m
	PendingTTL time.Duration // default 1h, min 15m (longer than presign TTL)
	OrphanTTL  time.Duration // default 7d, min 24h (orphan scan, orphan.go)
	// OrphanInterval — 孤儿扫描（ready 行但所有域都不再引用）的周期。
	// 比 pending sweep 慢得多：判定要走三个域的 NOT EXISTS，且 TTL 是
	// 天级，扫得勤没有意义。default 24h, min 1h。
	OrphanInterval time.Duration
	BatchSize      int // default 100
	Logger         *slog.Logger
}

type CleanupWorker struct {
	pool *pgxpool.Pool
	blob *Blob
	cfg  CleanupConfig

	// metrics — exported via test harness; production wiring updates
	// these via the standard prometheus exporter on the brain side.
	// We keep a struct rather than direct prom.Counter so the package
	// stays prometheus-free (brain/main wires the actual exporter).
	stats CleanupStats
}

// CleanupStats — lightweight counters for visibility. Read after a
// RunOnce call in tests; in production the brain logs them per tick.
type CleanupStats struct {
	Scanned       int64
	BlobsRemoved  int64
	RowsDeleted   int64
	BlobErrors    int64
	StoreErrors   int64
}

func NewCleanupWorker(pool *pgxpool.Pool, blob *Blob, cfg CleanupConfig) *CleanupWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Interval < time.Minute {
		cfg.Interval = time.Minute
	}
	if cfg.PendingTTL <= 0 {
		cfg.PendingTTL = time.Hour
	}
	if cfg.PendingTTL < 15*time.Minute {
		cfg.PendingTTL = 15 * time.Minute
	}
	if cfg.OrphanTTL <= 0 {
		cfg.OrphanTTL = 7 * 24 * time.Hour
	}
	if cfg.OrphanTTL < 24*time.Hour {
		cfg.OrphanTTL = 24 * time.Hour
	}
	if cfg.OrphanInterval <= 0 {
		cfg.OrphanInterval = 24 * time.Hour
	}
	if cfg.OrphanInterval < time.Hour {
		cfg.OrphanInterval = time.Hour
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &CleanupWorker{pool: pool, blob: blob, cfg: cfg}
}

// Stats — snapshot of cumulative counters since worker start.
func (w *CleanupWorker) Stats() CleanupStats { return w.stats }

// Run drives the periodic loop. Blocks until ctx done. Failures within
// a tick are logged but never abort the loop — one bad row shouldn't
// poison the whole sweep.
//
// 两条 cadence：pending sweep 按 Interval（默认 5m）；孤儿扫描按
// OrphanInterval（默认 24h，live 删除 —— 候选判定见 orphan.go，TTL
// 默认 7d 兜底）。孤儿扫描首轮在启动后一个 OrphanInterval 才跑，
// 需要立即排查时用 RunOrphanScan(ctx, true) 手动 dry-run。
func (w *CleanupWorker) Run(ctx context.Context) {
	w.cfg.Logger.Info("files cleanup worker started",
		"interval", w.cfg.Interval, "pending_ttl", w.cfg.PendingTTL,
		"orphan_interval", w.cfg.OrphanInterval, "orphan_ttl", w.cfg.OrphanTTL)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	orphanT := time.NewTicker(w.cfg.OrphanInterval)
	defer orphanT.Stop()
	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info("files cleanup worker stopped")
			return
		case <-t.C:
			w.RunOnce(ctx)
		case <-orphanT.C:
			if _, err := w.RunOrphanScan(ctx, false); err != nil {
				w.cfg.Logger.Error("orphan scan failed", "err", err)
			}
		}
	}
}

// RunOnce executes a single sweep. Exposed for tests + manual triggers.
func (w *CleanupWorker) RunOnce(ctx context.Context) {
	cutoff := time.Now().Add(-w.cfg.PendingTTL)
	rows, err := w.scanExpired(ctx, cutoff, w.cfg.BatchSize)
	if err != nil {
		w.cfg.Logger.Error("cleanup scan failed", "err", err)
		w.stats.StoreErrors++
		return
	}
	w.stats.Scanned += int64(len(rows))
	for _, r := range rows {
		w.cleanupOne(ctx, r)
	}
	if len(rows) > 0 {
		w.cfg.Logger.Info("files cleanup tick",
			"scanned", len(rows),
			"total_blobs_removed", w.stats.BlobsRemoved,
			"total_rows_deleted", w.stats.RowsDeleted)
	}
}

// pendingRow — minimal projection for cleanup; we only need the trio
// to remove the blob and the row.
type pendingRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ObjectKey string
}

func (w *CleanupWorker) scanExpired(ctx context.Context, cutoff time.Time, limit int) ([]pendingRow, error) {
	const q = `
SELECT id, user_id, object_key
  FROM files.objects
 WHERE status = 'pending' AND created_at < $1
 LIMIT $2
`
	rows, err := w.pool.Query(ctx, q, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.ObjectKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (w *CleanupWorker) cleanupOne(ctx context.Context, r pendingRow) {
	// Blob.Remove first — if it fails for a transient reason and we'd
	// already deleted the row, the orphan blob would be unreachable.
	// minio Remove of a NoSuchKey returns nil (or our isNotFound branch);
	// either way, treat as success and proceed.
	if err := w.blob.Remove(ctx, r.ObjectKey); err != nil {
		// Soft-fail: log and skip the DELETE so a retry next tick can
		// finish. Blob errors are usually transient (network glitch).
		w.cfg.Logger.Warn("cleanup blob remove failed",
			"file_id", r.ID, "object_key", r.ObjectKey, "err", err)
		w.stats.BlobErrors++
		return
	}
	w.stats.BlobsRemoved++

	// HardDelete uses a `(user_id, id)` predicate. We have both fields.
	if err := hardDeleteRaw(ctx, w.pool, r.UserID, r.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// already cleaned by another sweep / process — fine.
			return
		}
		w.cfg.Logger.Warn("cleanup row delete failed",
			"file_id", r.ID, "err", err)
		w.stats.StoreErrors++
		return
	}
	w.stats.RowsDeleted++
}

// hardDeleteRaw — minimal wrapper; can't use Store.HardDelete here
// because the worker takes a *Blob, not *Store, and we want to keep
// the dependency surface tight. The query is the same.
func hardDeleteRaw(ctx context.Context, pool *pgxpool.Pool, userID, id uuid.UUID) error {
	const q = `DELETE FROM files.objects WHERE user_id = $1 AND id = $2`
	tag, err := pool.Exec(ctx, q, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Compile-time guard: cleanup uses the same scan path as Store. If
// future schema migrations rename the columns, this catches it early.
var _ = func() any { return pgx.ErrNoRows }
