// Package api implements Brain.Notes HTTP endpoints.
//
//	POST   /v1/notebooks                 create notebook
//	GET    /v1/notebooks                 list mine
//	PUT    /v1/notebooks/{id}            update (name/position)
//	DELETE /v1/notebooks/{id}            soft delete
//	POST   /v1/notes                     create (客户端可带 id，主键冲突幂等返回)
//	GET    /v1/notes                     list (notebook_id/tag/todo 过滤；默认排除已归档，archived=only 只看归档)
//	GET    /v1/notes/search?q=&limit=    全文搜索 (zhparser tsv + ts_headline；排除已归档)
//	GET    /v1/notes/{id}                get one
//	PUT    /v1/notes/{id}                update (If-Match version)
//	DELETE /v1/notes/{id}                soft delete (回收站)
//	GET    /v1/notes/trash               回收站列表
//	POST   /v1/notes/{id}/restore        还原（父笔记本已删则置根）
//	POST   /v1/notes/{id}/archive        归档（区别于回收站；幂等）
//	POST   /v1/notes/{id}/unarchive      归档还原（promoted_page_id 保留）
//	POST   /v1/notes/{id}/promote        转入知识库（建 wiki page + 归档回链；幂等）
//	DELETE /v1/notes/{id}/purge          物理删除
//	POST   /v1/note-tags                 create tag (幂等)
//	GET    /v1/note-tags                 list mine
//	PUT    /v1/notes/{id}/tags           整组替换标签关联
//	GET    /v1/notes/changes?since=N     增量事件流 (scope=note:user:<uid>)
//	GET    /v1/notes/{id}/revisions                  版本列表（不含 content_md）
//	GET    /v1/notes/{id}/revisions/{rid}            单个版本（含完整内容）
//	POST   /v1/notes/{id}/revisions/{rid}/restore    覆盖式恢复（先自动备份当前状态）
//	POST   /v1/notes/{id}/revisions/{rid}/save-as-copy 以该版本另存新笔记
//
// 笔记分享（§7.6）：管理端见 share.go（随 Mount 挂载，requireAuth）；
// 公开端见 share_public.go（MountPublic 单独挂载，无鉴权 —— brain 首批
// 公开业务路由）。
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	filespkg "github.com/biumind/biumind/services/brain/internal/files"
	"github.com/biumind/biumind/services/brain/internal/note/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type Server struct {
	Store    *store.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
	// Wiki —— 「转入知识库」（promote）在同进程内直调 wiki store 建页。
	// nil 时 promote 端点返回 503。
	Wiki *wikistore.Store
	// ShareSigningKey —— 笔记分享访问 JWT 的 HS256 密钥
	// （env BRAIN_SHARE_SIGNING_KEY；main 在为空时随机生成 + warn）。
	ShareSigningKey []byte
	// ShareBlob —— 分享附件代理的 presign 后端（复用 files 域 Blob）。
	// nil（MINIO_ENDPOINT 未配置）时 files 代理路由 503 files_unavailable；
	// main 在 files 装配后回填（挂载顺序见 cmd/brain/main.go）。
	ShareBlob *filespkg.Blob
}

func NewServer(s *store.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Verifier: v, Logger: l}
}

// WithWiki 注入 wiki store（promote 用），返回自身便于装配链式调用。
func (s *Server) WithWiki(w *wikistore.Store) *Server {
	s.Wiki = w
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/notebooks", s.requireAuth(s.handleCreateNotebook))
	mux.HandleFunc("GET /v1/notebooks", s.requireAuth(s.handleListNotebooks))
	mux.HandleFunc("PUT /v1/notebooks/{id}", s.requireAuth(s.handleUpdateNotebook))
	mux.HandleFunc("DELETE /v1/notebooks/{id}", s.requireAuth(s.handleDeleteNotebook))

	// trash/changes/search 是字面量段，Go 1.22 ServeMux 按 specificity 优先于 {id}。
	mux.HandleFunc("POST /v1/notes", s.requireAuth(s.handleCreateNote))
	mux.HandleFunc("GET /v1/notes", s.requireAuth(s.handleListNotes))
	mux.HandleFunc("GET /v1/notes/trash", s.requireAuth(s.handleListTrash))
	mux.HandleFunc("GET /v1/notes/changes", s.requireAuth(s.handleChanges))
	mux.HandleFunc("GET /v1/notes/search", s.requireAuth(s.handleSearchNotes))
	mux.HandleFunc("GET /v1/notes/{id}", s.requireAuth(s.handleGetNote))
	mux.HandleFunc("PUT /v1/notes/{id}", s.requireAuth(s.handleUpdateNote))
	mux.HandleFunc("DELETE /v1/notes/{id}", s.requireAuth(s.handleDeleteNote))
	mux.HandleFunc("POST /v1/notes/{id}/restore", s.requireAuth(s.handleRestoreNote))
	mux.HandleFunc("POST /v1/notes/{id}/archive", s.requireAuth(s.handleArchiveNote))
	mux.HandleFunc("POST /v1/notes/{id}/unarchive", s.requireAuth(s.handleUnarchiveNote))
	mux.HandleFunc("POST /v1/notes/{id}/promote", s.requireAuth(s.handlePromoteNote))
	mux.HandleFunc("DELETE /v1/notes/{id}/purge", s.requireAuth(s.handlePurgeNote))
	mux.HandleFunc("PUT /v1/notes/{id}/tags", s.requireAuth(s.handleSetNoteTags))

	mux.HandleFunc("GET /v1/notes/{id}/revisions", s.requireAuth(s.handleListRevisions))
	mux.HandleFunc("GET /v1/notes/{id}/revisions/{rid}", s.requireAuth(s.handleGetRevision))
	mux.HandleFunc("POST /v1/notes/{id}/revisions/{rid}/restore", s.requireAuth(s.handleRestoreRevision))
	mux.HandleFunc("POST /v1/notes/{id}/revisions/{rid}/save-as-copy", s.requireAuth(s.handleSaveRevisionAsCopy))

	mux.HandleFunc("POST /v1/note-tags", s.requireAuth(s.handleCreateTag))
	mux.HandleFunc("GET /v1/note-tags", s.requireAuth(s.handleListTags))

	// 笔记分享管理端（§7.6）。「shares」是字面量段，ServeMux 按
	// specificity 优先于 {id}；公开端 /v1/shares/* 走 MountPublic。
	mux.HandleFunc("PUT /v1/notes/{id}/share", s.requireAuth(s.handlePutShare))
	mux.HandleFunc("GET /v1/notes/{id}/share", s.requireAuth(s.handleGetShare))
	mux.HandleFunc("DELETE /v1/notes/{id}/share", s.requireAuth(s.handleDeleteShare))
	mux.HandleFunc("POST /v1/notes/{id}/share/rotate", s.requireAuth(s.handleRotateShare))
	mux.HandleFunc("GET /v1/notes/shares", s.requireAuth(s.handleListShares))
}

// ─── Notebooks ──────────────────────────────────────────

type createNotebookReq struct {
	Name     string   `json:"name"`
	Position *float64 `json:"position"`
	// ParentID —— 可选；缺省/空串 = 根级（多级目录，迁移 00003）。
	ParentID *string `json:"parent_id"`
}

func (s *Server) handleCreateNotebook(w http.ResponseWriter, r *http.Request) {
	var req createNotebookReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "missing_name", "")
		return
	}
	pos := 0.0
	if req.Position != nil {
		pos = *req.Position
	}
	var parentID *uuid.UUID
	if req.ParentID != nil && *req.ParentID != "" {
		u, err := uuid.Parse(*req.ParentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_parent_id", "")
			return
		}
		parentID = &u
	}
	uid := mustUserID(r)
	nb, err := s.Store.CreateNotebook(r.Context(), uid, req.Name, pos, parentID, uid.String())
	if err != nil {
		if writeNotebookErr(w, err) {
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notebookOut(nb))
}

// writeNotebookErr —— notebook 层级校验错误 → 4xx（对齐 bad_notebook_id
// 的 400 惯例）；已处理返回 true。
func writeNotebookErr(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrInvalidParent):
		writeErr(w, http.StatusBadRequest, "bad_parent_id", "parent notebook not found or deleted")
	case errors.Is(err, store.ErrNotebookCycle):
		writeErr(w, http.StatusBadRequest, "notebook_cycle", "parent would create a cycle")
	case errors.Is(err, store.ErrNotebookDepth):
		writeErr(w, http.StatusBadRequest, "depth_limit", "notebook hierarchy too deep")
	case errors.Is(err, store.ErrDuplicateName):
		writeErr(w, http.StatusConflict, "name_conflict", "notebook name already exists in parent")
	default:
		return false
	}
	return true
}

func (s *Server) handleListNotebooks(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	nbs, err := s.Store.ListNotebooks(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(nbs))
	for _, nb := range nbs {
		out = append(out, notebookOut(nb))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notebooks": out})
}

type updateNotebookReq struct {
	Name     *string  `json:"name"`
	Position *float64 `json:"position"`
	// ParentID —— nil = 不动；"" = 升到根；合法 uuid = 移到该父本
	// （同 handleUpdateNote 的 notebook_id 惯例）。
	ParentID *string `json:"parent_id"`
}

func (s *Server) handleUpdateNotebook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req updateNotebookReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := mustUserID(r)
	in := store.UpdateNotebookInput{
		ID: id, UserID: uid, Name: req.Name, Position: req.Position, ActorID: uid.String(),
	}
	if req.ParentID != nil {
		if *req.ParentID == "" {
			in.MoveToRoot = true
		} else {
			u, err := uuid.Parse(*req.ParentID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad_parent_id", "")
				return
			}
			in.ParentID = &u
		}
	}
	nb, err := s.Store.UpdateNotebook(r.Context(), in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if writeNotebookErr(w, err) {
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notebookOut(nb))
}

func (s *Server) handleDeleteNotebook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	if err := s.Store.SoftDeleteNotebook(r.Context(), id, uid, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// ─── Notes ──────────────────────────────────────────────

type createNoteReq struct {
	ID              string   `json:"id"` // 客户端生成的 uuid（可选）
	NotebookID      *string  `json:"notebook_id"`
	Title           string   `json:"title"`
	ContentMD       string   `json:"content_md"`
	IsTodo          bool     `json:"is_todo"`
	TodoCompletedAt *string  `json:"todo_completed_at"` // RFC3339
	Position        *float64 `json:"position"`
	SourceURL       *string  `json:"source_url"` // webclip 剪藏来源（可选）
	Author          *string  `json:"author"`
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var req createNoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := mustUserID(r)

	var id *uuid.UUID
	if req.ID != "" {
		u, err := uuid.Parse(req.ID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_id", "")
			return
		}
		id = &u
	}
	notebookID, ok := s.parseNotebookID(w, r, req.NotebookID)
	if !ok {
		return
	}
	todoCompletedAt, ok := parseTimeOpt(w, req.TodoCompletedAt)
	if !ok {
		return
	}
	pos := 0.0
	if req.Position != nil {
		pos = *req.Position
	}
	n, replayed, err := s.Store.CreateNote(r.Context(), store.CreateNoteInput{
		ID: id, UserID: uid, NotebookID: notebookID,
		Title: req.Title, ContentMD: req.ContentMD,
		IsTodo: req.IsTodo, TodoCompletedAt: todoCompletedAt,
		Position: pos, SourceURL: req.SourceURL, Author: req.Author,
		ActorID: uid.String(),
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := noteOut(n)
	if replayed {
		out["idempotent_replay"] = true
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	q := r.URL.Query()
	f := store.ListNotesFilter{UserID: uid, Tag: q.Get("tag")}

	if nb := q.Get("notebook_id"); nb != "" {
		if nb == "root" {
			f.RootOnly = true
		} else {
			u, err := uuid.Parse(nb)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad_notebook_id", "")
				return
			}
			f.NotebookID = &u
		}
	}
	f.TodoOnly = q.Get("todo") == "true"
	// archived=only 只看已归档；默认（含其它值）排除已归档。回收站语义不变。
	if q.Get("archived") == "only" {
		f.Archived = "only"
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))

	ns, err := s.Store.ListNotes(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		out = append(out, noteOut(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": out})
}

func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ns, err := s.Store.ListTrash(r.Context(), uid, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		out = append(out, noteOut(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": out})
}

// handleSearchNotes —— GET /v1/notes/search?q=<text>&limit=<n>。
// q 空 → 400；limit 默认 20、超过上限 50 收敛到 50。
func (s *Server) handleSearchNotes(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeErr(w, http.StatusBadRequest, "bad_query", "q required")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = store.SearchDefaultLimit
	}
	if limit > store.SearchMaxLimit {
		limit = store.SearchMaxLimit
	}
	hits, err := s.Store.SearchNotes(r.Context(), uid, query, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchHitOut(&h))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.GetNote(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

type updateNoteReq struct {
	Title           *string  `json:"title"`
	ContentMD       *string  `json:"content_md"`
	NotebookID      *string  `json:"notebook_id"` // 存在且为 "" = 移回根
	IsTodo          *bool    `json:"is_todo"`
	TodoCompletedAt *string  `json:"todo_completed_at"` // RFC3339；存在且为 "" = 清除
	Position        *float64 `json:"position"`
	SourceURL       *string  `json:"source_url"`
	Author          *string  `json:"author"`
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req updateNoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := mustUserID(r)

	in := store.UpdateNoteInput{
		ID: id, UserID: uid, Title: req.Title, ContentMD: req.ContentMD,
		IsTodo: req.IsTodo, Position: req.Position,
		SourceURL: req.SourceURL, Author: req.Author,
		ActorID: uid.String(),
	}
	ifMatch, _ := strconv.Atoi(r.Header.Get("If-Match"))
	in.IfMatchVersion = ifMatch

	if req.NotebookID != nil {
		if *req.NotebookID == "" {
			in.MoveToRoot = true
		} else {
			u, err := uuid.Parse(*req.NotebookID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad_notebook_id", "")
				return
			}
			if _, alive, err := s.Store.GetNotebook(r.Context(), u, uid); err != nil || !alive {
				writeErr(w, http.StatusBadRequest, "bad_notebook_id", "notebook not found or deleted")
				return
			}
			in.NotebookID = &u
		}
	}
	if req.TodoCompletedAt != nil {
		if *req.TodoCompletedAt == "" {
			in.ClearTodoCompleted = true
		} else {
			t, err := time.Parse(time.RFC3339, *req.TodoCompletedAt)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad_todo_completed_at", "")
				return
			}
			in.TodoCompletedAt = &t
		}
	}

	n, err := s.Store.UpdateNote(r.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "")
		case errors.Is(err, store.ErrConflict):
			// 409 带服务端当前版本与内容，客户端做用户裁决（设计 §4 D4）。
			body := map[string]any{
				"error": map[string]any{
					"code": "version_conflict", "message": "If-Match version mismatch",
				},
			}
			if cur, gerr := s.Store.GetNote(r.Context(), id, uid); gerr == nil {
				body["current_version"] = cur.Version
				body["current"] = noteOut(cur)
			}
			writeJSON(w, http.StatusConflict, body)
		default:
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	if err := s.Store.SoftDeleteNote(r.Context(), id, uid, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

func (s *Server) handleRestoreNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.RestoreNote(r.Context(), id, uid, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, noteOut(n))
}

// ─── Archive / Promote ──────────────────────────────────

func (s *Server) handleArchiveNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.ArchiveNote(r.Context(), id, uid, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

// handleUnarchiveNote —— 归档还原：archived_at 置 NULL，promoted_page_id
// 保留（页面已建，回链不丢）。返回 note。
func (s *Server) handleUnarchiveNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.UnarchiveNote(r.Context(), id, uid, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

type promoteNoteReq struct {
	ProjectID string `json:"project_id"`
}

// handlePromoteNote —— 转入知识库：在指定 wiki project 下建 page
// （标题=笔记标题，content_md 按空行拆段、每段一个 text block —— 同
// wiki ingest subscriber 的最朴素落地），成功后归档笔记并回链
// promoted_page_id。幂等：promoted_page_id 非空时直接回既有 page/note，
// 不重复建页。
func (s *Server) handlePromoteNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if s.Wiki == nil {
		writeErr(w, http.StatusServiceUnavailable, "promote_disabled", "wiki store not wired")
		return
	}
	var req promoteNoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	pid, err := uuid.Parse(req.ProjectID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	uid := mustUserID(r)

	n, err := s.Store.GetNote(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// 幂等重放：已 promote 直接回既有 page/note，不再建页。
	if n.PromotedPageID != nil {
		p, perr := s.Wiki.GetPage(r.Context(), *n.PromotedPageID)
		if perr != nil && !errors.Is(perr, wikistore.ErrNotFound) {
			writeErr(w, http.StatusInternalServerError, "internal", perr.Error())
			return
		}
		body := map[string]any{"note": noteOut(n), "idempotent_replay": true}
		if perr == nil {
			body["page"] = pageOut(p)
		} else {
			body["page"] = nil // 页面已被删除；回链保留
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	if n.ArchivedAt != nil {
		writeErr(w, http.StatusConflict, "note_archived", "archived note cannot be promoted")
		return
	}

	// wiki project 归属校验（同 wiki api ownsProject 的既有办法）。
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "project")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if proj.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}

	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = "未命名笔记"
	}
	page, err := s.Wiki.CreatePage(r.Context(), wikistore.CreatePageInput{
		ProjectID: pid,
		Title:     title,
		Frontmatter: map[string]any{
			"source":     "note",
			"note_id":    n.ID.String(),
			"source_url": n.SourceURL,
			"author":     n.Author,
		},
		ActorID: uid.String(),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 正文 → 每段一个 text block（best-effort：个别 block 失败只记日志，
	// page 已建、promote 继续完成，重放幂等兜底）。
	for i, para := range splitParagraphs(n.ContentMD) {
		if _, berr := s.Wiki.CreateBlock(r.Context(), wikistore.CreateBlockInput{
			PageID:    page.ID,
			ProjectID: pid,
			Position:  float64(i + 1),
			Type:      "text",
			Content:   map[string]any{"text": para},
			ActorID:   uid.String(),
		}); berr != nil && s.Logger != nil {
			s.Logger.Warn("note promote: create block failed",
				"note_id", n.ID, "page_id", page.ID, "idx", i, "err", berr)
		}
	}

	n, err = s.Store.MarkPromoted(r.Context(), id, uid, page.ID, uid.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": pageOut(page), "note": noteOut(n)})
}

// splitParagraphs —— 按空行拆段（同 wiki ingest subscriber 的朴素落地；
// N3 不做 markdown→block 结构转换）。
func splitParagraphs(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	parts := strings.Split(body, "\n\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pageOut —— wiki page 序列化（形状对齐 wiki api 的 pageOut）。
func pageOut(p *wikistore.Page) map[string]any {
	out := map[string]any{
		"id":          p.ID.String(),
		"project_id":  p.ProjectID.String(),
		"title":       p.Title,
		"frontmatter": p.Frontmatter,
		"share_mode":  p.ShareMode,
		"version":     p.Version,
		"created_at":  p.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":  p.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p.ParentID != nil {
		out["parent_id"] = p.ParentID.String()
	}
	return out
}

func (s *Server) handlePurgeNote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	if err := s.Store.PurgeNote(r.Context(), id, uid, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": id.String()})
}

// ─── Tags ───────────────────────────────────────────────

type createTagReq struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var req createTagReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "missing_name", "")
		return
	}
	uid := mustUserID(r)
	t, err := s.Store.CreateTag(r.Context(), uid, req.Name, uid.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagOut(t))
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	ts, err := s.Store.ListTags(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, tagOut(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}

type setNoteTagsReq struct {
	TagIDs []string `json:"tag_ids"`
}

func (s *Server) handleSetNoteTags(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req setNoteTagsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	tagIDs := make([]uuid.UUID, 0, len(req.TagIDs))
	for _, s := range req.TagIDs {
		u, err := uuid.Parse(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_tag_id", "")
			return
		}
		tagIDs = append(tagIDs, u)
	}
	uid := mustUserID(r)
	if err := s.Store.SetNoteTags(r.Context(), id, uid, tagIDs, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"note_id": id.String(), "tag_ids": req.TagIDs})
}

// ─── Revisions ──────────────────────────────────────────

// parseRevisionIDs —— 解析 {id} 与 {rid} 两个路径参数。
func parseRevisionIDs(w http.ResponseWriter, r *http.Request) (noteID, revisionID uuid.UUID, ok bool) {
	noteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return uuid.Nil, uuid.Nil, false
	}
	revisionID, err = uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_revision_id", "")
		return uuid.Nil, uuid.Nil, false
	}
	return noteID, revisionID, true
}

// handleListRevisions —— limit 默认 20、超过上限 100 静默收敛；
// 列表项不含 content_md（取内容走 GET …/revisions/{rid}）。
func (s *Server) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	revs, err := s.Store.ListRevisions(r.Context(), id, uid, limit, offset)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(revs))
	for _, rev := range revs {
		out = append(out, revisionOut(rev, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

func (s *Server) handleGetRevision(w http.ResponseWriter, r *http.Request) {
	id, rid, ok := parseRevisionIDs(w, r)
	if !ok {
		return
	}
	uid := mustUserID(r)
	rev, err := s.Store.GetRevision(r.Context(), id, rid, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, revisionOut(rev, true))
}

// handleRestoreRevision —— 覆盖式恢复：事务内先把当前状态存为
// change_type='restore' 的自动备份，再走正常更新路径（version+1、
// note.updated 事件）。回收站内笔记 404。
func (s *Server) handleRestoreRevision(w http.ResponseWriter, r *http.Request) {
	id, rid, ok := parseRevisionIDs(w, r)
	if !ok {
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.RestoreRevision(r.Context(), id, rid, uid, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

// handleSaveRevisionAsCopy —— 以该版本内容新建笔记（同 notebook、
// 复制标签关联、标题追加「（历史副本）」），返回新笔记。
func (s *Server) handleSaveRevisionAsCopy(w http.ResponseWriter, r *http.Request) {
	id, rid, ok := parseRevisionIDs(w, r)
	if !ok {
		return
	}
	uid := mustUserID(r)
	n, err := s.Store.SaveRevisionAsCopy(r.Context(), id, rid, uid, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("ETag", strconv.Itoa(n.Version))
	writeJSON(w, http.StatusOK, noteOut(n))
}

// ─── Changes (catchup) ──────────────────────────────────

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.Store.EventsSince(r.Context(), uid, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	latest, err := s.Store.LatestEventID(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		out = append(out, map[string]any{
			"id":         e.ID,
			"event_type": e.EventType,
			"actor_id":   e.ActorID,
			"payload":    e.Payload,
			"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "latest": latest})
}

// ─── helpers ────────────────────────────────────────────

// parseNotebookID —— 校验可选 notebook_id：必须存在、属于本人且未删。
func (s *Server) parseNotebookID(w http.ResponseWriter, r *http.Request, raw *string) (*uuid.UUID, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	u, err := uuid.Parse(*raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_notebook_id", "")
		return nil, false
	}
	uid := mustUserID(r)
	if _, alive, err := s.Store.GetNotebook(r.Context(), u, uid); err != nil || !alive {
		writeErr(w, http.StatusBadRequest, "bad_notebook_id", "notebook not found or deleted")
		return nil, false
	}
	return &u, true
}

func parseTimeOpt(w http.ResponseWriter, raw *string) (*time.Time, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_todo_completed_at", "")
		return nil, false
	}
	return &t, true
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "note api: request",
				"user_id", claims.UserID, "method", r.Method,
				"path", r.URL.Path, "query", r.URL.RawQuery)
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func notebookOut(nb *store.Notebook) map[string]any {
	out := map[string]any{
		"id":         nb.ID.String(),
		"name":       nb.Name,
		"position":   nb.Position,
		"created_at": nb.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": nb.UpdatedAt.UTC().Format(time.RFC3339),
	}
	// 同 noteOut 的 notebook_id 惯例：可空字段显式序列化为 null。
	if nb.ParentID != nil {
		out["parent_id"] = nb.ParentID.String()
	} else {
		out["parent_id"] = nil
	}
	return out
}

func noteOut(n *store.Note) map[string]any {
	out := map[string]any{
		"id":         n.ID.String(),
		"title":      n.Title,
		"content_md": n.ContentMD,
		"is_todo":    n.IsTodo,
		"position":   n.Position,
		"version":    n.Version,
		"created_at": n.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.NotebookID != nil {
		out["notebook_id"] = n.NotebookID.String()
	} else {
		out["notebook_id"] = nil
	}
	if n.TodoCompletedAt != nil {
		out["todo_completed_at"] = n.TodoCompletedAt.UTC().Format(time.RFC3339)
	}
	if n.DeletedAt != nil {
		out["deleted_at"] = n.DeletedAt.UTC().Format(time.RFC3339)
	}
	if n.SourceURL != nil {
		out["source_url"] = *n.SourceURL
	} else {
		out["source_url"] = nil
	}
	if n.Author != nil {
		out["author"] = *n.Author
	} else {
		out["author"] = nil
	}
	if n.ArchivedAt != nil {
		out["archived_at"] = n.ArchivedAt.UTC().Format(time.RFC3339)
	} else {
		out["archived_at"] = nil
	}
	if n.PromotedPageID != nil {
		out["promoted_page_id"] = n.PromotedPageID.String()
	} else {
		out["promoted_page_id"] = nil
	}
	return out
}

// searchHitOut —— 搜索结果序列化；null 字段风格对齐 noteOut
// （notebook_id 显式给 nil，todo_completed_at 缺席则省略）。
func searchHitOut(h *store.SearchHit) map[string]any {
	out := map[string]any{
		"id":         h.ID.String(),
		"title":      h.Title,
		"is_todo":    h.IsTodo,
		"updated_at": h.UpdatedAt.UTC().Format(time.RFC3339),
		"snippet":    h.Snippet,
		"rank":       h.Rank,
	}
	if h.NotebookID != nil {
		out["notebook_id"] = h.NotebookID.String()
	} else {
		out["notebook_id"] = nil
	}
	if h.TodoCompletedAt != nil {
		out["todo_completed_at"] = h.TodoCompletedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// revisionOut —— 版本序列化；full=true 时带完整 content_md
// （列表端点不传，控制响应体积）。
func revisionOut(r *store.Revision, full bool) map[string]any {
	out := map[string]any{
		"id":          r.ID.String(),
		"note_id":     r.NoteID.String(),
		"title":       r.Title,
		"change_type": r.ChangeType,
		"created_at":  r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.ChangeSummary != nil {
		out["change_summary"] = *r.ChangeSummary
	} else {
		out["change_summary"] = nil
	}
	if full {
		out["content_md"] = r.ContentMD
	}
	return out
}

func tagOut(t *store.Tag) map[string]any {
	return map[string]any{
		"id":         t.ID.String(),
		"name":       t.Name,
		"scope_key":  t.ScopeKey,
		"created_at": t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
