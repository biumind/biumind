// agent_runs / page_revisions.run_id / restore if_match 集成测试（迁移 00010，
// §1.2 P2）。真 Postgres，DATABASE_URL 未设时 skip（同 store_test.go 惯例）。

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (h *wikiTestHarness) createAgentRun(t *testing.T, pid, owner uuid.UUID, runID string) {
	t.Helper()
	if err := h.st.CreateAgentRun(context.Background(), AgentRun{
		RunID: runID, ProjectID: pid, OwnerID: owner,
		Mode: "standard", Model: "m1", Instruction: "整理一下",
	}); err != nil {
		t.Fatalf("CreateAgentRun: %v", err)
	}
}

func TestAgentRuns_Lifecycle(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "runs-lifecycle")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	h.createAgentRun(t, proj.ID, owner, "run-a")

	runs, err := h.st.ListAgentRuns(ctx, proj.ID, 50)
	if err != nil {
		t.Fatalf("ListAgentRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != AgentRunRunning || runs[0].FinishedAt != nil {
		t.Fatalf("running row wrong: %+v", runs)
	}

	if err := h.st.FinishAgentRun(ctx, "run-a", AgentRunDone, ""); err != nil {
		t.Fatalf("FinishAgentRun: %v", err)
	}
	got, err := h.st.GetAgentRun(ctx, proj.ID, "run-a")
	if err != nil {
		t.Fatalf("GetAgentRun: %v", err)
	}
	if got.Status != AgentRunDone || got.FinishedAt == nil || got.Error != "" {
		t.Fatalf("done row wrong: %+v", got)
	}

	// 终态后重复 finish 不覆盖（WHERE status='running' 幂等）。
	if err := h.st.FinishAgentRun(ctx, "run-a", AgentRunFailed, "late"); err != nil {
		t.Fatal(err)
	}
	got, _ = h.st.GetAgentRun(ctx, proj.ID, "run-a")
	if got.Status != AgentRunDone || got.Error != "" {
		t.Fatalf("terminal status overwritten: %+v", got)
	}

	// cancel 优先：running 时 cancel → cancelled；loop 退出的 failed 回写被挡。
	h.createAgentRun(t, proj.ID, owner, "run-b")
	if err := h.st.FinishAgentRun(ctx, "run-b", AgentRunCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.st.FinishAgentRun(ctx, "run-b", AgentRunFailed, "context canceled"); err != nil {
		t.Fatal(err)
	}
	got, _ = h.st.GetAgentRun(ctx, proj.ID, "run-b")
	if got.Status != AgentRunCancelled {
		t.Fatalf("cancel lost to late failed write: %+v", got)
	}

	// 跨项目隔离。
	if _, err := h.st.GetAgentRun(ctx, uuid.New(), "run-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project get: want ErrNotFound, got %v", err)
	}
}

func TestPageRevisions_RunIDAttribution(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "run-attr")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	page := h.createPage(t, proj.ID, owner, "p")

	// agent 路径：UpdatePage 带 RunID → 快照落 run_id。
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{
		PageID: page.ID, Title: strPtr("v1"), ActorID: owner.String(), RunID: "run-1",
	}); err != nil {
		t.Fatal(err)
	}
	revs := listPageRevisions(t, h, page.ID)
	if len(revs) != 1 || revs[0].RunID != "run-1" {
		t.Fatalf("agent snapshot run_id wrong: %+v", revs)
	}

	// 窗口合并：run 内第二次写不新增快照，首行 run_id 不被覆盖/清空。
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{
		PageID: page.ID, Title: strPtr("v2"), ActorID: owner.String(), RunID: "run-1",
	}); err != nil {
		t.Fatal(err)
	}
	// 不同 run 在窗口内同样合并（窗口语义不变），run_id 保持首写归属。
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{
		PageID: page.ID, Title: strPtr("v3"), ActorID: owner.String(), RunID: "run-2",
	}); err != nil {
		t.Fatal(err)
	}
	revs = listPageRevisions(t, h, page.ID)
	if len(revs) != 1 || revs[0].RunID != "run-1" {
		t.Fatalf("window merge clobbered run_id: %+v", revs)
	}

	// 人工路径：过窗口后无 RunID → 快照 run_id 为 NULL（list 读出 ""）。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{
		PageID: page.ID, Title: strPtr("v4"), ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	revs = listPageRevisions(t, h, page.ID)
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revs))
	}
	if revs[0].RunID != "" {
		t.Fatalf("manual snapshot must have NULL run_id, got %q", revs[0].RunID)
	}
	// detail 路径也带 run_id。
	full, err := h.st.GetPageRevision(ctx, page.ID, revs[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.RunID != "run-1" {
		t.Fatalf("GetPageRevision run_id = %q, want run-1", full.RunID)
	}
}

func TestAgentRuns_ChangesAggregate(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "run-changes")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()
	h.createAgentRun(t, proj.ID, owner, "run-x")

	// run-x：update 两页（p1 两次窗口合并只算一页）+ merge p3→p2。
	p1 := h.createPage(t, proj.ID, owner, "p1")
	p2 := h.createPage(t, proj.ID, owner, "p2")
	p3 := h.createPage(t, proj.ID, owner, "p3")
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{PageID: p1.ID, Title: strPtr("p1b"), ActorID: owner.String(), RunID: "run-x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{PageID: p1.ID, Title: strPtr("p1c"), ActorID: owner.String(), RunID: "run-x"}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MergePages(ctx, p2.ID, p3.ID, owner.String(), "run-x"); err != nil {
		t.Fatal(err)
	}

	runs, err := h.st.ListAgentRuns(ctx, proj.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ChangedPages != 3 {
		t.Fatalf("changed_pages = %+v, want 3 (p1 + canonical p2 + duplicate p3)", runs)
	}

	changes, err := h.st.ListAgentRunChanges(ctx, "run-x")
	if err != nil {
		t.Fatal(err)
	}
	// 快照行：p1 update 1 条（窗口合并）+ canonical/duplicate 各 1 条。
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	byPage := map[uuid.UUID]*AgentRunChange{}
	for _, c := range changes {
		byPage[c.PageID] = c
		if c.ChangeType != "edit" {
			t.Fatalf("change_type = %q, want edit", c.ChangeType)
		}
	}
	if byPage[p1.ID].Op != "update" || byPage[p2.ID].Op != "update" {
		t.Fatalf("update rows mis-inferred: %+v", changes)
	}
	// duplicate 软删 + merged_into → 推断 merge；标题取快照写前标题（页已删可查）。
	if byPage[p3.ID].Op != "merge" || byPage[p3.ID].Title != "p3" {
		t.Fatalf("duplicate row = %+v, want merge/p3", byPage[p3.ID])
	}
}

func TestPageRevisions_RestoreIfMatch(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "restore-occ")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	page := h.createPage(t, proj.ID, owner, "occ")
	h.createBlock(t, proj.ID, page.ID, owner, "text", "旧正文", 0)
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{
		PageID: page.ID, Title: strPtr("新标题"), ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	revs := listPageRevisions(t, h, page.ID)
	target := revs[0].ID

	cur, err := h.st.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	revCountBefore := len(listPageRevisions(t, h, page.ID))

	// 1. 过期 if_match（run 之后页面又被改过）→ ErrConflict，页与版本历史都不动。
	if _, err := h.st.RestorePageRevision(ctx, page.ID, target, owner.String(), cur.Version+99); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale if_match: want ErrConflict, got %v", err)
	}
	after, _ := h.st.GetPage(ctx, page.ID)
	if after.Version != cur.Version || after.Title != cur.Title {
		t.Fatalf("conflict mutated page: %+v", after)
	}
	if got := len(listPageRevisions(t, h, page.ID)); got != revCountBefore {
		t.Fatalf("conflict wrote backup revision: %d → %d", revCountBefore, got)
	}

	// 2. 正确 if_match → 通过。
	if _, err := h.st.RestorePageRevision(ctx, page.ID, target, owner.String(), cur.Version); err != nil {
		t.Fatalf("matching if_match: %v", err)
	}

	// 3. 不传（0）→ 维持覆盖式行为（向后兼容）。
	if _, err := h.st.RestorePageRevision(ctx, page.ID, target, owner.String(), 0); err != nil {
		t.Fatalf("no if_match: %v", err)
	}
}
