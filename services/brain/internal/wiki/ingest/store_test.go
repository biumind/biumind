// Ingest store tests against real Postgres（同 wiki/store 测试惯例）。
// Skips when DATABASE_URL unset.

package ingest

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ingestTestHarness struct {
	pool  *pgxpool.Pool
	st    *Store
	owner uuid.UUID
	pid   uuid.UUID
}

func newIngestTestHarness(t *testing.T) *ingestTestHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	h := &ingestTestHarness{pool: pool, st: New(pool), owner: uuid.New()}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		h.owner, "ingest-store-test").Scan(&h.pid); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, h.pid); err != nil {
			t.Logf("cleanup project: %v", err)
		}
		pool.Close()
	})
	return h
}

func (h *ingestTestHarness) createTask(t *testing.T) *Task {
	t.Helper()
	task, err := h.st.Create(context.Background(), CreateInput{
		ProjectID: h.pid, OwnerID: h.owner,
		RawText: "hello", Title: "t",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return task
}

func TestStore_RetryFailedTask(t *testing.T) {
	h := newIngestTestHarness(t)
	task := h.createTask(t)

	// 跑起来 → 失败终态（带 requeue_count + error + 时间戳）。
	if err := h.st.MarkRunning(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Requeue(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MarkTerminal(context.Background(), task.ID, StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}

	rt, err := h.st.Retry(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if rt.Status != StatusPending {
		t.Errorf("status = %q, want pending", rt.Status)
	}
	if rt.Error != "" {
		t.Errorf("error = %q, want empty", rt.Error)
	}
	if rt.CancelRequestedAt != nil || rt.StartedAt != nil || rt.FinishedAt != nil {
		t.Errorf("timestamps not cleared: %+v", rt)
	}
	if got := rt.RequeueCount(); got != 0 {
		t.Errorf("requeue_count = %d, want 0 (manual retry resets poison counter)", got)
	}
}

func TestStore_RetryRejectsNonTerminalAndDone(t *testing.T) {
	h := newIngestTestHarness(t)

	pending := h.createTask(t)
	if _, err := h.st.Retry(context.Background(), pending.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("retry pending: expected ErrNotFound, got %v", err)
	}

	doneTask := h.createTask(t)
	if err := h.st.MarkRunning(context.Background(), doneTask.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MarkTerminal(context.Background(), doneTask.ID, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Retry(context.Background(), doneTask.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("retry done: expected ErrNotFound, got %v", err)
	}
}

func TestStore_SweepCancelRequested(t *testing.T) {
	h := newIngestTestHarness(t)
	stale := h.createTask(t)
	fresh := h.createTask(t)

	for _, id := range []uuid.UUID{stale.ID, fresh.ID} {
		if err := h.st.RequestCancel(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	// stale 的取消请求拨回 1 小时前；fresh 保持现在。
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE brain.ingest_tasks SET cancel_requested_at = now() - interval '1 hour' WHERE id = $1`,
		stale.ID); err != nil {
		t.Fatal(err)
	}

	n, err := h.st.SweepCancelRequested(context.Background(), time.Now().Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("SweepCancelRequested: %v", err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	got, err := h.st.Get(context.Background(), stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCancelled {
		t.Errorf("stale status = %q, want cancelled", got.Status)
	}
	got, err = h.st.Get(context.Background(), fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Errorf("fresh status = %q, want pending (not yet stale)", got.Status)
	}
}
