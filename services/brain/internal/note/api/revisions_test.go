// HTTP handler tests for the notes revisions endpoints — same harness and
// skip convention as api_test.go.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
)

func (h *apiHarness) post(t *testing.T, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, _ := http.NewRequest("POST", h.server.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (h *apiHarness) put(t *testing.T, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req, _ := http.NewRequest("PUT", h.server.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// mkNoteWithRevision —— 建一篇笔记并做一次编辑，返回 (noteID, revisionID)。
func mkNoteWithRevision(t *testing.T, h *apiHarness, uid uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	n, _, err := h.st.CreateNote(ctx, store.CreateNoteInput{
		UserID: uid, Title: "旧标题", ContentMD: "旧正文", ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	title, content := "新标题", "新正文"
	if _, err := h.st.UpdateNote(ctx, store.UpdateNoteInput{
		ID: n.ID, UserID: uid, Title: &title, ContentMD: &content, ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	revs, err := h.st.ListRevisions(ctx, n.ID, uid, 20, 0)
	if err != nil || len(revs) != 1 {
		t.Fatalf("ListRevisions: %v len=%d", err, len(revs))
	}
	return n.ID, revs[0].ID
}

func TestRevisionsAPI_ListShapeAndLimitClamp(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, _ := mkNoteWithRevision(t, h, uid)

	// limit 超上限静默收敛，不报错。
	status, body := h.get(t, "/v1/notes/"+noteID.String()+"/revisions?limit=500", h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("expected 200 got %d (%v)", status, body)
	}
	revs, ok := body["revisions"].([]any)
	if !ok || len(revs) != 1 {
		t.Fatalf("revisions: %v", body)
	}
	item := revs[0].(map[string]any)
	if item["change_type"] != "edit" {
		t.Fatalf("change_type = %v", item["change_type"])
	}
	if item["title"] != "旧标题" {
		t.Fatalf("title = %v, want 旧标题", item["title"])
	}
	if _, has := item["content_md"]; has {
		t.Fatalf("list item must not carry content_md: %v", item)
	}
}

func TestRevisionsAPI_GetHasFullContent(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, rid := mkNoteWithRevision(t, h, uid)

	status, body := h.get(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String(), h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("expected 200 got %d (%v)", status, body)
	}
	if body["content_md"] != "旧正文" || body["title"] != "旧标题" {
		t.Fatalf("revision body: %v", body)
	}
}

func TestRevisionsAPI_Restore(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, rid := mkNoteWithRevision(t, h, uid)

	status, body := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/restore",
		h.mintToken(uid), nil)
	if status != http.StatusOK {
		t.Fatalf("restore: expected 200 got %d (%v)", status, body)
	}
	if body["title"] != "旧标题" || body["content_md"] != "旧正文" {
		t.Fatalf("restored note: %v", body)
	}
	if v, _ := body["version"].(float64); v != 3 {
		t.Fatalf("version = %v, want 3 (create=1, edit=2, restore=3)", body["version"])
	}

	// 自动备份版本已落库且带固定摘要。
	revs, err := h.st.ListRevisions(context.Background(), noteID, uid, 20, 0)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revs) != 2 || revs[0].ChangeType != "restore" {
		t.Fatalf("revisions after restore: %+v", revs)
	}
	if revs[0].ChangeSummary == nil || *revs[0].ChangeSummary != store.RevisionRestoreSummary {
		t.Fatalf("backup summary = %v", revs[0].ChangeSummary)
	}
}

func TestRevisionsAPI_RestoreTrashedNote404(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, rid := mkNoteWithRevision(t, h, uid)

	if err := h.st.SoftDeleteNote(context.Background(), noteID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNote: %v", err)
	}
	status, _ := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/restore",
		h.mintToken(uid), nil)
	if status != http.StatusNotFound {
		t.Fatalf("restore trashed note: expected 404 got %d", status)
	}
}

func TestRevisionsAPI_SaveAsCopy(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, rid := mkNoteWithRevision(t, h, uid)

	status, body := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/save-as-copy",
		h.mintToken(uid), nil)
	if status != http.StatusOK {
		t.Fatalf("save-as-copy: expected 200 got %d (%v)", status, body)
	}
	if body["id"] == noteID.String() {
		t.Fatalf("copy must be a new note")
	}
	if body["title"] != "旧标题"+store.RevisionCopySuffix {
		t.Fatalf("copy title = %v", body["title"])
	}
	if body["content_md"] != "旧正文" {
		t.Fatalf("copy content = %v", body["content_md"])
	}
}

func TestRevisionsAPI_RequiresAuth(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	noteID, rid := mkNoteWithRevision(t, h, uid)

	if status, _ := h.get(t, "/v1/notes/"+noteID.String()+"/revisions", ""); status != http.StatusUnauthorized {
		t.Fatalf("list without token: expected 401 got %d", status)
	}
	if status, _ := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/restore", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("restore without token: expected 401 got %d", status)
	}
}

func TestRevisionsAPI_OtherUserGets404(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uidA, uidB := uuid.New(), uuid.New()
	defer h.cleanupUser(t, uidA)
	defer h.cleanupUser(t, uidB)
	noteID, rid := mkNoteWithRevision(t, h, uidA)

	if status, _ := h.get(t, "/v1/notes/"+noteID.String()+"/revisions", h.mintToken(uidB)); status != http.StatusNotFound {
		t.Fatalf("list as other user: expected 404 got %d", status)
	}
	if status, _ := h.get(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String(), h.mintToken(uidB)); status != http.StatusNotFound {
		t.Fatalf("get as other user: expected 404 got %d", status)
	}
	if status, _ := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/restore", h.mintToken(uidB), nil); status != http.StatusNotFound {
		t.Fatalf("restore as other user: expected 404 got %d", status)
	}
	if status, _ := h.post(t, "/v1/notes/"+noteID.String()+"/revisions/"+rid.String()+"/save-as-copy", h.mintToken(uidB), nil); status != http.StatusNotFound {
		t.Fatalf("save-as-copy as other user: expected 404 got %d", status)
	}
}

// 确保窗口合并在 API 路径同样生效：连续两次 PUT 只产生一条 edit 版本。
func TestRevisionsAPI_WindowMergeViaUpdate(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	token := h.mintToken(uid)

	status, body := h.post(t, "/v1/notes", token, map[string]any{"title": "v0", "content_md": "c0"})
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, body)
	}
	noteID := body["id"].(string)
	for _, title := range []string{"v1", "v2"} {
		status, body = h.put(t, "/v1/notes/"+noteID, token, map[string]any{"title": title})
		if status != http.StatusOK {
			t.Fatalf("update %s: %d %v", title, status, body)
		}
	}
	time.Sleep(100 * time.Millisecond)
	status, body = h.get(t, "/v1/notes/"+noteID+"/revisions", token)
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, body)
	}
	if revs := body["revisions"].([]any); len(revs) != 1 {
		t.Fatalf("window merge via API: expected 1 revision, got %d (%v)", len(revs), body)
	}
}
