// Store tests against real Postgres (tsv 生成列 + zhparser 分词需要真库)。
// Skips when DATABASE_URL unset — same convention as internal/files tests.

package store

import (
	"context"
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

// cleanupNotes — 物理清掉测试用户的笔记（事件/关联随行删除）。
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
