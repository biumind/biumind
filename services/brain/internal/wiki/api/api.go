// Package api implements Brain.Wiki HTTP endpoints.
//
//	POST   /v1/wiki/projects                               create project
//	GET    /v1/wiki/projects                               list mine
//	POST   /v1/wiki/projects/{pid}/pages                   create page
//	GET    /v1/wiki/projects/{pid}/pages                   list pages
//	GET    /v1/wiki/projects/{pid}/pages/{id}              get page
//	PUT    /v1/wiki/projects/{pid}/pages/{id}              update (If-Match)
//	DELETE /v1/wiki/projects/{pid}/pages/{id}              soft delete
//	GET    /v1/wiki/projects/{pid}/pages/{id}/blocks       list blocks
//	POST   /v1/wiki/projects/{pid}/pages/{id}/blocks       create block
//	PUT    /v1/wiki/projects/{pid}/blocks/{id}             update block (If-Match)
//	DELETE /v1/wiki/projects/{pid}/blocks/{id}             soft delete
//	GET    /v1/wiki/projects/{pid}/changes?since={id}      catchup events
//	POST   /v1/wiki/projects/{pid}/sources/clip            webclip ingest
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/wiki/enrich"
	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
	"github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/templates"
	"github.com/google/uuid"
)

type Server struct {
	Store    *store.Store
	Sources  *sources.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
	// Relay (S3 P0-1) is the model-relay sender used by the wiki
	// autonomous-maintenance agent loop. nil when MODEL_RELAY_URL is unset
	// (handler returns 503). Wired by main.go via WithRelay after the chat
	// HTTPSender is built.
	Relay *chat.HTTPSender
	// Selection (S3 P1-6) is the model-relay caller for inline selection
	// edit/ask on a page body. nil when MODEL_RELAY_URL is unset (handler
	// returns 503). Wired by main.go via WithSelection.
	Selection *enrich.RelayLLMCaller
	// BM25 (S3 P1-6 phase B) powers Ask-mode KB retrieval (top5 same-project
	// pages) so the model can cite `[1][2]`. nil ⇒ Ask degrades to no-KB.
	BM25 *bm25.Searcher
	// AgentRuns (S3 P0-1 ⑤) tracks in-flight wiki agent loops so the
	// POST .../agent/run/cancel endpoint can stop one by run id. key = runID
	// (string), value = context.CancelFunc. sync.Map zero-value is ready;
	// entries are removed by the run's defer and by cancel's LoadAndDelete.
	AgentRuns sync.Map
	// Semantic (S3 P1) is triggered automatically after a successful wiki
	// agent run so contradiction / lint findings land server-side without
	// relying on the client to POST /reviews/scan. nil ⇒ skip (semantic
	// lint disabled). Wired by main.go via WithSemantic.
	Semantic SemanticScanner
}

// SemanticScanner is the subset of reviews.SemanticRunner the agent-run
// hook needs. Declared as an interface (not the concrete runner) so the
// api package doesn't import reviews and tests can stub it.
type SemanticScanner interface {
	Run(ctx context.Context, projectID, ownerID uuid.UUID) error
}

func NewServer(s *store.Store, src *sources.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Sources: src, Verifier: v, Logger: l}
}

// WithRelay wires the model-relay sender that powers the wiki agent loop
// (POST /v1/wiki/projects/{pid}/agent/run). sender may be nil — the
// handler degrades to 503 in that case. Returns s for chaining.
func (s *Server) WithRelay(sender *chat.HTTPSender) *Server {
	s.Relay = sender
	return s
}

// WithSelection wires the model-relay caller that powers inline selection
// edit/ask (POST /v1/wiki/projects/{pid}/pages/{id}/selection-edit).
// caller may be nil — the handler degrades to 503 in that case.
func (s *Server) WithSelection(caller *enrich.RelayLLMCaller) *Server {
	s.Selection = caller
	return s
}

// WithBM25 wires the BM25 searcher for Ask-mode KB retrieval. searcher may
// be nil — Ask then runs without KB citations.
func (s *Server) WithBM25(searcher *bm25.Searcher) *Server {
	s.BM25 = searcher
	return s
}

// WithSemantic wires the semantic lint runner that fires automatically
// after a successful wiki agent run (POST .../agent/run). scanner may be
// nil — the post-run hook then no-ops. Returns s for chaining.
func (s *Server) WithSemantic(sc SemanticScanner) *Server {
	s.Semantic = sc
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/wiki/projects", s.requireAuth(s.handleCreateProject))
	mux.HandleFunc("GET /v1/wiki/projects", s.requireAuth(s.handleListProjects))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages", s.requireAuth(s.handleCreatePage))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages", s.requireAuth(s.handleListPages))
	// by-path 必须在 {id} 前注册，但 Go 1.22 ServeMux 按 specificity 自动
	// 选最匹配的，所以顺序不严格关键 — 这里仍按可读性排在 list 之后。
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/by-path", s.requireAuth(s.handlePageByPath))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}", s.requireAuth(s.handleGetPage))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/enrich", s.requireAuth(s.handleEnrichPage))
	// S3 P1-6 inline selection edit/ask（选中文字 → LLM 改/答）。
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/selection-edit",
		s.requireAuth(s.handleSelectionEdit))
	mux.HandleFunc("PUT /v1/wiki/projects/{pid}/pages/{id}", s.requireAuth(s.handleUpdatePage))
	// §⑤ Milkdown 整篇 body_md 写（Path C 权威入口；事务内 mdparse 重算 blocks 投影）。
	mux.HandleFunc("PUT /v1/wiki/projects/{pid}/pages/{id}/body", s.requireAuth(s.handleUpdatePageBody))
	// S3 P0-1 自主迭代维护 agent loop（wiki 写工具集，SSE 流式）。
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/agent/run", s.requireAuth(s.handleWikiAgentRun))
	// S3 P0-1 ⑤ 显式取消某次 agent run（hubCtx detach，client 断开不停，
	// 必须经此 endpoint 按 run_id 调 cancel 才停）。
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/agent/run/cancel",
		s.requireAuth(s.handleWikiAgentCancel))
	// §1.2 P2 run 持久化历史（审计「可回看历史」）：列表 + 详情（改动页清单）。
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/agent/runs",
		s.requireAuth(s.handleListAgentRuns))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/agent/runs/{runId}",
		s.requireAuth(s.handleGetAgentRun))
	mux.HandleFunc("DELETE /v1/wiki/projects/{pid}/pages/{id}", s.requireAuth(s.handleDeletePage))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}/blocks", s.requireAuth(s.handleListBlocks))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/blocks", s.requireAuth(s.handleCreateBlock))
	mux.HandleFunc("PUT /v1/wiki/projects/{pid}/blocks/{id}", s.requireAuth(s.handleUpdateBlock))
	mux.HandleFunc("DELETE /v1/wiki/projects/{pid}/blocks/{id}", s.requireAuth(s.handleDeleteBlock))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/changes", s.requireAuth(s.handleChanges))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/sources/clip", s.requireAuth(s.handleClip))
	// Wikilink reverse-lookup — "who links to this page?"
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}/backlinks",
		s.requireAuth(s.handleListBacklinks))
	// Per-page event timeline (page.created / block.updated / ...).
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}/changelog",
		s.requireAuth(s.handleListChangelog))
	// Per-page content snapshots — restore / save-as-copy（迁移 00065）。
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}/revisions",
		s.requireAuth(s.handleListPageRevisions))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}",
		s.requireAuth(s.handleGetPageRevision))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}/restore",
		s.requireAuth(s.handleRestorePageRevision))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}/save-as-copy",
		s.requireAuth(s.handleSavePageRevisionAsCopy))
}

// ─── Projects ───────────────────────────────────────────

type createProjectReq struct {
	Name       string `json:"name"`
	TemplateID string `json:"template_id,omitempty"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "missing_name", "")
		return
	}
	// Lookup is nil for "" / "general" / unknown ids → seed nothing. We
	// stay lenient (no 400 on unknown template) so a stale client passing
	// a retired id still gets its project created empty rather than
	// failing the whole create.
	var seed []templates.SeedPage
	if tpl := templates.Lookup(req.TemplateID); tpl != nil {
		seed = tpl.SeedPages
	}
	uid := mustUserID(r)
	p, err := s.Store.CreateProjectWithTemplate(r.Context(), uid, req.Name, req.TemplateID, seed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectOut(p))
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	ps, err := s.Store.ListProjects(r.Context(), uid, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectOut(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

// ─── Pages ──────────────────────────────────────────────

type createPageReq struct {
	Title    string  `json:"title"`
	ParentID *string `json:"parent_id"`
}

func (s *Server) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	var req createPageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := mustUserID(r)
	if !s.ownsProject(w, r, pid) {
		return
	}
	var parent *uuid.UUID
	if req.ParentID != nil && *req.ParentID != "" {
		if u, err := uuid.Parse(*req.ParentID); err == nil {
			parent = &u
		}
	}
	p, err := s.Store.CreatePage(r.Context(), store.CreatePageInput{
		ProjectID: pid, ParentID: parent, Title: req.Title, ActorID: uid.String(),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pageOut(p))
}

func (s *Server) handleListPages(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	ps, err := s.Store.ListPages(r.Context(), pid, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "wiki api: list pages",
			"project_id", pid, "count", len(ps))
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, pageOut(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": out})
}

func (s *Server) handleGetPage(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	p, err := s.Store.GetPage(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if p.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	w.Header().Set("ETag", strconv.Itoa(p.Version))
	writeJSON(w, http.StatusOK, pageOut(p))
}

type updatePageReq struct {
	Title       *string        `json:"title"`
	Frontmatter map[string]any `json:"frontmatter"`
}

func (s *Server) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req updatePageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ifMatch, _ := strconv.Atoi(r.Header.Get("If-Match"))
	uid := mustUserID(r)
	p, err := s.Store.UpdatePage(r.Context(), store.UpdatePageInput{
		PageID: id, IfMatchVersion: ifMatch, Title: req.Title, Frontmatter: req.Frontmatter,
		ActorID: uid.String(),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "")
		case errors.Is(err, store.ErrConflict):
			cur, _ := s.Store.GetPage(r.Context(), id)
			body := map[string]any{
				"error": map[string]any{
					"code": "version_conflict", "message": "If-Match version mismatch",
				},
			}
			if cur != nil {
				body["server_version"] = cur.Version
				body["server_payload"] = pageOut(cur)
			}
			writeJSON(w, http.StatusConflict, body)
		default:
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.Header().Set("ETag", strconv.Itoa(p.Version))
	writeJSON(w, http.StatusOK, pageOut(p))
}

func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)
	if err := s.Store.SoftDeletePage(r.Context(), id, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// ─── Blocks ─────────────────────────────────────────────

type createBlockReq struct {
	Position float64        `json:"position"`
	Type     string         `json:"type"`
	Content  map[string]any `json:"content"`
}

func (s *Server) handleCreateBlock(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req createBlockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Type == "" {
		req.Type = "text"
	}
	uid := mustUserID(r)
	b, err := s.Store.CreateBlock(r.Context(), store.CreateBlockInput{
		PageID: pageID, ProjectID: pid, Position: req.Position, Type: req.Type,
		Content: req.Content, ActorID: uid.String(),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, blockOut(b))
}

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	bs, err := s.Store.ListBlocks(r.Context(), pageID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		out = append(out, blockOut(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": out})
}

type updateBlockReq struct {
	Content  map[string]any `json:"content"`
	Position *float64       `json:"position"`
}

func (s *Server) handleUpdateBlock(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req updateBlockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ifMatch, _ := strconv.Atoi(r.Header.Get("If-Match"))
	uid := mustUserID(r)
	b, err := s.Store.UpdateBlock(r.Context(), store.UpdateBlockInput{
		BlockID: id, IfMatchVersion: ifMatch, Content: req.Content, Position: req.Position,
		ActorID: uid.String(),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "")
		case errors.Is(err, store.ErrConflict):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{"code": "version_conflict", "message": "If-Match mismatch"},
			})
		default:
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.Header().Set("ETag", strconv.Itoa(b.Version))
	writeJSON(w, http.StatusOK, blockOut(b))
}

func (s *Server) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)
	if err := s.Store.SoftDeleteBlock(r.Context(), id, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// ─── Changes (catchup) ──────────────────────────────────

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit := 200
	if l, _ := strconv.Atoi(r.URL.Query().Get("limit")); l > 0 && l <= 1000 {
		limit = l
	}
	scope := "wiki:project:" + pid.String()
	events, err := s.Store.EventsSince(r.Context(), scope, since, limit)
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
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "scope": scope})
}

// ─── Clip ───────────────────────────────────────────────

type clipReq struct {
	URL      string         `json:"url"`
	Title    string         `json:"title"`
	Content  string         `json:"content_md"`
	Tags     []string       `json:"tags"`
	Metadata map[string]any `json:"metadata"`
}

func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	var req clipReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Content == "" {
		writeErr(w, http.StatusBadRequest, "missing_content", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)

	hash := sha256.Sum256([]byte(req.URL + "|" + req.Content))

	md := req.Metadata
	if md == nil {
		md = map[string]any{}
	}
	if len(req.Tags) > 0 {
		md["tags"] = req.Tags
	}

	src, dup, err := s.Sources.CreateWebclip(r.Context(), sources.CreateWebclipInput{
		ProjectID: pid, UserID: uid,
		URL: req.URL, Title: req.Title, Raw: req.Content,
		Metadata: md, ContentHash: hash[:],
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "wiki api: clip",
			"project_id", pid, "user_id", uid, "url", req.URL,
			"content_bytes", len(req.Content), "duplicate", dup,
			"source_id", src.ID)
	}
	status := http.StatusCreated
	if dup {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"source_id": src.ID.String(),
		"duplicate": dup,
		"status":    src.ParseStatus,
	})
}

// ─── helpers ────────────────────────────────────────────

// ownsProject is a helper called inline by handlers; on failure it writes the
// HTTP error and returns false so the handler can simply `return`.
func (s *Server) ownsProject(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := mustUserID(r)
	p, err := s.Store.GetProject(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return false
	}
	if p.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return false
	}
	return true
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
		// 单点记录全 wiki 路由的入口 — 14 个 handler 共享,debug 模式下能
		// 一行看到 user_id + path + query (page/limit 等),不污染各 handler。
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "wiki api: request",
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

func projectOut(p *store.Project) map[string]any {
	out := map[string]any{
		"id":         p.ID.String(),
		"name":       p.Name,
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339),
	}
	if p.TemplateID != "" {
		out["template_id"] = p.TemplateID
	}
	return out
}

// handleUpdatePageBody —— PUT /pages/{id}/body，Milkdown 整篇 body_md 写（§⑤ Path C 权威入口）。
// If-Match OCC；409 返 server_payload；200 返新 page（pageOut 含 body_md）。store 内事务
// 快照旧态 + UPDATE body_md + reconcileBlocksTx 重算 blocks 投影 + emit page.updated。
func (s *Server) handleUpdatePageBody(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req updatePageBodyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ifMatch, _ := strconv.Atoi(r.Header.Get("If-Match"))
	uid := mustUserID(r)
	p, err := s.Store.UpdatePageBody(r.Context(), store.UpdatePageBodyInput{
		PageID: id, BodyMd: req.BodyMd, IfMatchVersion: ifMatch, ActorID: uid.String(),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "")
		case errors.Is(err, store.ErrConflict):
			cur, _ := s.Store.GetPage(r.Context(), id)
			body := map[string]any{
				"error": map[string]any{
					"code": "version_conflict", "message": "If-Match version mismatch",
				},
			}
			if cur != nil {
				body["server_version"] = cur.Version
				body["server_payload"] = pageOut(cur)
			}
			writeJSON(w, http.StatusConflict, body)
		default:
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.Header().Set("ETag", strconv.Itoa(p.Version))
	writeJSON(w, http.StatusOK, pageOut(p))
}

type updatePageBodyReq struct {
	BodyMd string `json:"body_md"`
}

func pageOut(p *store.Page) map[string]any {
	out := map[string]any{
		"id":          p.ID.String(),
		"project_id":  p.ProjectID.String(),
		"title":       p.Title,
		"frontmatter": p.Frontmatter,
		"body_md":     p.BodyMd,
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

func blockOut(b *store.Block) map[string]any {
	out := map[string]any{
		"id":         b.ID.String(),
		"page_id":    b.PageID.String(),
		"position":   b.Position,
		"type":       b.Type,
		"content":    b.Content,
		"version":    b.Version,
		"created_at": b.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": b.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if b.ParentID != nil {
		out["parent_id"] = b.ParentID.String()
	}
	return out
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

// handlePageByPath —— 按 frontmatter `path` 或 slug 查找页面 stub。
//
//	GET /v1/wiki/projects/{pid}/pages/by-path?path=concepts/agent-architecture
//
// B1 完整实现（pages 模块迁移时）会接 store.GetPageByPath；当前 stub
// 校验 ownership 后返回 501，让前端 wikilink 深链能命中路由层。
func (s *Server) handlePageByPath(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	uid := mustUserID(r)
	proj, err := s.Store.GetProject(r.Context(), pid)
	if err != nil || proj.OwnerID != uid {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": map[string]any{
			"code":     "not_implemented",
			"message":  "by-path lookup awaiting B1 migration",
			"endpoint": "wiki.pages.by_path",
		},
	})
}

// handleEnrichPage —— 手动触发单页 enrich（重跑 LLM wikilink 抽取）。
//
//	POST /v1/wiki/projects/{pid}/pages/{id}/enrich
//
// 富化 worker 是自愈轮询：扫 enriched_at IS NULL OR enriched_at <
// updated_at 的页。所以"入队" = 把该页 enriched_at 置 NULL，worker 下个
// tick（默认 30s，受 ENRICH_INTERVAL_SEC 控制）捞起。worker 未启用时
// 标记仍落库，启用后即处理。
func (s *Server) handleEnrichPage(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	uid := mustUserID(r)
	proj, err := s.Store.GetProject(r.Context(), pid)
	if err != nil || proj.OwnerID != uid {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return
	}
	// Verify the page exists + belongs to this project before marking
	// stale, so we don't silently accept a bogus page id.
	pg, err := s.Store.GetPage(r.Context(), pageID)
	if err != nil || pg.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "page")
		return
	}
	if err := s.Store.MarkEnrichStale(r.Context(), pageID, pid); err != nil {
		writeErr(w, http.StatusInternalServerError, "enrich_queue", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"page_id": pageID.String(),
		"queued":  true,
	})
}
