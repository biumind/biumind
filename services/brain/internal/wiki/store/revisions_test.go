// Wiki page_revisions tests against real Postgres — 镜像 note/store revisions_test.go，
// 加 wiki 特有的 block 对账 restore 场景。Skips when DATABASE_URL unset。

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// backdateLastPageEditRevision —— 把该页最新一条 edit 版本拨回 d，跳窗口合并/构造 prune 场景。
func backdateLastPageEditRevision(t *testing.T, h *wikiTestHarness, pageID uuid.UUID, d time.Duration) {
	t.Helper()
	tag, err := h.pool.Exec(context.Background(), `
		UPDATE brain.page_revisions SET created_at = now() - make_interval(secs => $2)
		WHERE id = (
			SELECT id FROM brain.page_revisions
			WHERE page_id = $1 AND change_type = 'edit'
			ORDER BY created_at DESC LIMIT 1
		)
	`, pageID, d.Seconds())
	if err != nil {
		t.Fatalf("backdate revision: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatalf("backdate revision: no edit revision found for page %s", pageID)
	}
}

func listPageRevisions(t *testing.T, h *wikiTestHarness, pageID uuid.UUID) []*Revision {
	t.Helper()
	revs, err := h.st.ListPageRevisions(context.Background(), pageID, 100, 0)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	return revs
}

func TestPageRevisions_SnapshotBeforeEdit(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	defer h.cleanupProject(t, h.createProject(t, owner, "p").ID)
	proj := h.createProject(t, owner, "snapshot-test")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "旧标题")
	h.createBlock(t, proj.ID, page.ID, owner, "text", "旧正文", 0)
	// createBlock 已触发快照 #1（空页态）；backdate 过窗口，让 UpdatePage 触发
	// 新快照 #2 捕获 [旧正文] 旧态（block 此刻已存在）。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)

	// 实质改 title → 写前快照旧态。
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{
		PageID: page.ID, Title: strPtr("新标题"), ActorID: owner.String(),
	}); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}

	revs := listPageRevisions(t, h, page.ID)
	if len(revs) < 1 {
		t.Fatalf("expected >=1 revision, got %d", len(revs))
	}
	// 最新一条 = UpdatePage 触发的快照（旧标题 + [旧正文]）。
	r := revs[0]
	if r.ChangeType != "edit" || r.ChangeSummary != nil {
		t.Fatalf("change_type=%q summary=%v, want edit/nil", r.ChangeType, r.ChangeSummary)
	}
	if r.Title != "旧标题" {
		t.Fatalf("snapshot title=%q, want 旧标题", r.Title)
	}
	// 列表项不带 blocks_json；完整内容走 GetPageRevision。
	full, err := h.st.GetPageRevision(context.Background(), page.ID, r.ID)
	if err != nil {
		t.Fatalf("GetPageRevision: %v", err)
	}
	var blocks []Block
	if err := json.Unmarshal(full.BlocksJSON, &blocks); err != nil {
		t.Fatalf("unmarshal blocks_json: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Content["text"] != "旧正文" {
		t.Fatalf("snapshot blocks = %+v, want 1 block 旧正文", blocks)
	}
}

func TestPageRevisions_WindowMerge(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "window-test")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "v0")
	// 5 分钟内两次改 title：窗口合并，只 1 条快照（窗口起点旧态 v0）。
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{PageID: page.ID, Title: strPtr("v1"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{PageID: page.ID, Title: strPtr("v2"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}
	if got := len(listPageRevisions(t, h, page.ID)); got != 1 {
		t.Fatalf("window merge: expected 1 revision, got %d", got)
	}

	// 拨回 6 分钟 → 过窗口 → 再改产生新快照（内容是 v2）。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{PageID: page.ID, Title: strPtr("v3"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}
	revs := listPageRevisions(t, h, page.ID)
	if len(revs) != 2 {
		t.Fatalf("after window: expected 2 revisions, got %d", len(revs))
	}
	if revs[0].Title != "v2" {
		t.Fatalf("newest snapshot title=%q, want v2", revs[0].Title)
	}
}

// TestPageRevisions_BlockWritesTriggerSnapshot —— CreateBlock/UpdateBlock/SoftDeleteBlock
// 三个 block 写入口都触发页级快照（验证 4 触发点中 block 相关 3 个）。
func TestPageRevisions_BlockWritesTriggerSnapshot(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "block-trigger-test")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "p")

	// CreateBlock：第一次写产生快照（页空 blocks）。后续窗口内合并。
	b1 := h.createBlock(t, proj.ID, page.ID, owner, "text", "b1", 0)
	if got := len(listPageRevisions(t, h, page.ID)); got != 1 {
		t.Fatalf("CreateBlock: expected 1 revision, got %d", got)
	}

	// 拨窗口 → UpdateBlock 产生新快照。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdateBlock(context.Background(), UpdateBlockInput{
		BlockID: b1.ID, Content: map[string]any{"text": "b1-mod"}, ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(listPageRevisions(t, h, page.ID)); got != 2 {
		t.Fatalf("UpdateBlock: expected 2 revisions, got %d", got)
	}

	// 拨窗口 → SoftDeleteBlock 产生新快照。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if err := h.st.SoftDeleteBlock(context.Background(), b1.ID, owner.String()); err != nil {
		t.Fatal(err)
	}
	if got := len(listPageRevisions(t, h, page.ID)); got != 3 {
		t.Fatalf("SoftDeleteBlock: expected 3 revisions, got %d", got)
	}
}

// TestPageRevisions_RestoreBlockReconcile —— 核心对账场景：
// 快照 [A,B,C] → 删 B + 加 D + 改 A → restore → 验 A 复原/B 复活/C 留/D 软删。
func TestPageRevisions_RestoreBlockReconcile(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "reconcile-test")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "页")
	a := h.createBlock(t, proj.ID, page.ID, owner, "text", "A", 0)
	b := h.createBlock(t, proj.ID, page.ID, owner, "text", "B", 1)
	c := h.createBlock(t, proj.ID, page.ID, owner, "text", "C", 2)

	// 直接 INSERT 一条 edit 版本 R，blocks_json = 当前 [A,B,C] 全量（确定性快照源）。
	blocksJSON, err := json.Marshal([]*Block{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	var rid uuid.UUID
	err = h.pool.QueryRow(context.Background(), `
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, frontmatter, blocks_json, change_type)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, 'edit')
		RETURNING id
	`, page.ID, proj.ID, owner.String(), "页", blocksJSON).Scan(&rid)
	if err != nil {
		t.Fatalf("insert revision R: %v", err)
	}

	// 变形：删 B + 改 A 内容 + 加 D。
	if err := h.st.SoftDeleteBlock(context.Background(), b.ID, owner.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdateBlock(context.Background(), UpdateBlockInput{
		BlockID: a.ID, Content: map[string]any{"text": "A-modified"}, ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	h.createBlock(t, proj.ID, page.ID, owner, "text", "D", 3)

	// 恢复前 page version。
	cur, err := h.st.GetPage(context.Background(), page.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Restore R。
	restored, err := h.st.RestorePageRevision(context.Background(), page.ID, rid, owner.String())
	if err != nil {
		t.Fatalf("RestorePageRevision: %v", err)
	}
	if restored.Version != cur.Version+1 {
		t.Fatalf("version = %d, want %d", restored.Version, cur.Version+1)
	}

	// 验对账后 live blocks = [A(orig), B(revived), C]，D 软删。
	live, err := h.st.ListBlocks(context.Background(), page.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]string{}
	for _, b := range live {
		byID[b.ID] = b.Content["text"].(string)
	}
	if got, want := len(live), 3; got != want {
		t.Fatalf("live blocks = %d, want %d", got, want)
	}
	if byID[a.ID] != "A" {
		t.Errorf("A content = %q, want A (restored from snapshot, not A-modified)", byID[a.ID])
	}
	if _, ok := byID[b.ID]; !ok {
		t.Errorf("B should be revived, missing from live")
	} else if byID[b.ID] != "B" {
		t.Errorf("B content = %q, want B", byID[b.ID])
	}
	if _, ok := byID[c.ID]; !ok {
		t.Errorf("C should remain, missing from live")
	}
	if _, ok := byID[a.ID]; !ok {
		t.Errorf("A missing from live")
	}

	// D 应软删（不在 live，但行仍在，block_id 连续）。
	var dDeleted *time.Time
	if err := h.pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM brain.blocks WHERE content->>'text' = 'D' AND page_id = $1`, page.ID).Scan(&dDeleted); err != nil {
		t.Fatalf("query D: %v", err)
	}
	if dDeleted == nil {
		t.Errorf("D should be soft-deleted after restore")
	}

	// 恢复前自动备份：当前（变形）态存为 restore 版本，永久。
	revs := listPageRevisions(t, h, page.ID)
	var backup *Revision
	for _, r := range revs {
		if r.ChangeType == "restore" {
			backup = r
		}
	}
	if backup == nil {
		t.Fatalf("restore auto-backup not found")
	}
	if backup.ChangeSummary == nil || *backup.ChangeSummary != RevisionRestoreSummary {
		t.Errorf("backup summary = %v, want %q", backup.ChangeSummary, RevisionRestoreSummary)
	}
}

func TestPageRevisions_SaveAsCopy(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "copy-test")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "原标题")
	h.createBlock(t, proj.ID, page.ID, owner, "text", "块1", 0)
	// createBlock 触发快照 #1（空 blocks）；backdate 后改标题触发快照 #2
	// 捕获旧态（原标题 + [块1]），作为 save-as-copy 源。
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)

	// 改标题产生快照（旧态：原标题 + 块1）。
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{
		PageID: page.ID, Title: strPtr("新标题"), ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	revs := listPageRevisions(t, h, page.ID)
	if len(revs) < 1 {
		t.Fatalf("expected >=1 revision, got %d", len(revs))
	}

	cp, err := h.st.SavePageRevisionAsCopy(context.Background(), page.ID, revs[0].ID, owner.String())
	if err != nil {
		t.Fatalf("SavePageRevisionAsCopy: %v", err)
	}
	if cp.ID == page.ID {
		t.Fatal("copy got same id as source")
	}
	if cp.Title != "原标题"+RevisionCopySuffix {
		t.Errorf("copy title = %q, want suffix %q", cp.Title, RevisionCopySuffix)
	}
	cpBlocks, err := h.st.ListBlocks(context.Background(), cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cpBlocks) != 1 || cpBlocks[0].Content["text"] != "块1" {
		t.Errorf("copy blocks = %+v, want 1 block 块1", cpBlocks)
	}
	// 注：wiki CreateBlock 触发快照（note CreateNote 不触发），故 copy 页合法
	// 携带其创建过程中的 revision（窗口合并后通常 1 条空态）—— 与 note 「0 条」不同。
}

func TestPageRevisions_Prune(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "prune-test")
	defer h.cleanupProject(t, proj.ID)
	page := h.createPage(t, proj.ID, owner, "p")
	ctx := context.Background()

	// 5 条 40 天前的 edit + 1 条 40 天前的 restore（直插构造历史）。
	for i := 0; i < 5; i++ {
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, blocks_json, change_type, created_at)
			VALUES ($1, $2, $3, 'old', '[]'::jsonb, 'edit', now() - interval '40 days')
		`, page.ID, proj.ID, owner.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, blocks_json, change_type, change_summary, created_at)
		VALUES ($1, $2, $3, 'backup', '[]'::jsonb, 'restore', $4, now() - interval '40 days')
	`, page.ID, proj.ID, owner.String(), RevisionRestoreSummary); err != nil {
		t.Fatal(err)
	}
	// 2 条新 edit（拨窗口避开合并）。
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{PageID: page.ID, Title: strPtr("t1"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePage(ctx, UpdatePageInput{PageID: page.ID, Title: strPtr("t2"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}

	deleted, err := h.st.PrunePageRevisions(ctx, 2, 30)
	if err != nil {
		t.Fatalf("PrunePageRevisions: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("pruned %d rows, want 5 (old edits beyond top-2)", deleted)
	}
	revs := listPageRevisions(t, h, page.ID)
	if len(revs) != 3 {
		t.Fatalf("expected 3 revisions left, got %d", len(revs))
	}
	var restoreKept bool
	for _, r := range revs {
		if r.ChangeType == "restore" {
			restoreKept = true
		}
	}
	if !restoreKept {
		t.Fatalf("restore revision must be kept forever")
	}
}

func TestPageRevisions_GetRevisionCrossPageRejected(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "xpage-test")
	defer h.cleanupProject(t, proj.ID)
	page1 := h.createPage(t, proj.ID, owner, "p1")
	page2 := h.createPage(t, proj.ID, owner, "p2")
	if _, err := h.st.UpdatePage(context.Background(), UpdatePageInput{PageID: page1.ID, Title: strPtr("x"), ActorID: owner.String()}); err != nil {
		t.Fatal(err)
	}
	revs := listPageRevisions(t, h, page1.ID)
	if len(revs) != 1 {
		t.Fatal("expected 1 revision")
	}
	// 用 page2 取 page1 的版本 → ErrNotFound（page_id 严格匹配防跨页）。
	if _, err := h.st.GetPageRevision(context.Background(), page2.ID, revs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-page get: expected ErrNotFound, got %v", err)
	}
}

// strPtr 便捷取 *string（UpdatePageInput.Title 用）。
func strPtr(s string) *string { return &s }

// TestPageRevisions_RestorePreservesBodyMd —— 回归：RestorePageRevision 曾 SELECT 不含
// body_md 却以 rev.BodyMd（零值）覆盖 pages.body_md，restore 后一编辑即二次抹掉内容；
// 恢复前备份行同样不写 body_md，二次恢复链断。本测试锁死两条路径。
func TestPageRevisions_RestorePreservesBodyMd(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "restore-bodymd")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "页")

	const v1Body = "# 第一版\n\n正文一"
	const v2Body = "# 第二版\n\n正文二"

	// v1 → backdate 跳窗口合并 → v2（快照捕获 v1 旧态，含 body_md）。
	if _, err := h.st.UpdatePageBody(context.Background(), UpdatePageBodyInput{
		PageID: page.ID, BodyMd: v1Body, ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePageBody(context.Background(), UpdatePageBodyInput{
		PageID: page.ID, BodyMd: v2Body, ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}

	// 找含 v1 body_md 的快照。
	var targetID uuid.UUID
	for _, r := range listPageRevisions(t, h, page.ID) {
		full, err := h.st.GetPageRevision(context.Background(), page.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if full.BodyMd == v1Body {
			targetID = r.ID
			break
		}
	}
	if targetID == uuid.Nil {
		t.Fatal("no revision carrying v1 body_md found")
	}

	// Restore → pages.body_md 必须回到 v1（GetPage 复查，不信 RETURNING）。
	if _, err := h.st.RestorePageRevision(context.Background(), page.ID, targetID, owner.String()); err != nil {
		t.Fatalf("RestorePageRevision: %v", err)
	}
	got, err := h.st.GetPage(context.Background(), page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyMd != v1Body {
		t.Errorf("body_md after restore = %q, want v1 body", got.BodyMd)
	}

	// 恢复前自动备份的 restore 版本必须带 v2 body_md（二次恢复链不断）。
	var backupID uuid.UUID
	for _, r := range listPageRevisions(t, h, page.ID) {
		if r.ChangeType == "restore" {
			backupID = r.ID
			break
		}
	}
	if backupID == uuid.Nil {
		t.Fatal("restore auto-backup not found")
	}
	backup, err := h.st.GetPageRevision(context.Background(), page.ID, backupID)
	if err != nil {
		t.Fatal(err)
	}
	if backup.BodyMd != v2Body {
		t.Errorf("backup body_md = %q, want v2 body", backup.BodyMd)
	}
}

// TestPageRevisions_SaveAsCopyPreservesBodyMd —— 回归：SavePageRevisionAsCopy 曾不传
// BodyMd，副本页编辑态打开空白。body_md 非空时 CreatePage 自动投影 blocks，
// 逐块复制会翻倍——断言副本 blocks 恰好等于 mdparse 投影量。
func TestPageRevisions_SaveAsCopyPreservesBodyMd(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "copy-bodymd")
	defer h.cleanupProject(t, proj.ID)

	page := h.createPage(t, proj.ID, owner, "原标题")

	const v1Body = "# 标题\n\n正文"
	if _, err := h.st.UpdatePageBody(context.Background(), UpdatePageBodyInput{
		PageID: page.ID, BodyMd: v1Body, ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}
	backdateLastPageEditRevision(t, h, page.ID, 6*time.Minute)
	if _, err := h.st.UpdatePageBody(context.Background(), UpdatePageBodyInput{
		PageID: page.ID, BodyMd: "后续改动", ActorID: owner.String(),
	}); err != nil {
		t.Fatal(err)
	}

	var targetID uuid.UUID
	for _, r := range listPageRevisions(t, h, page.ID) {
		full, err := h.st.GetPageRevision(context.Background(), page.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if full.BodyMd == v1Body {
			targetID = r.ID
			break
		}
	}
	if targetID == uuid.Nil {
		t.Fatal("no revision carrying v1 body_md found")
	}

	cp, err := h.st.SavePageRevisionAsCopy(context.Background(), page.ID, targetID, owner.String())
	if err != nil {
		t.Fatalf("SavePageRevisionAsCopy: %v", err)
	}
	if cp.BodyMd != v1Body {
		t.Errorf("copy body_md = %q, want v1 body", cp.BodyMd)
	}
	cpBlocks, err := h.st.ListBlocks(context.Background(), cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	// mdparse("# 标题\n\n正文") = heading + text 两块；若 body 投影与逐块复制叠加则为 4。
	if len(cpBlocks) != 2 {
		t.Errorf("copy blocks = %d, want 2 (mdparse projection only, no duplicate copy)", len(cpBlocks))
	}
}
