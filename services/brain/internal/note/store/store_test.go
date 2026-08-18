// Store tests against real Postgres (tsv 生成列 + zhparser 分词需要真库)。
// Skips when DATABASE_URL unset — same convention as internal/files tests.

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type storeHarness struct {
	pool *pgxpool.Pool
	st   *Store
}

func newStoreHarness(t *testing.T) *storeHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &storeHarness{pool: pool, st: New(pool)}
}

// cleanupNotes — 物理清掉测试用户的笔记/笔记本（事件/关联随行删除）。
func (h *storeHarness) cleanupNotes(t *testing.T, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx,
		`DELETE FROM brain.note_attachments WHERE note_id IN (SELECT id FROM brain.note_notes WHERE user_id = $1)`, uid); err != nil {
		t.Fatalf("cleanup attachments: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_notes WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup notes: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_notebooks WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup notebooks: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.events WHERE scope = $1`, "note:user:"+uid.String()); err != nil {
		t.Fatalf("cleanup events: %v", err)
	}
}

func createNote(t *testing.T, h *storeHarness, uid uuid.UUID, title, content string) *Note {
	t.Helper()
	n, replayed, err := h.st.CreateNote(context.Background(), CreateNoteInput{
		UserID: uid, Title: title, ContentMD: content, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if replayed {
		t.Fatalf("unexpected idempotent replay")
	}
	return n
}

// ─── Search ─────────────────────────────────────────────

func TestSearchNotes_ChineseHitAndSnippet(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	// 用 uuid-style token 作锚点 — zhparser 对纯字母数字单独分词，不会
	// 被相邻中文切碎（纯中文子串匹配的限制同 chat.SearchMessages，
	// 是已知行为，不在此覆盖）。中文上下文用来验证 ts_headline 包裹。
	token := "anchor" + strings.ReplaceAll(uuid.New().String(), "-", "")
	createNote(t, h, uid, "十年磨一剑", "这是一段包含 "+token+" 的文字，前后还有内容用于搜索测试。")
	createNote(t, h, uid, "无关笔记", "今天天气不错，适合出门散步。")

	hits, err := h.st.SearchNotes(context.Background(), uid, token, 0)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d (%+v)", len(hits), hits)
	}
	hit := hits[0]
	if hit.Title != "十年磨一剑" {
		t.Errorf("unexpected hit title: %q", hit.Title)
	}
	if !strings.Contains(hit.Snippet, "<mark>") || !strings.Contains(hit.Snippet, "</mark>") {
		t.Errorf("snippet missing <mark> highlight: %q", hit.Snippet)
	}
	if hit.Rank <= 0 {
		t.Errorf("rank should be positive, got %v", hit.Rank)
	}
}

func TestSearchNotes_TitleWeightBeatsBody(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	token := "sword" + strings.ReplaceAll(uuid.New().String(), "-", "")
	titleHit := createNote(t, h, uid, "关于 "+token+" 的考据", "正文没有相关词。")
	createNote(t, h, uid, "随笔", "随便提一句 "+token+"，正文权重应低于标题命中。")

	hits, err := h.st.SearchNotes(context.Background(), uid, token, 0)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].ID != titleHit.ID {
		t.Errorf("title-weighted note should rank first: %+v", hits)
	}
}

func TestSearchNotes_UserIsolationAndTrashExcluded(t *testing.T) {
	h := newStoreHarness(t)
	uid, other := uuid.New(), uuid.New()
	defer h.cleanupNotes(t, uid)
	defer h.cleanupNotes(t, other)

	token := "昆吾铁冶"
	createNote(t, h, uid, "我的笔记", token)
	createNote(t, h, other, "别人的笔记", token)
	trashed := createNote(t, h, uid, "回收站笔记", token)
	if err := h.st.SoftDeleteNote(context.Background(), trashed.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNote: %v", err)
	}

	hits, err := h.st.SearchNotes(context.Background(), uid, token, 0)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit (other user + trash excluded), got %d", len(hits))
	}
	if hits[0].Title != "我的笔记" {
		t.Errorf("unexpected hit: %+v", hits[0])
	}
}

func TestSearchNotes_LimitClamped(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	token := "欧冶子"
	for i := 0; i < 3; i++ {
		createNote(t, h, uid, token+" 系列", token)
	}
	// limit 超过上限应收敛而非报错。
	hits, err := h.st.SearchNotes(context.Background(), uid, token, 1000)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("expected 3 hits, got %d", len(hits))
	}
}

// ─── Attachments reconciliation ─────────────────────────

// seedFile — 直接插一行 files.objects（绕开 files 包的 upload 流程，
// 这里只关心对账 SQL 的归属/状态过滤）。
func (h *storeHarness) seedFile(t *testing.T, uid uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, source, status)
		VALUES ($1, $2, $3, 1, 'test-bucket', $4, 'note-attachment', $5)
	`, id, uid, id.String(), "test-note/"+id.String(), status)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM files.objects WHERE id = $1`, id)
	})
	return id
}

func (h *storeHarness) attachmentFileIDs(t *testing.T, noteID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT file_id FROM brain.note_attachments WHERE note_id = $1`, noteID)
	if err != nil {
		t.Fatalf("query attachments: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = true
	}
	return out
}

func TestAttachments_ReconcileOnCreateAndUpdate(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	f1 := h.seedFile(t, uid, "ready")
	f2 := h.seedFile(t, uid, "ready")

	n := createNote(t, h, uid, "带图笔记",
		"看图 ![a](biu-file://"+f1.String()+") 和 ![b](biu-file://"+f2.String()+")，重复引用 biu-file://"+f1.String())

	got := h.attachmentFileIDs(t, n.ID)
	if len(got) != 2 || !got[f1] || !got[f2] {
		t.Fatalf("expected {f1, f2}, got %v", got)
	}

	// 更新正文去掉 f2 → f2 行应被 prune，f1 保留（is_associated 仍 true）。
	newContent := "只留 ![a](biu-file://" + f1.String() + ")"
	if _, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: n.ID, UserID: uid, ContentMD: &newContent, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	got = h.attachmentFileIDs(t, n.ID)
	if len(got) != 1 || !got[f1] {
		t.Fatalf("expected {f1} after prune, got %v", got)
	}
	var assoc bool
	if err := h.pool.QueryRow(context.Background(),
		`SELECT is_associated FROM brain.note_attachments WHERE note_id = $1 AND file_id = $2`,
		n.ID, f1).Scan(&assoc); err != nil || !assoc {
		t.Fatalf("f1 should stay is_associated=true: %v %v", assoc, err)
	}

	// 不动 content_md 的更新不应触碰对账结果。
	other := "别的标题"
	if _, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: n.ID, UserID: uid, Title: &other, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote(title only): %v", err)
	}
	if got := h.attachmentFileIDs(t, n.ID); len(got) != 1 || !got[f1] {
		t.Fatalf("title-only update must not touch attachments, got %v", got)
	}

	// 清空全部引用 → 行全删。
	empty := "没有任何附件了"
	if _, err := h.st.UpdateNote(context.Background(), UpdateNoteInput{
		ID: n.ID, UserID: uid, ContentMD: &empty, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote(empty): %v", err)
	}
	if got := h.attachmentFileIDs(t, n.ID); len(got) != 0 {
		t.Fatalf("expected no attachments, got %v", got)
	}
}

func TestAttachments_RejectsForeignAndPendingFiles(t *testing.T) {
	h := newStoreHarness(t)
	uid, other := uuid.New(), uuid.New()
	defer h.cleanupNotes(t, uid)

	own := h.seedFile(t, uid, "ready")
	foreign := h.seedFile(t, other, "ready") // 别人的 file
	pending := h.seedFile(t, uid, "pending") // 本人但未 finalize
	missing := uuid.New()                    // 不存在

	n := createNote(t, h, uid, "混合引用",
		"biu-file://"+own.String()+" biu-file://"+foreign.String()+
			" biu-file://"+pending.String()+" biu-file://"+missing.String())

	got := h.attachmentFileIDs(t, n.ID)
	if len(got) != 1 || !got[own] {
		t.Fatalf("only own ready file should be reconciled, got %v", got)
	}
}

// ─── Notebook hierarchy（迁移 00003）─────────────────────

// createNotebook — 测试便捷封装：建本，parentID 为 nil 表示根级。
func createNotebook(t *testing.T, h *storeHarness, uid uuid.UUID, name string, parentID *uuid.UUID) *Notebook {
	t.Helper()
	nb, err := h.st.CreateNotebook(context.Background(), uid, name, 0, parentID, uid.String())
	if err != nil {
		t.Fatalf("CreateNotebook(%q): %v", name, err)
	}
	return nb
}

func getNotebook(t *testing.T, h *storeHarness, uid, id uuid.UUID) *Notebook {
	t.Helper()
	nb, alive, err := h.st.GetNotebook(context.Background(), id, uid)
	if err != nil {
		t.Fatalf("GetNotebook: %v", err)
	}
	if !alive {
		t.Fatalf("GetNotebook: %s unexpectedly soft-deleted", id)
	}
	return nb
}

func TestNotebookHierarchy_CreateAndList(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	root := createNotebook(t, h, uid, "根", nil)
	if root.ParentID != nil {
		t.Fatalf("root parent_id should be nil, got %v", root.ParentID)
	}
	child := createNotebook(t, h, uid, "子", &root.ID)
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child parent_id = %v, want %s", child.ParentID, root.ID)
	}

	nbs, err := h.st.ListNotebooks(context.Background(), uid)
	if err != nil {
		t.Fatalf("ListNotebooks: %v", err)
	}
	if len(nbs) != 2 {
		t.Fatalf("expected 2 notebooks, got %d", len(nbs))
	}
	byID := map[uuid.UUID]*Notebook{}
	for _, nb := range nbs {
		byID[nb.ID] = nb
	}
	if byID[root.ID].ParentID != nil {
		t.Errorf("listed root parent_id should be nil, got %v", byID[root.ID].ParentID)
	}
	if byID[child.ID].ParentID == nil || *byID[child.ID].ParentID != root.ID {
		t.Errorf("listed child parent_id = %v, want %s", byID[child.ID].ParentID, root.ID)
	}
}

func TestNotebookHierarchy_InvalidParentRejected(t *testing.T) {
	h := newStoreHarness(t)
	uid, other := uuid.New(), uuid.New()
	defer h.cleanupNotes(t, uid)
	defer h.cleanupNotes(t, other)

	foreign := createNotebook(t, h, other, "别人的本", nil)
	for name, pid := range map[string]uuid.UUID{
		"missing": uuid.New(),
		"foreign": foreign.ID,
	} {
		if _, err := h.st.CreateNotebook(context.Background(), uid, "子", 0, &pid, uid.String()); !errors.Is(err, ErrInvalidParent) {
			t.Errorf("%s parent: expected ErrInvalidParent, got %v", name, err)
		}
	}
	// 已软删的本也不能当父本。
	trashed := createNotebook(t, h, uid, "回收站", nil)
	if err := h.st.SoftDeleteNotebook(context.Background(), trashed.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNotebook: %v", err)
	}
	if _, err := h.st.CreateNotebook(context.Background(), uid, "子", 0, &trashed.ID, uid.String()); !errors.Is(err, ErrInvalidParent) {
		t.Errorf("trashed parent: expected ErrInvalidParent, got %v", err)
	}
}

func TestNotebookHierarchy_NameUniqueWithinParent(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	a := createNotebook(t, h, uid, "目录A", nil)
	b := createNotebook(t, h, uid, "目录B", nil)
	createNotebook(t, h, uid, "读书笔记", &a.ID)

	// 同父下同名（大小写不同也算）拒绝 —— DB 唯一索引冲突。
	if _, err := h.st.CreateNotebook(context.Background(), uid, "读书笔记", 0, &a.ID, uid.String()); err == nil {
		t.Errorf("same name under same parent should be rejected")
	}
	createNotebook(t, h, uid, "Reading", &a.ID)
	if _, err := h.st.CreateNotebook(context.Background(), uid, "reading", 0, &a.ID, uid.String()); err == nil {
		t.Errorf("case-insensitive same name under same parent should be rejected")
	}
	// 不同父下同名允许；根级与目录内同名也允许（不同 parent）。
	createNotebook(t, h, uid, "读书笔记", &b.ID)
	createNotebook(t, h, uid, "读书笔记", nil)
	// 根级之间同名拒绝（NULLS NOT DISTINCT）。
	if _, err := h.st.CreateNotebook(context.Background(), uid, "目录A", 0, nil, uid.String()); err == nil {
		t.Errorf("same name at root should be rejected")
	}
}

func TestNotebookHierarchy_DepthLimit(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	// 一路建到第 5 层（根=1）。
	parent := createNotebook(t, h, uid, "L1", nil)
	for level := 2; level <= maxNotebookDepth; level++ {
		parent = createNotebook(t, h, uid, fmt.Sprintf("L%d", level), &parent.ID)
	}
	// 第 6 层拒绝。
	if _, err := h.st.CreateNotebook(context.Background(), uid, "L6", 0, &parent.ID, uid.String()); !errors.Is(err, ErrNotebookDepth) {
		t.Fatalf("level 6: expected ErrNotebookDepth, got %v", err)
	}
}

func TestNotebookHierarchy_Reparent(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	a := createNotebook(t, h, uid, "A", nil)
	b := createNotebook(t, h, uid, "B", nil)
	c := createNotebook(t, h, uid, "C", &b.ID)

	// 移到自身拒绝。
	if _, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: c.ID, UserID: uid, ParentID: &c.ID, ActorID: uid.String(),
	}); !errors.Is(err, ErrNotebookCycle) {
		t.Fatalf("self-parent: expected ErrNotebookCycle, got %v", err)
	}
	// 移到后代拒绝：B → C 下（C 是 B 的后代）。
	if _, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: b.ID, UserID: uid, ParentID: &c.ID, ActorID: uid.String(),
	}); !errors.Is(err, ErrNotebookCycle) {
		t.Fatalf("descendant-parent: expected ErrNotebookCycle, got %v", err)
	}
	// 正常移动：C → A 下。
	nb, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: c.ID, UserID: uid, ParentID: &a.ID, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("reparent C under A: %v", err)
	}
	if nb.ParentID == nil || *nb.ParentID != a.ID {
		t.Fatalf("C parent_id = %v, want %s", nb.ParentID, a.ID)
	}
	// 升到根。
	nb, err = h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: c.ID, UserID: uid, MoveToRoot: true, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("move C to root: %v", err)
	}
	if nb.ParentID != nil {
		t.Fatalf("C parent_id should be nil after MoveToRoot, got %v", nb.ParentID)
	}
	// 别人的本不能当父本。
	other := uuid.New()
	defer h.cleanupNotes(t, other)
	foreign := createNotebook(t, h, other, "别人", nil)
	if _, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: c.ID, UserID: uid, ParentID: &foreign.ID, ActorID: uid.String(),
	}); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("foreign parent: expected ErrInvalidParent, got %v", err)
	}
}

func TestNotebookHierarchy_ReparentDepthLimit(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	// 链 A1→A2→A3→A4→A5（5 层），另起根 B。
	a1 := createNotebook(t, h, uid, "A1", nil)
	parent := a1
	for level := 2; level <= maxNotebookDepth; level++ {
		parent = createNotebook(t, h, uid, fmt.Sprintf("A%d", level), &parent.ID)
	}
	b := createNotebook(t, h, uid, "B", nil)

	// A1（子树高 5）挂到 B（深度 1）下 → 5+1=6 超深，拒绝。
	if _, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: a1.ID, UserID: uid, ParentID: &b.ID, ActorID: uid.String(),
	}); !errors.Is(err, ErrNotebookDepth) {
		t.Fatalf("subtree too deep: expected ErrNotebookDepth, got %v", err)
	}
	// A5（叶子，子树高 1）挂到 B 下 → 2 层，允许。
	if _, err := h.st.UpdateNotebook(context.Background(), UpdateNotebookInput{
		ID: parent.ID, UserID: uid, ParentID: &b.ID, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("leaf reparent should succeed: %v", err)
	}
}

func TestNotebookHierarchy_SoftDeletePromotesChildren(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupNotes(t, uid)

	a := createNotebook(t, h, uid, "A", nil)
	b := createNotebook(t, h, uid, "B", &a.ID)
	c := createNotebook(t, h, uid, "C", &b.ID)

	// 软删 B → C 上移到 A 下。
	if err := h.st.SoftDeleteNotebook(context.Background(), b.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNotebook(B): %v", err)
	}
	got := getNotebook(t, h, uid, c.ID)
	if got.ParentID == nil || *got.ParentID != a.ID {
		t.Fatalf("after deleting B, C parent_id = %v, want %s", got.ParentID, a.ID)
	}
	// 软删根 A → C 变根。
	if err := h.st.SoftDeleteNotebook(context.Background(), a.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNotebook(A): %v", err)
	}
	got = getNotebook(t, h, uid, c.ID)
	if got.ParentID != nil {
		t.Fatalf("after deleting root A, C parent_id should be nil, got %v", got.ParentID)
	}
}
