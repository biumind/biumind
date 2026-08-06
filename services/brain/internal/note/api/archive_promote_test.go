// HTTP handler tests for N3: 归档过滤 / unarchive / promote（转入知识库）。
//
// Skips when DATABASE_URL unset (same convention as api_test.go).

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/note/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type n3Harness struct {
	server *httptest.Server
	signer *bauth.Signer
	pool   *pgxpool.Pool
	st     *store.Store
	wiki   *wikistore.Store
}

func newN3Harness(t *testing.T) *n3Harness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	st := store.New(pool)
	wiki := wikistore.New(pool)
	srv := NewServer(st,
		bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithWiki(wiki)
	mux := http.NewServeMux()
	srv.Mount(mux)
	return &n3Harness{
		server: httptest.NewServer(mux),
		signer: bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute),
		pool:   pool,
		st:     st,
		wiki:   wiki,
	}
}

func (h *n3Harness) close() {
	h.server.Close()
	h.pool.Close()
}

func (h *n3Harness) mintToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

// cleanupUser 清掉该用户的 notes / wiki pages / projects / 两边事件。
func (h *n3Harness) cleanupUser(t *testing.T, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_notes WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup notes: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		DELETE FROM brain.blocks WHERE page_id IN (
			SELECT p.id FROM brain.pages p JOIN brain.projects pr ON pr.id = p.project_id
			WHERE pr.owner_id = $1)`, uid); err != nil {
		t.Fatalf("cleanup blocks: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		DELETE FROM brain.pages WHERE project_id IN (
			SELECT id FROM brain.projects WHERE owner_id = $1)`, uid); err != nil {
		t.Fatalf("cleanup pages: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.projects WHERE owner_id = $1`, uid); err != nil {
		t.Fatalf("cleanup projects: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		DELETE FROM brain.events WHERE scope = $1 OR scope LIKE 'wiki:project:%'`, "note:user:"+uid.String()); err != nil {
		t.Fatalf("cleanup events: %v", err)
	}
}

func (h *n3Harness) get(t *testing.T, path, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", h.server.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func (h *n3Harness) post(t *testing.T, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", h.server.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func (h *n3Harness) mkNote(t *testing.T, uid uuid.UUID, title, content string) *store.Note {
	t.Helper()
	n, _, err := h.st.CreateNote(context.Background(), store.CreateNoteInput{
		UserID: uid, Title: title, ContentMD: content, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	return n
}

func noteIDs(body map[string]any) map[string]bool {
	out := map[string]bool{}
	notes, _ := body["notes"].([]any)
	for _, raw := range notes {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		out[id] = true
	}
	return out
}

// ─── 子项 1：来源字段 + 归档过滤 + unarchive ─────────────

func TestCreateNote_SourceFieldsRoundTrip(t *testing.T) {
	h := newN3Harness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)

	status, body := h.post(t, "/v1/notes", h.mintToken(uid), map[string]any{
		"title": "剪藏", "content_md": "正文",
		"source_url": "https://example.com/a", "author": "张三",
	})
	if status != http.StatusOK {
		t.Fatalf("create: %d (%v)", status, body)
	}
	if body["source_url"] != "https://example.com/a" || body["author"] != "张三" {
		t.Fatalf("source fields not echoed: %v", body)
	}
	if body["archived_at"] != nil || body["promoted_page_id"] != nil {
		t.Fatalf("new note should be unarchived/unpromoted: %v", body)
	}

	// 不传时四个新字段也应在序列化里（null）。
	status, body = h.post(t, "/v1/notes", h.mintToken(uid), map[string]any{"title": "x"})
	if status != http.StatusOK {
		t.Fatalf("create2: %d (%v)", status, body)
	}
	for _, k := range []string{"source_url", "author", "archived_at", "promoted_page_id"} {
		if v, present := body[k]; !present || v != nil {
			t.Fatalf("key %s: present=%v value=%v", k, present, v)
		}
	}
}

func TestArchiveFilter_ListAndSearch(t *testing.T) {
	h := newN3Harness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)

	token := "霁月光风"
	live := h.mkNote(t, uid, "活笔记", token+" 在正文里")
	arch := h.mkNote(t, uid, "归档笔记", token+" 也在正文里")
	if _, err := h.st.ArchiveNote(context.Background(), arch.ID, uid, uid.String()); err != nil {
		t.Fatalf("ArchiveNote: %v", err)
	}

	// 默认列表排除已归档。
	status, body := h.get(t, "/v1/notes", h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("list: %d", status)
	}
	ids := noteIDs(body)
	if !ids[live.ID.String()] || ids[arch.ID.String()] {
		t.Fatalf("default list should exclude archived: %v", ids)
	}

	// archived=only 只看已归档。
	status, body = h.get(t, "/v1/notes?archived=only", h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("list archived=only: %d", status)
	}
	ids = noteIDs(body)
	if ids[live.ID.String()] || !ids[arch.ID.String()] {
		t.Fatalf("archived=only should only show archived: %v", ids)
	}

	// 搜索同样排除已归档。
	status, body = h.get(t, "/v1/notes/search?q="+token, h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("search: %d", status)
	}
	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("search should hit only the live note, got %d (%v)", len(results), body)
	}
}

func TestUnarchive_PreservesPromotedPageID(t *testing.T) {
	h := newN3Harness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)

	n := h.mkNote(t, uid, "t", "c")
	pageID := uuid.New()
	if _, err := h.st.MarkPromoted(context.Background(), n.ID, uid, pageID, uid.String()); err != nil {
		t.Fatalf("MarkPromoted: %v", err)
	}

	status, body := h.post(t, "/v1/notes/"+n.ID.String()+"/unarchive", h.mintToken(uid), nil)
	if status != http.StatusOK {
		t.Fatalf("unarchive: %d (%v)", status, body)
	}
	if body["archived_at"] != nil {
		t.Fatalf("archived_at should be null after unarchive: %v", body)
	}
	if body["promoted_page_id"] != pageID.String() {
		t.Fatalf("promoted_page_id should survive unarchive: %v", body)
	}

	// 未归档笔记 unarchive 幂等返回 200。
	status, _ = h.post(t, "/v1/notes/"+n.ID.String()+"/unarchive", h.mintToken(uid), nil)
	if status != http.StatusOK {
		t.Fatalf("unarchive replay: %d", status)
	}

	// 他人笔记 404。
	status, _ = h.post(t, "/v1/notes/"+n.ID.String()+"/unarchive", h.mintToken(uuid.New()), nil)
	if status != http.StatusNotFound {
		t.Fatalf("other user unarchive: expected 404 got %d", status)
	}
}

// ─── 子项 2：promote ────────────────────────────────────

func TestPromote_SuccessAndIdempotentReplay(t *testing.T) {
	h := newN3Harness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)

	proj, err := h.wiki.CreateProjectWithTemplate(context.Background(), uid, "知识库", "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n := h.mkNote(t, uid, "笔记标题", "第一段。\n\n第二段。")

	status, body := h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": proj.ID.String()})
	if status != http.StatusOK {
		t.Fatalf("promote: %d (%v)", status, body)
	}
	page, _ := body["page"].(map[string]any)
	note, _ := body["note"].(map[string]any)
	if page == nil || note == nil {
		t.Fatalf("response should carry page+note: %v", body)
	}
	if page["title"] != "笔记标题" {
		t.Fatalf("page title: %v", page)
	}
	if page["project_id"] != proj.ID.String() {
		t.Fatalf("page project: %v", page)
	}
	if note["archived_at"] == nil {
		t.Fatalf("promote should archive the note: %v", note)
	}
	if note["promoted_page_id"] != page["id"] {
		t.Fatalf("promoted_page_id should equal page id: %v", note)
	}

	// page 内容落地：两段 → 两个 text block。
	var blockCount int
	if err := h.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM brain.blocks WHERE page_id = $1 AND deleted_at IS NULL
	`, page["id"].(string)).Scan(&blockCount); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if blockCount != 2 {
		t.Fatalf("expected 2 paragraph blocks, got %d", blockCount)
	}

	// promote 后笔记从默认列表消失（已归档）。
	_, listBody := h.get(t, "/v1/notes", h.mintToken(uid))
	if noteIDs(listBody)[n.ID.String()] {
		t.Fatalf("promoted note should be archived out of default list")
	}

	// 重复 promote —— 幂等重放：同 page、不新建、带 idempotent_replay。
	status, body2 := h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": proj.ID.String()})
	if status != http.StatusOK {
		t.Fatalf("promote replay: %d (%v)", status, body2)
	}
	page2, _ := body2["page"].(map[string]any)
	if page2["id"] != page["id"] {
		t.Fatalf("replay must return the same page: %v vs %v", page2["id"], page["id"])
	}
	if body2["idempotent_replay"] != true {
		t.Fatalf("replay should be flagged: %v", body2)
	}
	var pageCount int
	if err := h.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM brain.pages WHERE project_id = $1 AND deleted_at IS NULL
	`, proj.ID).Scan(&pageCount); err != nil {
		t.Fatalf("count pages: %v", err)
	}
	if pageCount != 1 {
		t.Fatalf("replay must not create a second page, got %d", pageCount)
	}
}

func TestPromote_Rejections(t *testing.T) {
	h := newN3Harness(t)
	defer h.close()
	uid := uuid.New()
	other := uuid.New()
	defer h.cleanupUser(t, uid)
	defer h.cleanupUser(t, other)

	proj, err := h.wiki.CreateProjectWithTemplate(context.Background(), uid, "我的库", "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n := h.mkNote(t, uid, "t", "c")

	// 他人笔记 → 404（用户隔离）。
	status, _ := h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(other),
		map[string]any{"project_id": proj.ID.String()})
	if status != http.StatusNotFound {
		t.Fatalf("other user's note: expected 404 got %d", status)
	}

	// 他人 project → 403。
	otherProj, err := h.wiki.CreateProjectWithTemplate(context.Background(), other, "别人的库", "", nil)
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	status, _ = h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": otherProj.ID.String()})
	if status != http.StatusForbidden {
		t.Fatalf("other's project: expected 403 got %d", status)
	}

	// 不存在的 project → 404。
	status, _ = h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": uuid.New().String()})
	if status != http.StatusNotFound {
		t.Fatalf("missing project: expected 404 got %d", status)
	}

	// 已归档（未 promote）笔记 → 409。
	if _, err := h.st.ArchiveNote(context.Background(), n.ID, uid, uid.String()); err != nil {
		t.Fatalf("ArchiveNote: %v", err)
	}
	status, _ = h.post(t, "/v1/notes/"+n.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": proj.ID.String()})
	if status != http.StatusConflict {
		t.Fatalf("archived note: expected 409 got %d", status)
	}

	// 回收站笔记 → 404。
	n2 := h.mkNote(t, uid, "t2", "c2")
	if err := h.st.SoftDeleteNote(context.Background(), n2.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNote: %v", err)
	}
	status, _ = h.post(t, "/v1/notes/"+n2.ID.String()+"/promote", h.mintToken(uid),
		map[string]any{"project_id": proj.ID.String()})
	if status != http.StatusNotFound {
		t.Fatalf("trashed note: expected 404 got %d", status)
	}
}
