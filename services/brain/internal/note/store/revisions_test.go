// Revision tests against real Postgres — same skip convention as store_test.go.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// updateNote —— 测试便捷封装：整篇覆盖 title/content。
func updateNote(t *testing.T, h *storeHarness, uid, noteID uuid.UUID, title, content string) *Note {
	t.Helper()
	n, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: noteID, UserID: uid, Title: &title, ContentMD: &content, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	return n
}

// backdateLastEditRevision —— 把该笔记最新一条 edit 版本的 created_at
// 拨回 d，用来跳过窗口合并或构造 prune 场景。
func backdateLastEditRevision(t *testing.T, h *storeHarness, noteID uuid.UUID, d time.Duration) {
	t.Helper()
	tag, err := h.pool.Exec(context.Background(), `
		UPDATE brain.note_revisions SET created_at = now() - $2::interval
		WHERE id = (
			SELECT id FROM brain.note_revisions
			WHERE note_id = $1 AND change_type = 'edit'
			ORDER BY created_at DESC LIMIT 1
		)
	`, noteID, d)
	if err != nil {
		t.Fatalf("backdate revision: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatalf("backdate revision: no edit revision found for note %s", noteID)
	}
}

func listRevisions(t *testing.T, h *storeHarness, uid, noteID uuid.UUID) []*Revision {
	t.Helper()
	revs, err := h.st.ListRevisions(context.Background(), noteID, uid, 100, 0)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	return revs
}

func TestRevisions_SnapshotBeforeEdit(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "旧标题", "旧正文")
	updateNote(t, h, uid, n.ID, "新标题", "新正文")

	revs := listRevisions(t, h, uid, n.ID)
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	r := revs[0]
	if r.ChangeType != "edit" {
		t.Fatalf("change_type = %q, want edit", r.ChangeType)
	}
	if r.ChangeSummary != nil {
		t.Fatalf("edit revision summary should be nil, got %q", *r.ChangeSummary)
	}
	// 列表项不带 content_md；完整内容走 GetRevision。
	full, err := h.st.GetRevision(context.Background(), n.ID, r.ID, uid)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if full.Title != "旧标题" || full.ContentMD != "旧正文" {
		t.Fatalf("snapshot holds (%q, %q), want (旧标题, 旧正文)", full.Title, full.ContentMD)
	}
}

func TestRevisions_WindowMerge(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "v0", "c0")
	updateNote(t, h, uid, n.ID, "v1", "c1")
	// 5 分钟内第二次编辑：窗口合并，不产生新版本。
	updateNote(t, h, uid, n.ID, "v2", "c2")
	if got := len(listRevisions(t, h, uid, n.ID)); got != 1 {
		t.Fatalf("window merge: expected 1 revision, got %d", got)
	}

	// 把上一条 edit 版本拨回 6 分钟前，窗口过后再次编辑 → 新快照，
	// 内容是窗口终点前的旧状态（v2/c2）。
	backdateLastEditRevision(t, h, n.ID, 6*time.Minute)
	updateNote(t, h, uid, n.ID, "v3", "c3")
	revs := listRevisions(t, h, uid, n.ID)
	if len(revs) != 2 {
		t.Fatalf("after window: expected 2 revisions, got %d", len(revs))
	}
	latest := revs[0] // 新→旧
	if latest.Title != "v2" || latest.ChangeType != "edit" {
		t.Fatalf("newest snapshot = (%q, %q), want (v2, edit)", latest.Title, latest.ChangeType)
	}
}

func TestRevisions_NoSnapshotWithoutMaterialChange(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "标题", "正文")
	// 只动 position：不产生快照。
	pos := 42.0
	if _, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: n.ID, UserID: uid, Position: &pos, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote position: %v", err)
	}
	// title/content 传了但值相同：也不算实质变化。
	sameTitle, sameContent := "标题", "正文"
	if _, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: n.ID, UserID: uid, Title: &sameTitle, ContentMD: &sameContent, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote same content: %v", err)
	}
	if got := len(listRevisions(t, h, uid, n.ID)); got != 0 {
		t.Fatalf("expected 0 revisions, got %d", got)
	}
}

func TestRevisions_RestoreAutoBackup(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "原始", "初始正文")
	updateNote(t, h, uid, n.ID, "改后", "改后正文")
	revs := listRevisions(t, h, uid, n.ID)
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}

	cur, err := h.st.GetNote(context.Background(), n.ID, uid)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	restored, err := h.st.RestoreRevision(context.Background(), n.ID, revs[0].ID, uid, uid.String())
	if err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}
	if restored.Title != "原始" || restored.ContentMD != "初始正文" {
		t.Fatalf("restored = (%q, %q), want (原始, 初始正文)", restored.Title, restored.ContentMD)
	}
	if restored.Version != cur.Version+1 {
		t.Fatalf("version = %d, want %d", restored.Version, cur.Version+1)
	}

	// 恢复前自动备份：当前（改后）状态存为 restore 版本，永久保留。
	revs = listRevisions(t, h, uid, n.ID)
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions after restore, got %d", len(revs))
	}
	backup := revs[0] // 最新一条
	if backup.ChangeType != "restore" {
		t.Fatalf("backup change_type = %q, want restore", backup.ChangeType)
	}
	if backup.ChangeSummary == nil || *backup.ChangeSummary != RevisionRestoreSummary {
		t.Fatalf("backup summary = %v, want %q", backup.ChangeSummary, RevisionRestoreSummary)
	}
	if backup.Title != "改后" {
		t.Fatalf("backup title = %q, want 改后", backup.Title)
	}
}

func TestRevisions_RestoreTrashedNoteRejected(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "t", "c")
	updateNote(t, h, uid, n.ID, "t2", "c2")
	revs := listRevisions(t, h, uid, n.ID)
	if err := h.st.SoftDeleteNote(context.Background(), n.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNote: %v", err)
	}
	if _, err := h.st.RestoreRevision(context.Background(), n.ID, revs[0].ID, uid, uid.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore trashed note: expected ErrNotFound, got %v", err)
	}
}

func TestRevisions_SaveAsCopy(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	nb, err := h.st.CreateNotebook(context.Background(), uid, "笔记本", 0, nil, uid.String())
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	tag, err := h.st.CreateTag(context.Background(), uid, "标签A", uid.String())
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	n, replayed, err := h.st.CreateNote(context.Background(), CreateNoteInput{
		UserID: uid, NotebookID: &nb.ID, Title: "旧标题", ContentMD: "旧正文", ActorID: uid.String(),
	})
	if err != nil || replayed {
		t.Fatalf("CreateNote: %v replayed=%v", err, replayed)
	}
	if err := h.st.SetNoteTags(context.Background(), n.ID, uid, []uuid.UUID{tag.ID}, uid.String()); err != nil {
		t.Fatalf("SetNoteTags: %v", err)
	}
	updateNote(t, h, uid, n.ID, "新标题", "新正文")
	revs := listRevisions(t, h, uid, n.ID)

	cp, err := h.st.SaveRevisionAsCopy(context.Background(), n.ID, revs[0].ID, uid, uid.String())
	if err != nil {
		t.Fatalf("SaveRevisionAsCopy: %v", err)
	}
	if cp.ID == n.ID {
		t.Fatalf("copy got same id as source")
	}
	if cp.Title != "旧标题"+RevisionCopySuffix {
		t.Fatalf("copy title = %q, want suffix %q", cp.Title, RevisionCopySuffix)
	}
	if cp.ContentMD != "旧正文" {
		t.Fatalf("copy content = %q, want 旧正文", cp.ContentMD)
	}
	if cp.NotebookID == nil || *cp.NotebookID != nb.ID {
		t.Fatalf("copy notebook = %v, want %s", cp.NotebookID, nb.ID)
	}
	// 标签关联已复制。
	var tagCount int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM brain.note_note_tags WHERE note_id = $1 AND tag_id = $2`,
		cp.ID, tag.ID).Scan(&tagCount); err != nil {
		t.Fatalf("count copy tags: %v", err)
	}
	if tagCount != 1 {
		t.Fatalf("copy has %d tag links, want 1", tagCount)
	}
	// 副本本身不携带历史版本。
	if got := len(listRevisions(t, h, uid, cp.ID)); got != 0 {
		t.Fatalf("copy should have 0 revisions, got %d", got)
	}
}

func TestRevisions_Prune(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	n := createNote(t, h, uid, "t", "c")
	ctx := context.Background()

	// 5 条 40 天前的 edit 版本 + 1 条 40 天前的 restore 版本（直插 SQL 构造历史）。
	for i := 0; i < 5; i++ {
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO brain.note_revisions (note_id, user_id, title, content_md, change_type, created_at)
			VALUES ($1, $2, $3, '', 'edit', now() - interval '40 days')
		`, n.ID, uid, "old"); err != nil {
			t.Fatalf("insert old revision: %v", err)
		}
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO brain.note_revisions (note_id, user_id, title, content_md, change_type, change_summary, created_at)
		VALUES ($1, $2, 'backup', '', 'restore', $3, now() - interval '40 days')
	`, n.ID, uid, RevisionRestoreSummary); err != nil {
		t.Fatalf("insert restore revision: %v", err)
	}
	// 2 条新的 edit 版本（走正常快照路径，拨时间上一条避开窗口合并）。
	updateNote(t, h, uid, n.ID, "t1", "c1")
	backdateLastEditRevision(t, h, n.ID, 6*time.Minute)
	updateNote(t, h, uid, n.ID, "t2", "c2")

	deleted, err := h.st.PruneRevisions(ctx, 2, 30)
	if err != nil {
		t.Fatalf("PruneRevisions: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("pruned %d rows, want 5 (old edits beyond top-2)", deleted)
	}
	revs := listRevisions(t, h, uid, n.ID)
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

	// keepDays 门槛：新 edit 版本即使超出 keepRecent 也不删。
	if _, err := h.st.PruneRevisions(ctx, 1, 30); err != nil {
		t.Fatalf("PruneRevisions second pass: %v", err)
	}
	if got := len(listRevisions(t, h, uid, n.ID)); got != 3 {
		t.Fatalf("recent edits within keepDays must survive, got %d left", got)
	}
}

func TestRevisions_UserIsolation(t *testing.T) {
	h := newStoreHarness(t)
	uidA, uidB := uuid.New(), uuid.New()
	defer h.cleanupNotes(t, uidA)
	defer h.cleanupNotes(t, uidB)

	n := createNote(t, h, uidA, "a", "a")
	updateNote(t, h, uidA, n.ID, "a2", "a2")
	revs := listRevisions(t, h, uidA, n.ID)
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	rid := revs[0].ID
	ctx := context.Background()

	if _, err := h.st.ListRevisions(ctx, n.ID, uidB, 20, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list as other user: expected ErrNotFound, got %v", err)
	}
	if _, err := h.st.GetRevision(ctx, n.ID, rid, uidB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get as other user: expected ErrNotFound, got %v", err)
	}
	if _, err := h.st.RestoreRevision(ctx, n.ID, rid, uidB, uidB.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore as other user: expected ErrNotFound, got %v", err)
	}
	if _, err := h.st.SaveRevisionAsCopy(ctx, n.ID, rid, uidB, uidB.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("save-as-copy as other user: expected ErrNotFound, got %v", err)
	}
}
