// Orphan scan tests against real Postgres + MinIO. Skips when the
// integration env isn't wired (same convention as cleanup_test).

package files

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makeReady — seed a ready row (aged via SQL) + a real blob. Returns id.
func makeReady(t *testing.T, h *apiHarness, uid uuid.UUID, age time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	objectKey := "test-orphan/" + id.String()
	mime := "image/png"
	q := `INSERT INTO files.objects (
	  id, user_id, sha256, size_bytes, mime_type, bucket, object_key,
	  source, status, metadata, created_at
	) VALUES ($1, $2, $3, 12, $4, $5, $6, 'note-attachment', 'ready', '{}'::jsonb, now() - $7::interval)`
	if _, err := h.pool.Exec(context.Background(), q,
		id, uid, id.String(), mime, h.blob.Bucket(), objectKey, ageInterval(age),
	); err != nil {
		t.Fatalf("seed ready row: %v", err)
	}
	body := []byte("orphan-bytes")
	if err := h.blob.Put(context.Background(), objectKey, bytes.NewReader(body),
		int64(len(body)), mime); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	t.Cleanup(func() {
		// 兜底清理（测试失败时别留脏数据）。
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM files.objects WHERE id = $1`, id)
		_ = h.blob.Remove(context.Background(), objectKey)
	})
	return id
}

func newTestOrphanWorker(h *apiHarness) *CleanupWorker {
	return NewCleanupWorker(h.pool, h.blob, CleanupConfig{
		Interval:   time.Minute,
		PendingTTL: time.Hour,
		OrphanTTL:  24 * time.Hour, // 最小允许值；测试行都按 48h 以上 aging
		BatchSize:  100,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func hasCandidate(res *OrphanScanResult, id uuid.UUID) bool {
	for _, c := range res.Candidates {
		if c.ID == id {
			return true
		}
	}
	return false
}

func TestOrphanScan_DryRunListsButDeletesNothing(t *testing.T) {
	h := newAPIHarness(t)
	t.Cleanup(h.close)
	uid := uuid.New()
	id := makeReady(t, h, uid, 48*time.Hour)
	objectKey := "test-orphan/" + id.String()

	w := newTestOrphanWorker(h)
	res, err := w.RunOrphanScan(context.Background(), true)
	if err != nil {
		t.Fatalf("RunOrphanScan(dry): %v", err)
	}
	if !res.DryRun {
		t.Errorf("result should be marked dry-run")
	}
	if !hasCandidate(res, id) {
		t.Errorf("unreferenced old ready row should be listed as orphan candidate")
	}
	if res.RowsDeleted != 0 || res.BlobsRemoved != 0 {
		t.Errorf("dry-run must not delete anything: %+v", res)
	}
	if !rowExists(t, h, id) {
		t.Errorf("dry-run deleted the row")
	}
	if !blobExists(t, h, objectKey) {
		t.Errorf("dry-run removed the blob")
	}
}

func TestOrphanScan_ExcludesReferencedRows(t *testing.T) {
	h := newAPIHarness(t)
	t.Cleanup(h.close)
	uid := uuid.New()
	ctx := context.Background()

	// 1) 被 note_attachments(is_associated=true) 引用 → 不算孤儿。
	noteRef := makeReady(t, h, uid, 48*time.Hour)
	noteID := uuid.New()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO brain.note_notes (id, user_id, title, content_md) VALUES ($1, $2, 'n', 'x')`,
		noteID, uid); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	defer func() {
		_, _ = h.pool.Exec(ctx, `DELETE FROM brain.note_notes WHERE id = $1`, noteID)
	}()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO brain.note_attachments (note_id, file_id, is_associated) VALUES ($1, $2, true)`,
		noteID, noteRef); err != nil {
		t.Fatalf("seed note attachment: %v", err)
	}

	// 2) 被 wiki_sources.file_id 引用 → 不算孤儿。
	wikiRef := makeReady(t, h, uid, 48*time.Hour)
	projectID := uuid.New()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO brain.projects (id, owner_id, name) VALUES ($1, $2, 'p')`,
		projectID, uid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	defer func() {
		_, _ = h.pool.Exec(ctx, `DELETE FROM brain.projects WHERE id = $1`, projectID)
	}()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO brain.wiki_sources (project_id, file_id, rel_path, filename) VALUES ($1, $2, $3, 'f.png')`,
		projectID, wikiRef, "assets/"+wikiRef.String()); err != nil {
		t.Fatalf("seed wiki source: %v", err)
	}

	// 3) is_associated=false 的行不算引用（笔记曾引用但已解除）。
	unassoc := makeReady(t, h, uid, 48*time.Hour)
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO brain.note_attachments (note_id, file_id, is_associated) VALUES ($1, $2, false)`,
		noteID, unassoc); err != nil {
		t.Fatalf("seed unassociated attachment: %v", err)
	}

	// 4) 太新的 ready 行（TTL 内）→ 不算孤儿。
	fresh := makeReady(t, h, uid, time.Hour)

	w := newTestOrphanWorker(h)
	res, err := w.RunOrphanScan(context.Background(), true)
	if err != nil {
		t.Fatalf("RunOrphanScan(dry): %v", err)
	}
	if hasCandidate(res, noteRef) {
		t.Errorf("note-referenced file must not be an orphan candidate")
	}
	if hasCandidate(res, wikiRef) {
		t.Errorf("wiki-referenced file must not be an orphan candidate")
	}
	if hasCandidate(res, fresh) {
		t.Errorf("fresh file (within TTL) must not be an orphan candidate")
	}
	if !hasCandidate(res, unassoc) {
		t.Errorf("is_associated=false should NOT protect a file from orphan listing")
	}
}

func TestOrphanScan_ExecuteDeletesOrphanOnly(t *testing.T) {
	h := newAPIHarness(t)
	t.Cleanup(h.close)
	uid := uuid.New()
	orphan := makeReady(t, h, uid, 48*time.Hour)
	orphanKey := "test-orphan/" + orphan.String()

	w := newTestOrphanWorker(h)
	// 保护：sweep 没有 user 过滤，dev 库里若已有别的孤儿行（非本测试
	// 种子），拒绝执行破坏性删除——dry-run 路径已由上面的测试覆盖。
	dry, err := w.RunOrphanScan(context.Background(), true)
	if err != nil {
		t.Fatalf("RunOrphanScan(dry): %v", err)
	}
	for _, c := range dry.Candidates {
		if c.ID != orphan {
			t.Skipf("pre-existing orphan rows in DB (e.g. %s) — refusing destructive sweep", c.ID)
		}
	}
	res, err := w.RunOrphanScan(context.Background(), false)
	if err != nil {
		t.Fatalf("RunOrphanScan(exec): %v", err)
	}
	if !hasCandidate(res, orphan) {
		t.Fatalf("orphan not found: %+v", res)
	}
	if rowExists(t, h, orphan) {
		t.Errorf("executed sweep should delete the orphan row")
	}
	if blobExists(t, h, orphanKey) {
		t.Errorf("executed sweep should remove the orphan blob")
	}
	if res.RowsDeleted < 1 || res.BlobsRemoved < 1 {
		t.Errorf("expected deletion counters, got %+v", res)
	}
}
