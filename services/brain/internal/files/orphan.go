// Orphan scan — finds `ready` rows no domain references anymore.
//
// 设计文档: docs/BiuMind-Notes-Design-Draft.md §5 M4
// （note_attachments 就是给这里的孤儿判定用的）。
//
// Unlike the pending sweep (cleanup.go), a ready row may still be
// referenced by business data, so "old" alone isn't enough. A ready row
// counts as an orphan only when ALL referencing domains have let go:
//
//   - brain.note_attachments (is_associated=true — the note body still
//     carries a `biu-file://<uuid>` reference, reconciled on every write)
//   - brain.wiki_sources.file_id (wiki 项目来源文件)
//   - chat.messages content/parts containing the file id (聊天附件，
//     引用形式是消息 JSON 里的 file URL/UUID，无法做外键，用文本兜底)
//
// The scan is exposed as RunOrphanScan(ctx, dryRun). dryRun=true only
// counts and lists candidates — nothing is removed from MinIO or
// Postgres. dryRun=false removes the blob first, then the row (same
// order as the pending sweep, so a transient blob error never strands
// an unreachable object).
//
// Wired into the periodic Run loop (OrphanInterval, default 24h, live
// sweep). dryRun=true remains available for manual/ops inspection.

package files

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// OrphanCandidate — one ready row with no surviving domain reference.
type OrphanCandidate struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ObjectKey string
	SizeBytes int64
	Source    string
	CreatedAt time.Time
}

// OrphanScanResult — what one scan found (and did). Candidates is always
// populated (capped by BatchSize) so a dry-run report is inspectable;
// the deletion counters stay 0 unless dryRun=false.
type OrphanScanResult struct {
	DryRun       bool
	Candidates   []OrphanCandidate
	BlobsRemoved int64
	RowsDeleted  int64
	BlobErrors   int64
	StoreErrors  int64
}

// RunOrphanScan — single orphan sweep. dryRun=true: scan + report only,
// guaranteed no writes. dryRun=false: blob remove → row hard delete per
// candidate (failures skip the row delete so a later sweep can retry).
func (w *CleanupWorker) RunOrphanScan(ctx context.Context, dryRun bool) (*OrphanScanResult, error) {
	cutoff := time.Now().Add(-w.cfg.OrphanTTL)
	cands, err := w.scanOrphans(ctx, cutoff, w.cfg.BatchSize)
	if err != nil {
		return nil, err
	}
	res := &OrphanScanResult{DryRun: dryRun, Candidates: cands}
	if dryRun {
		w.cfg.Logger.Info("files orphan scan (dry-run)",
			"candidates", len(cands), "orphan_ttl", w.cfg.OrphanTTL)
		return res, nil
	}
	for _, c := range cands {
		before := w.stats
		w.cleanupOne(ctx, pendingRow{ID: c.ID, UserID: c.UserID, ObjectKey: c.ObjectKey})
		res.BlobsRemoved += w.stats.BlobsRemoved - before.BlobsRemoved
		res.RowsDeleted += w.stats.RowsDeleted - before.RowsDeleted
		res.BlobErrors += w.stats.BlobErrors - before.BlobErrors
		res.StoreErrors += w.stats.StoreErrors - before.StoreErrors
	}
	if len(cands) > 0 {
		w.cfg.Logger.Info("files orphan sweep",
			"candidates", len(cands),
			"blobs_removed", res.BlobsRemoved,
			"rows_deleted", res.RowsDeleted)
	}
	return res, nil
}

func (w *CleanupWorker) scanOrphans(ctx context.Context, cutoff time.Time, limit int) ([]OrphanCandidate, error) {
	const q = `
SELECT o.id, o.user_id, o.object_key, o.size_bytes, o.source, o.created_at
  FROM files.objects o
 WHERE o.status = 'ready'
   AND o.deleted_at IS NULL
   AND o.created_at < $1
   AND NOT EXISTS (SELECT 1 FROM brain.note_attachments a
                    WHERE a.file_id = o.id AND a.is_associated)
   AND NOT EXISTS (SELECT 1 FROM brain.wiki_sources ws
                    WHERE ws.file_id = o.id)
   AND NOT EXISTS (SELECT 1 FROM chat.messages m
                    WHERE m.content LIKE '%' || o.id::text || '%'
                       OR m.parts::text LIKE '%' || o.id::text || '%')
 ORDER BY o.created_at
 LIMIT $2
`
	rows, err := w.pool.Query(ctx, q, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrphanCandidate{}
	for rows.Next() {
		var c OrphanCandidate
		if err := rows.Scan(&c.ID, &c.UserID, &c.ObjectKey, &c.SizeBytes, &c.Source, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
