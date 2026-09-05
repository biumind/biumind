// Research store delete/rerun integration tests against real Postgres
// (同 ingest/store_test.go 惯例)。Skips when DATABASE_URL unset.

package research

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type researchTestHarness struct {
	pool  *pgxpool.Pool
	st    *Store
	owner uuid.UUID
	pid   uuid.UUID
}

func newResearchTestHarness(t *testing.T) *researchTestHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	h := &researchTestHarness{pool: pool, st: New(pool), owner: uuid.New()}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		h.owner, "research-store-test").Scan(&h.pid); err != nil {
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

func (h *researchTestHarness) createTask(t *testing.T) *Task {
	t.Helper()
	task, err := h.st.Create(context.Background(), h.pid, h.owner, "topic", []string{"q1"}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return task
}

func TestIsActiveStatus(t *testing.T) {
	for _, s := range []string{StatusQueued, StatusSearching, StatusSynthesizing, StatusSaving} {
		if !IsActiveStatus(s) {
			t.Errorf("IsActiveStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{StatusDone, StatusError, "cancelled", ""} {
		if IsActiveStatus(s) {
			t.Errorf("IsActiveStatus(%q) = true, want false", s)
		}
	}
}

// TestStore_Delete — active 任务拒删（条件 DELETE 0 行），terminal 任务
// 硬删成功且行确实消失。
func TestStore_Delete(t *testing.T) {
	h := newResearchTestHarness(t)
	ctx := context.Background()

	active := h.createTask(t) // queued
	ok, err := h.st.Delete(ctx, active.ID)
	if err != nil {
		t.Fatalf("Delete active: %v", err)
	}
	if ok {
		t.Fatal("Delete(active) = true, want false")
	}
	if _, err := h.st.Get(ctx, active.ID); err != nil {
		t.Fatalf("active task should still exist: %v", err)
	}

	done := h.createTask(t)
	if err := h.st.Fail(ctx, done.ID, "boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	ok, err = h.st.Delete(ctx, done.ID)
	if err != nil {
		t.Fatalf("Delete done: %v", err)
	}
	if !ok {
		t.Fatal("Delete(done) = false, want true")
	}
	if _, err := h.st.Get(ctx, done.ID); err != ErrNotFound {
		t.Fatalf("deleted task should be gone, got err=%v", err)
	}
}

// TestStore_ResetForRerun — terminal 任务被重置回 queued：相位产出清空、
// topic/queries 保留、旧页的 research_taskid 标记被剥掉（savePage 的
// dup guard 不会再挂回旧页）；重置后再次 reset / active 任务 reset 都
// 返回 false（并发 rerun 收敛为一个）。
func TestStore_ResetForRerun(t *testing.T) {
	h := newResearchTestHarness(t)
	ctx := context.Background()

	task := h.createTask(t)
	if err := h.st.SaveWebResults(ctx, task.ID, []WebHit{{Title: "x", URL: "https://x"}}); err != nil {
		t.Fatalf("SaveWebResults: %v", err)
	}
	if err := h.st.AppendSynthesis(ctx, task.ID, "## body"); err != nil {
		t.Fatalf("AppendSynthesis: %v", err)
	}
	// 旧产出页：带 research_taskid 标记的 wiki 页。
	var pageID uuid.UUID
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO brain.pages (project_id, title, frontmatter)
		VALUES ($1, 'Research: topic', jsonb_build_object('research_taskid', $2::text, 'origin', 'deep-research'))
		RETURNING id
	`, h.pid, task.ID.String()).Scan(&pageID); err != nil {
		t.Fatalf("insert old page: %v", err)
	}
	if err := h.st.Complete(ctx, task.ID, pageID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	ok, err := h.st.ResetForRerun(ctx, task.ID)
	if err != nil {
		t.Fatalf("ResetForRerun: %v", err)
	}
	if !ok {
		t.Fatal("ResetForRerun(done) = false, want true")
	}

	got, err := h.st.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.PageID != nil || len(got.WebResults) != 0 || got.Synthesis != "" ||
		got.ErrorMessage != "" || got.StartedAt != nil || got.FinishedAt != nil {
		t.Errorf("outputs not cleared: %+v", got)
	}
	if got.Topic != "topic" || len(got.Queries) != 1 || got.Queries[0] != "q1" {
		t.Errorf("topic/queries not preserved: %+v", got)
	}

	// 旧页保留但 marker 已剥掉 —— savePage 的 FindPageByTaskID 不再命中。
	var marker *string
	if err := h.pool.QueryRow(ctx,
		`SELECT frontmatter->>'research_taskid' FROM brain.pages WHERE id = $1`,
		pageID).Scan(&marker); err != nil {
		t.Fatalf("read old page: %v", err)
	}
	if marker != nil {
		t.Errorf("research_taskid marker = %v, want stripped (NULL)", *marker)
	}
	if p, err := h.st.FindPageByTaskID(ctx, h.pid, task.ID); err != nil || p != nil {
		t.Errorf("FindPageByTaskID = %v, %v; want nil, nil", p, err)
	}

	// 已重置回 queued（active）→ 再 reset 失败：同任务并发重跑收敛。
	ok, err = h.st.ResetForRerun(ctx, task.ID)
	if err != nil {
		t.Fatalf("second ResetForRerun: %v", err)
	}
	if ok {
		t.Fatal("ResetForRerun(queued) = true, want false")
	}
}

// TestStore_FindPageByTaskIDSkipsDeleted — 软删的产出页不参与 crash
// recover 的 dup guard（recover re-run 应另写新页）。
func TestStore_FindPageByTaskIDSkipsDeleted(t *testing.T) {
	h := newResearchTestHarness(t)
	ctx := context.Background()
	task := h.createTask(t)

	var pageID uuid.UUID
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO brain.pages (project_id, title, frontmatter)
		VALUES ($1, 'Research: x', jsonb_build_object('research_taskid', $2::text))
		RETURNING id
	`, h.pid, task.ID.String()).Scan(&pageID); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	p, err := h.st.FindPageByTaskID(ctx, h.pid, task.ID)
	if err != nil || p == nil || *p != pageID {
		t.Fatalf("FindPageByTaskID = %v, %v; want %s", p, err, pageID)
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE brain.pages SET deleted_at = now() WHERE id = $1`, pageID); err != nil {
		t.Fatalf("soft-delete page: %v", err)
	}
	p, err = h.st.FindPageByTaskID(ctx, h.pid, task.ID)
	if err != nil || p != nil {
		t.Errorf("FindPageByTaskID after soft-delete = %v, %v; want nil, nil", p, err)
	}
}
