// UnmergePages（§6.1 P3-a merge undo）tests against real Postgres.
// Skips when DATABASE_URL unset — 同 store_test.go / merge_body_test.go 惯例。
//
// 注意快照时序：snapshotPageRevisionMergeTx 有 5min 窗口合并，测试里建页后
// 直接 merge（中间不做其他写），保证 merge 写前快照（带 merge_id）一定落库。

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// mergeAndGetID — 执行 merge 并取回该次 merge 的 merge_id（两页快照共享）。
func mergeAndGetID(t *testing.T, h *wikiTestHarness, canonicalID, duplicateID, owner uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if err := h.st.MergePages(ctx, canonicalID, duplicateID, owner.String(), ""); err != nil {
		t.Fatalf("MergePages: %v", err)
	}
	var mergeID uuid.UUID
	if err := h.pool.QueryRow(ctx, `
		SELECT merge_id FROM brain.page_revisions
		WHERE page_id = $1 AND merge_id IS NOT NULL
		ORDER BY created_at DESC LIMIT 1
	`, canonicalID).Scan(&mergeID); err != nil {
		t.Fatalf("merge snapshot missing merge_id: %v", err)
	}
	// duplicate 侧快照必须共享同一 merge_id。
	var dupMergeID uuid.UUID
	if err := h.pool.QueryRow(ctx, `
		SELECT merge_id FROM brain.page_revisions
		WHERE page_id = $1 AND merge_id IS NOT NULL
		ORDER BY created_at DESC LIMIT 1
	`, duplicateID).Scan(&dupMergeID); err != nil {
		t.Fatalf("duplicate merge snapshot missing merge_id: %v", err)
	}
	if dupMergeID != mergeID {
		t.Fatalf("merge_id mismatch: canonical %v vs duplicate %v", mergeID, dupMergeID)
	}
	return mergeID
}

func liveBlockIDs(t *testing.T, h *wikiTestHarness, pageID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	blocks, err := h.st.ListBlocks(context.Background(), pageID)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	ids := make(map[uuid.UUID]bool, len(blocks))
	for _, b := range blocks {
		ids[b.ID] = true
	}
	return ids
}

// TestUnmergePages_RestoresBothPages — 正常 undo：两页 body/frontmatter/块归属
// 全部回到 merge 前态，迁入块按原 block_id 搬回 duplicate。
func TestUnmergePages_RestoresBothPages(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-restore")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "canonical 正文",
		Frontmatter: map[string]any{"tags": []any{"a"}, "status": "keep"},
		ActorID:     owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "duplicate 正文",
		Frontmatter: map[string]any{"tags": []any{"b"}, "owner": "x"},
		ActorID:     owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonBlocks := liveBlockIDs(t, h, canonical.ID)
	dupBlocks := liveBlockIDs(t, h, duplicate.ID)

	mergeAndGetID(t, h, canonical.ID, duplicate.ID, owner)

	canonPage, dupPage, err := h.st.UnmergePages(ctx, canonical.ID, duplicate.ID, owner.String(), 0)
	if err != nil {
		t.Fatalf("UnmergePages: %v", err)
	}

	// canonical：body/frontmatter 回 merge 前，version 1→2(merge)→3(undo)。
	if canonPage.BodyMd != "canonical 正文" {
		t.Errorf("canonical body not restored: %q", canonPage.BodyMd)
	}
	if canonPage.Version != 3 {
		t.Errorf("canonical version = %d, want 3", canonPage.Version)
	}
	if _, ok := canonPage.Frontmatter["owner"]; ok {
		t.Errorf("canonical frontmatter still carries unioned key: %v", canonPage.Frontmatter)
	}
	assertBlocksMatchBody(t, h, canonical.ID, canonPage.BodyMd)
	for id := range liveBlockIDs(t, h, canonical.ID) {
		if !canonBlocks[id] {
			t.Errorf("canonical has non-original live block %v", id)
		}
	}

	// duplicate：复活，body/frontmatter 回快照值（无 merged_into/merged_at），
	// 原块按原 id 搬回。
	if dupPage.BodyMd != "duplicate 正文" {
		t.Errorf("duplicate body not restored: %q", dupPage.BodyMd)
	}
	if _, ok := dupPage.Frontmatter["merged_into"]; ok {
		t.Errorf("duplicate frontmatter still has merged_into: %v", dupPage.Frontmatter)
	}
	if dupPage.Frontmatter["owner"] != "x" {
		t.Errorf("duplicate frontmatter lost original key: %v", dupPage.Frontmatter)
	}
	got, err := h.st.GetPage(ctx, duplicate.ID)
	if err != nil {
		t.Fatalf("duplicate should be live again: %v", err)
	}
	assertBlocksMatchBody(t, h, duplicate.ID, got.BodyMd)
	revived := liveBlockIDs(t, h, duplicate.ID)
	if len(revived) != len(dupBlocks) {
		t.Fatalf("duplicate live blocks = %d, want %d", len(revived), len(dupBlocks))
	}
	for id := range dupBlocks {
		if !revived[id] {
			t.Errorf("duplicate original block %v not revived", id)
		}
	}

	// undo 前自动备份：两页各一条 change_type='restore' 版本。
	for _, pid := range []uuid.UUID{canonical.ID, duplicate.ID} {
		var n int
		if err := h.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM brain.page_revisions
			WHERE page_id = $1 AND change_type = 'restore'
		`, pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("page %v restore backups = %d, want 1", pid, n)
		}
	}

	// 事件：duplicate 的 changelog 有 page.restored(cause=unmerge)。
	events, err := h.st.ListPageEvents(ctx, proj.ID, duplicate.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "page.restored" && e.Payload["cause"] == "unmerge" {
			found = true
		}
	}
	if !found {
		t.Errorf("page.restored(cause=unmerge) event missing on duplicate changelog")
	}
}

// TestUnmergePages_BlockCollapse — 块坍缩场景：两页正文完全相同，merge 时
// 迁入块被 reconcile 软删（只剩一份）；undo 后 duplicate 的原块按原 id 复活
// 并搬回 duplicate。
func TestUnmergePages_BlockCollapse(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-collapse")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "A", BodyMd: "相同正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "B", BodyMd: "相同正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dupBlocks := liveBlockIDs(t, h, duplicate.ID)
	if len(dupBlocks) != 1 {
		t.Fatalf("precondition: duplicate blocks = %d, want 1", len(dupBlocks))
	}

	mergeAndGetID(t, h, canonical.ID, duplicate.ID, owner)

	// merge 后块坍缩：canonical 只剩 1 个 live 块（duplicate 迁入块被软删）。
	if n := len(liveBlockIDs(t, h, canonical.ID)); n != 1 {
		t.Fatalf("post-merge canonical live blocks = %d, want 1 (collapsed)", n)
	}

	if _, _, err := h.st.UnmergePages(ctx, canonical.ID, duplicate.ID, owner.String(), 0); err != nil {
		t.Fatalf("UnmergePages: %v", err)
	}

	revived := liveBlockIDs(t, h, duplicate.ID)
	for id := range dupBlocks {
		if !revived[id] {
			t.Errorf("collapsed block %v not revived on duplicate", id)
		}
	}
	assertBlocksMatchBody(t, h, canonical.ID, "相同正文")
	assertBlocksMatchBody(t, h, duplicate.ID, "相同正文")
}

// TestUnmergePages_RejectsChainedMerge — A2：canonical 自身又被 merge 进
// 别的页后，旧 merge 的 undo 拒绝（ErrMergeStateInvalid）。
func TestUnmergePages_RejectsChainedMerge(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-chained")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	a := h.createPage(t, proj.ID, owner, "A")
	b := h.createPage(t, proj.ID, owner, "B")
	c := h.createPage(t, proj.ID, owner, "C")

	mergeAndGetID(t, h, a.ID, b.ID, owner) // B → A
	// A → C（链式）。注意 A 的 merge 写前快照命中 5min 窗口合并被跳过
	// （merge1 快照刚写过），故这里不用带 merge_id 断言的 mergeAndGetID。
	if err := h.st.MergePages(ctx, c.ID, a.ID, owner.String(), ""); err != nil {
		t.Fatalf("MergePages (chained): %v", err)
	}

	if _, _, err := h.st.UnmergePages(ctx, a.ID, b.ID, owner.String(), 0); !errors.Is(err, ErrMergeStateInvalid) {
		t.Errorf("chained merge undo: got %v, want ErrMergeStateInvalid", err)
	}
}

// TestUnmergePages_VersionConflict — canonical OCC：merge 后 canonical 又被
// 编辑，带旧 if_match_version 的 undo → ErrConflict；带当前 version → 成功。
func TestUnmergePages_VersionConflict(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-occ")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "A", BodyMd: "原文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := h.createPage(t, proj.ID, owner, "B")

	mergeAndGetID(t, h, canonical.ID, duplicate.ID, owner)

	merged, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	// merge 后再编辑 canonical（version 再 +1）。
	edited, err := h.st.UpdatePageBody(ctx, UpdatePageBodyInput{
		PageID: canonical.ID, BodyMd: "merge 后人工编辑",
		IfMatchVersion: merged.Version, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("UpdatePageBody: %v", err)
	}

	if _, _, err := h.st.UnmergePages(ctx, canonical.ID, duplicate.ID, owner.String(),
		merged.Version); !errors.Is(err, ErrConflict) {
		t.Errorf("stale if_match_version: got %v, want ErrConflict", err)
	}
	// 冲突不动数据：canonical 仍是编辑后内容，duplicate 仍软删。
	cur, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil || cur.BodyMd != "merge 后人工编辑" {
		t.Errorf("canonical changed despite conflict: %q err=%v", cur.BodyMd, err)
	}
	if _, err := h.st.GetPage(ctx, duplicate.ID); err != ErrNotFound {
		t.Errorf("duplicate revived despite conflict: err=%v", err)
	}

	// 带当前 version → 成功（force undo 语义：merge 后编辑被快照覆盖）。
	if _, _, err := h.st.UnmergePages(ctx, canonical.ID, duplicate.ID, owner.String(),
		edited.Version); err != nil {
		t.Fatalf("UnmergePages with current version: %v", err)
	}
	cur, err = h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.BodyMd != "原文" {
		t.Errorf("canonical body = %q, want pre-merge body", cur.BodyMd)
	}
}

// TestUnmergePages_SnapshotMissing — 快照缺失降级（A1）：merge 快照被清掉
// （模拟 Prune）或老数据无 merge_id（模拟 00011 前的 merge）→
// ErrMergeUndoUnavailable，数据不动。
func TestUnmergePages_SnapshotMissing(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-no-snapshot")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	// 情形一：快照被 Prune 清掉。
	a := h.createPage(t, proj.ID, owner, "A")
	b := h.createPage(t, proj.ID, owner, "B")
	mergeID := mergeAndGetID(t, h, a.ID, b.ID, owner)
	if _, err := h.pool.Exec(ctx, `
		DELETE FROM brain.page_revisions WHERE merge_id = $1
	`, mergeID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.st.UnmergePages(ctx, a.ID, b.ID, owner.String(), 0); !errors.Is(err, ErrMergeUndoUnavailable) {
		t.Errorf("pruned snapshots: got %v, want ErrMergeUndoUnavailable", err)
	}

	// 情形二：老数据无 merge_id（00011 前的 merge）。
	c := h.createPage(t, proj.ID, owner, "C")
	d := h.createPage(t, proj.ID, owner, "D")
	mergeAndGetID(t, h, c.ID, d.ID, owner)
	if _, err := h.pool.Exec(ctx, `
		UPDATE brain.page_revisions SET merge_id = NULL
		WHERE page_id IN ($1, $2)
	`, c.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.st.UnmergePages(ctx, c.ID, d.ID, owner.String(), 0); !errors.Is(err, ErrMergeUndoUnavailable) {
		t.Errorf("legacy merge without merge_id: got %v, want ErrMergeUndoUnavailable", err)
	}
	// 两对页都保持 merge 后状态（未被误动）。
	for _, dup := range []uuid.UUID{b.ID, d.ID} {
		if _, err := h.st.GetPage(ctx, dup); err != ErrNotFound {
			t.Errorf("duplicate %v should stay soft-deleted, err=%v", dup, err)
		}
	}
}

// TestUnmergePages_RejectsStateMismatch — 状态不符拒绝：duplicate 未处于
// merge 软删态、或 merged_into 指向的不是该 canonical。
func TestUnmergePages_RejectsStateMismatch(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "unmerge-state")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	a := h.createPage(t, proj.ID, owner, "A")
	b := h.createPage(t, proj.ID, owner, "B")
	c := h.createPage(t, proj.ID, owner, "C")

	// duplicate 还活着（未 merge）→ 拒绝。
	if _, _, err := h.st.UnmergePages(ctx, a.ID, b.ID, owner.String(), 0); !errors.Is(err, ErrMergeStateInvalid) {
		t.Errorf("live duplicate: got %v, want ErrMergeStateInvalid", err)
	}

	// B merge 进 A 后，用错误的 canonical（C）undo → 拒绝。
	mergeAndGetID(t, h, a.ID, b.ID, owner)
	if _, _, err := h.st.UnmergePages(ctx, c.ID, b.ID, owner.String(), 0); !errors.Is(err, ErrMergeStateInvalid) {
		t.Errorf("wrong canonical: got %v, want ErrMergeStateInvalid", err)
	}
	// 不存在的页 → ErrNotFound。
	if _, _, err := h.st.UnmergePages(ctx, a.ID, uuid.New(), owner.String(), 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing duplicate: got %v, want ErrNotFound", err)
	}
}
