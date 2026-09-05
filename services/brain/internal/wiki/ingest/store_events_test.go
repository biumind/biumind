// Ingest 任务事件（brain.events）集成测试：验证生命周期关键点的事件落库
// 且 payload 结构对齐 syncws 投影 + 客户端 ingest_stream_state.dart 的解析。
// Skips when DATABASE_URL unset.

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
	"github.com/google/uuid"
)

type brainEvent struct {
	id        int64
	eventType string
	payload   map[string]any
}

// fetchIngestEvents 拉本测试项目 scope 下所有 ingest_task.* 事件（按 id 序）。
func (h *ingestTestHarness) fetchIngestEvents(t *testing.T) []brainEvent {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `
		SELECT id, event_type, payload FROM brain.events
		WHERE scope = $1 AND event_type LIKE 'ingest_task.%'
		ORDER BY id
	`, fmt.Sprintf("wiki:project:%s", h.pid))
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	var out []brainEvent
	for rows.Next() {
		var e brainEvent
		var pl []byte
		if err := rows.Scan(&e.id, &e.eventType, &pl); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		if err := json.Unmarshal(pl, &e.payload); err != nil {
			t.Fatalf("payload json: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func eventsOfType(evs []brainEvent, typ string) []brainEvent {
	var out []brainEvent
	for _, e := range evs {
		if e.eventType == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestStore_CreateEmitsIngestTaskCreated(t *testing.T) {
	h := newIngestTestHarness(t)
	task := h.createTask(t)

	evs := eventsOfType(h.fetchIngestEvents(t), "ingest_task.created")
	if len(evs) != 1 {
		t.Fatalf("created events = %d, want 1", len(evs))
	}
	p := evs[0].payload
	if p["task_id"] != task.ID.String() {
		t.Errorf("task_id = %v, want %s", p["task_id"], task.ID)
	}
	if p["project_id"] != h.pid.String() {
		t.Errorf("project_id = %v, want %s", p["project_id"], h.pid)
	}
	if p["status"] != StatusPending {
		t.Errorf("status = %v, want pending", p["status"])
	}
}

func TestStore_LifecycleEvents(t *testing.T) {
	h := newIngestTestHarness(t)
	ctx := context.Background()
	task := h.createTask(t)

	// started：仅 pending→running 边沿发一次；重复 running 更新不重复发。
	if err := h.st.MarkRunning(ctx, task.ID, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MarkRunning(ctx, task.ID, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}

	// 两页流式落地。
	page1, page2 := uuid.New(), uuid.New()
	if err := h.st.AppendResultPage(ctx, task.ID, page1,
		map[string]any{"last_path": "a.md"}, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
	if err := h.st.AppendResultPage(ctx, task.ID, page2,
		map[string]any{"last_path": "b.md"}, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}

	if err := h.st.MarkTerminal(ctx, task.ID, StatusDone, "", "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}

	evs := h.fetchIngestEvents(t)

	started := eventsOfType(evs, "ingest_task.started")
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1 (idempotent running update must not re-emit)", len(started))
	}
	if started[0].payload["status"] != StatusRunning {
		t.Errorf("started status = %v, want running", started[0].payload["status"])
	}
	if started[0].payload["task_id"] != task.ID.String() {
		t.Errorf("started task_id = %v", started[0].payload["task_id"])
	}

	pages := eventsOfType(evs, "ingest_task.page")
	if len(pages) != 2 {
		t.Fatalf("page events = %d, want 2", len(pages))
	}
	if pages[0].payload["page_id"] != page1.String() {
		t.Errorf("page[0] page_id = %v, want %s", pages[0].payload["page_id"], page1)
	}
	if pages[0].payload["path"] != "a.md" {
		t.Errorf("page[0] path = %v, want a.md", pages[0].payload["path"])
	}
	if got := pages[1].payload["pages_done"]; got != float64(2) {
		t.Errorf("page[1] pages_done = %v, want 2", got)
	}
	if pages[1].payload["page_id"] != page2.String() {
		t.Errorf("page[1] page_id = %v, want %s", pages[1].payload["page_id"], page2)
	}

	done := eventsOfType(evs, "ingest_task.done")
	if len(done) != 1 {
		t.Fatalf("done events = %d, want 1", len(done))
	}
	rp, ok := done[0].payload["result_pages"].([]any)
	if !ok || len(rp) != 2 {
		t.Fatalf("done result_pages = %v, want 2 entries", done[0].payload["result_pages"])
	}
	if rp[0] != page1.String() || rp[1] != page2.String() {
		t.Errorf("done result_pages = %v, want [%s %s]", rp, page1, page2)
	}

	// 终态后的迟到 running 更新：MarkRunning 应 ErrNotFound（不重复发事件）。
	if err := h.st.MarkRunning(ctx, task.ID, "worker", "wiki-llm-worker"); err != ErrNotFound {
		t.Errorf("late MarkRunning = %v, want ErrNotFound", err)
	}
}

func TestStore_FailedAndProgressEvents(t *testing.T) {
	h := newIngestTestHarness(t)
	ctx := context.Background()
	task := h.createTask(t)

	if err := h.st.UpdateProgress(ctx, task.ID,
		map[string]any{"phase": "outline", "percent": 40}, "user", h.owner.String()); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MarkTerminal(ctx, task.ID, StatusFailed, "llm timeout", "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}

	evs := h.fetchIngestEvents(t)

	prog := eventsOfType(evs, "ingest_task.progress")
	if len(prog) != 1 {
		t.Fatalf("progress events = %d, want 1", len(prog))
	}
	// progress 键平铺进 payload（客户端 op=progress 直接读 phase/percent）。
	if prog[0].payload["phase"] != "outline" {
		t.Errorf("progress phase = %v, want outline", prog[0].payload["phase"])
	}
	if prog[0].payload["percent"] != float64(40) {
		t.Errorf("progress percent = %v, want 40", prog[0].payload["percent"])
	}

	failed := eventsOfType(evs, "ingest_task.failed")
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	if failed[0].payload["error"] != "llm timeout" {
		t.Errorf("failed error = %v, want 'llm timeout'", failed[0].payload["error"])
	}
}

func TestStore_SweepCancelRequestedEmitsCancelled(t *testing.T) {
	h := newIngestTestHarness(t)
	ctx := context.Background()
	task := h.createTask(t)

	if err := h.st.RequestCancel(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE brain.ingest_tasks SET cancel_requested_at = now() - interval '1 hour' WHERE id = $1`,
		task.ID); err != nil {
		t.Fatal(err)
	}

	n, err := h.st.SweepCancelRequested(ctx, time.Now().Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("swept = %d, want 1", n)
	}

	cancelled := eventsOfType(h.fetchIngestEvents(t), "ingest_task.cancelled")
	if len(cancelled) != 1 {
		t.Fatalf("cancelled events = %d, want 1", len(cancelled))
	}
	if cancelled[0].payload["task_id"] != task.ID.String() {
		t.Errorf("cancelled task_id = %v, want %s", cancelled[0].payload["task_id"], task.ID)
	}
	if cancelled[0].payload["status"] != StatusCancelled {
		t.Errorf("cancelled status = %v", cancelled[0].payload["status"])
	}
}

func TestStore_FindLastDoneBySource(t *testing.T) {
	h := newIngestTestHarness(t)
	ctx := context.Background()

	// source_id 有 FK 到 wiki_sources，建行真 source。
	srcStore := sources.New(h.pool)
	owner := h.owner
	src, err := srcStore.Upsert(ctx, sources.CreateInput{
		ProjectID: h.pid, UserID: &owner,
		RelPath: "docs/" + uuid.NewString() + ".md", Filename: "f.md",
		ContentHash: []byte("hash"), ExtractedText: "x", ParseStatus: "done",
	})
	if err != nil {
		t.Fatalf("Upsert source: %v", err)
	}
	sid := src.ID

	// 无任务 → ErrNotFound。
	if _, err := h.st.FindLastDoneBySource(ctx, sid); err != ErrNotFound {
		t.Fatalf("empty: %v, want ErrNotFound", err)
	}

	const hash = "abc123"
	mk := func() *Task {
		t.Helper()
		task, err := h.st.Create(ctx, CreateInput{
			ProjectID: h.pid, OwnerID: h.owner, SourceID: &sid,
			Title: "t", SourceHash: hash,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return task
	}

	// 失败任务不算"上次成功"。
	failed := mk()
	if err := h.st.MarkTerminal(ctx, failed.ID, StatusFailed, "boom", "user", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.FindLastDoneBySource(ctx, sid); err != ErrNotFound {
		t.Fatalf("failed-only: %v, want ErrNotFound", err)
	}

	// done 但无 result_pages 也不算。
	emptyDone := mk()
	if err := h.st.MarkTerminal(ctx, emptyDone.ID, StatusDone, "", "user", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.FindLastDoneBySource(ctx, sid); err != ErrNotFound {
		t.Fatalf("done-no-pages: %v, want ErrNotFound", err)
	}

	// done + result_pages → 命中，SourceHash 来自 progress。
	done := mk()
	if err := h.st.AppendResultPage(ctx, done.ID, uuid.New(), nil, "user", "test"); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MarkTerminal(ctx, done.ID, StatusDone, "", "user", "test"); err != nil {
		t.Fatal(err)
	}
	got, err := h.st.FindLastDoneBySource(ctx, sid)
	if err != nil {
		t.Fatalf("FindLastDoneBySource: %v", err)
	}
	if got.ID != done.ID {
		t.Errorf("task = %s, want %s", got.ID, done.ID)
	}
	if got.SourceHash() != hash {
		t.Errorf("SourceHash = %q, want %q", got.SourceHash(), hash)
	}

	// 无 SourceHash 的老任务 → SourceHash() 空串（不参与短路）。
	legacy, err := h.st.Create(ctx, CreateInput{
		ProjectID: h.pid, OwnerID: h.owner, RawText: "x", Title: "legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SourceHash() != "" {
		t.Errorf("legacy SourceHash = %q, want empty", legacy.SourceHash())
	}
}
