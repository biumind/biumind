// Cleanup worker tests against real Postgres + MinIO. Skips when the
// integration env isn't wired (same convention as api_test).

package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makePending — write a pending row + a real MinIO object so a sweep
// has something concrete to clean. Returns the row id.
func makePending(t *testing.T, h *apiHarness, uid uuid.UUID, age time.Duration, withBlob bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	objectKey := "test-pending/" + id.String()
	mime := "image/png"

	// Seed a row that already has an old created_at via SQL so we can
	// "age" it without sleeping the test. sha256 must be unique per row
	// (unique index files_objects_user_sha256_alive). Pending rows with
	// no real bytes get a synthetic per-row marker — production never
	// sets sha256 on pending; here we only need it for the seed schema.
	syntheticSha := id.String()
	q := `INSERT INTO files.objects (
	  id, user_id, sha256, size_bytes, mime_type, bucket, object_key,
	  source, status, metadata, created_at
	) VALUES ($1, $2, $3, 0, $4, $5, $6, 'chat-attachment', 'pending', '{}'::jsonb, now() - $7::interval)`
	if _, err := h.pool.Exec(context.Background(), q,
		id, uid, syntheticSha, mime, h.blob.Bucket(), objectKey,
		ageInterval(age),
	); err != nil {
		t.Fatalf("seed pending row: %v", err)
	}

	if withBlob {
		body := []byte("orphan-bytes")
		if err := h.blob.Put(context.Background(), objectKey, bytes.NewReader(body),
			int64(len(body)), mime); err != nil {
			t.Fatalf("seed blob: %v", err)
		}
	}
	return id
}

// ageInterval — Postgres interval literal, e.g. "2 hours".
func ageInterval(d time.Duration) string {
	// Use seconds for portability; Postgres parses '<n> seconds' fine.
	secs := int64(d.Seconds())
	return formatDur(secs)
}

func formatDur(secs int64) string {
	// Avoid fmt for this trivial case to keep test deps minimal.
	if secs == 0 {
		return "0 seconds"
	}
	// We allow negative — Postgres handles "now() - '-1 hour'::interval"
	// as future time, so callers using negative ages get rows that look
	// "fresh" (within TTL) and shouldn't be reaped.
	out := []byte{}
	if secs < 0 {
		out = append(out, '-')
		secs = -secs
	}
	// itoa
	digits := []byte{}
	for secs > 0 {
		digits = append([]byte{byte('0' + secs%10)}, digits...)
		secs /= 10
	}
	if len(digits) == 0 {
		digits = []byte{'0'}
	}
	out = append(out, digits...)
	out = append(out, []byte(" seconds")...)
	return string(out)
}

func newTestCleanupWorker(t *testing.T, h *apiHarness, ttl time.Duration) *CleanupWorker {
	t.Helper()
	return NewCleanupWorker(h.pool, h.blob, CleanupConfig{
		Interval:   time.Minute, // unused — we drive RunOnce
		PendingTTL: ttl,
		BatchSize:  100,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// rowExists — quick existence check on files.objects.
func rowExists(t *testing.T, h *apiHarness, id uuid.UUID) bool {
	t.Helper()
	var n int
	err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM files.objects WHERE id = $1`, id).Scan(&n)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n > 0
}

// blobExists — checks MinIO directly via Head.
func blobExists(t *testing.T, h *apiHarness, key string) bool {
	t.Helper()
	_, err := h.blob.Head(context.Background(), key)
	if err == nil {
		return true
	}
	if errors.Is(err, ErrNotFound) {
		return false
	}
	t.Fatalf("blob head: %v", err)
	return false
}

// ─── Tests ────────────────────────────────────────────────

func TestCleanup_RemovesExpiredPendingRow(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	id := makePending(t, h, uid, 2*time.Hour, true) // 2h old, with blob

	w := newTestCleanupWorker(t, h, time.Hour)
	w.RunOnce(context.Background())

	if rowExists(t, h, id) {
		t.Errorf("pending row should be deleted")
	}
	// Stats: at least 1 (this test's row). Other tests / prior runs may
	// have left pending rows that get swept too — the worker's job is
	// to clean ALL expired pending, not just ours.
	stats := w.Stats()
	if stats.RowsDeleted < 1 || stats.BlobsRemoved < 1 {
		t.Errorf("expected at least 1 deletion, got %+v", stats)
	}
}

func TestCleanup_KeepsFreshPendingRow(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	id := makePending(t, h, uid, 5*time.Minute, false)

	w := newTestCleanupWorker(t, h, time.Hour)
	w.RunOnce(context.Background())

	// Fresh row must survive regardless of whatever other expired rows
	// the sweep may have processed.
	if !rowExists(t, h, id) {
		t.Errorf("fresh pending row was deleted prematurely")
	}
	// cleanup
	_, _ = h.pool.Exec(context.Background(),
		"DELETE FROM files.objects WHERE id = $1", id)
}

func TestCleanup_KeepsReadyRowsRegardlessOfAge(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	// A ready row that's 24h old must NOT be touched by pending GC.
	r := h.uploadBytes(t, h.mintToken(uid), "x.png", "image/png",
		[]byte("ready-content-"+uuid.New().String()), nil)
	defer r.body.Close()
	if r.status != 200 {
		t.Fatalf("upload: %d %s", r.status, r.bodyStr)
	}
	fileID := r.json["id"].(string)
	id := uuid.MustParse(fileID)
	defer h.cleanupFile(uid, fileID)

	// Force created_at backwards.
	_, err := h.pool.Exec(context.Background(),
		`UPDATE files.objects SET created_at = now() - interval '48 hours'
		 WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("age row: %v", err)
	}

	w := newTestCleanupWorker(t, h, time.Hour)
	w.RunOnce(context.Background())

	if !rowExists(t, h, id) {
		t.Errorf("ready row was incorrectly deleted by pending GC")
	}
}

func TestCleanup_BlobMissingStillDeletesRow(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	// pending row, no actual blob (client never PUT bytes).
	id := makePending(t, h, uid, 2*time.Hour, false)
	w := newTestCleanupWorker(t, h, time.Hour)
	w.RunOnce(context.Background())

	// Row should still be deleted — Blob.Remove on NoSuchKey is nil.
	if rowExists(t, h, id) {
		t.Errorf("row should be deleted even when blob never existed")
	}
}

func TestCleanup_BlobActuallyRemovedFromMinIO(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	id := makePending(t, h, uid, 2*time.Hour, true)
	objectKey := "test-pending/" + id.String()

	if !blobExists(t, h, objectKey) {
		t.Fatalf("blob should exist before cleanup")
	}
	w := newTestCleanupWorker(t, h, time.Hour)
	w.RunOnce(context.Background())
	if blobExists(t, h, objectKey) {
		t.Errorf("blob still in MinIO after cleanup")
	}
}

func TestCleanup_BatchSize(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	const total = 5
	ids := make([]uuid.UUID, total)
	for i := 0; i < total; i++ {
		ids[i] = makePending(t, h, uid, 2*time.Hour, false)
	}

	w := NewCleanupWorker(h.pool, h.blob, CleanupConfig{
		Interval:   time.Minute,
		PendingTTL: time.Hour,
		BatchSize:  3, // smaller than total → first sweep partial
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	w.RunOnce(context.Background())
	// First sweep: at most BatchSize rows (3). Could be less if the
	// table happens to be lighter than that — what matters for the
	// batch-size contract is that we don't blow past the limit.
	if got := w.Stats().Scanned; got > 3 {
		t.Errorf("first sweep scanned %d, exceeds BatchSize=3", got)
	}
	// Multiple sweeps clear our 5 rows eventually.
	for i := 0; i < 5; i++ {
		w.RunOnce(context.Background())
	}
	for _, id := range ids {
		if rowExists(t, h, id) {
			t.Errorf("row %s survived all sweeps", id)
		}
	}
}
