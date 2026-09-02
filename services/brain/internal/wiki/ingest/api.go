// HTTP surface for ingest tasks.
//
//	POST  /v1/wiki/projects/{pid}/ingest                       create + publish
//	GET   /v1/wiki/projects/{pid}/ingest/tasks                 list (newest first)
//	GET   /v1/wiki/projects/{pid}/ingest/tasks/{tid}           detail
//	GET   /v1/wiki/projects/{pid}/ingest/tasks/{tid}/events    SSE 历史重放
//	POST  /v1/wiki/projects/{pid}/ingest/tasks/{tid}/cancel    request cancel + broadcast
//	POST  /v1/wiki/projects/{pid}/ingest/tasks/{tid}/retry     failed/cancelled → pending + republish
//
// 旧 /v1/wiki/ingest/{taskId} 形态已废弃（B0.5 决策），客户端必须带 project
// 上下文调用。
//
// SSE 端点（events）用于客户端重连后从 events_outbox 拉取该 task 的历史
// 事件序列；当前 stub 直接关闭流，B2 完整实现。
//
// 所有非创建端点都重新校验 ownership —— 仅靠 task id 查询会让 guess-the-uuid
// 攻击者窥视他人任务。
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// Server wires the ingest API. Publisher is required — without it,
// creating a task would land a row no one ever picks up. The handler
// rejects the create POST when Publisher is nil so failures surface at
// configuration time, not as silently-stuck tasks.
type Server struct {
	Store     *Store
	Wiki      *wikistore.Store
	Publisher publisher.Publisher
	Verifier  *bauth.Verifier
	Logger    *slog.Logger
}

func NewServer(s *Store, w *wikistore.Store, p publisher.Publisher, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Wiki: w, Publisher: p, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/ingest", s.requireAuth(s.handleCreate))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/ingest/tasks", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/ingest/tasks/{tid}", s.requireAuth(s.handleGet))
	mux.HandleFunc("PATCH /v1/wiki/projects/{pid}/ingest/tasks/{tid}", s.requireAuth(s.handlePatch))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/ingest/tasks/{tid}/events", s.requireAuth(s.handleEvents))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/ingest/tasks/{tid}/cancel", s.requireAuth(s.handleCancel))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/ingest/tasks/{tid}/retry", s.requireAuth(s.handleRetry))
}

// ─── Wire payloads ──────────────────────────────────────────────

type createReq struct {
	SourceID  *string `json:"source_id"`
	RawText   string  `json:"raw_text"`
	Title     string  `json:"title"`
	Processor string  `json:"processor,omitempty"` // 默认 server；"client" = 客户端镜像（W2，不 publish）
}

// patchReq 是客户端镜像任务的状态推进请求（W2）。status 与 progress
// 至少给一个；status ∈ running/done/failed/cancelled；error 仅 failed
// 时有意义。仅 processor=client 的任务接受 PATCH（服务端任务由 worker
// 经 NATS 推进，409）。
type patchReq struct {
	Status   string         `json:"status,omitempty"`
	Progress map[string]any `json:"progress,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// taskOut is the public projection. We hide owner_id (already implied
// by JWT) but expose project_id since callers index tasks per project.
type taskOut struct {
	ID                string         `json:"id"`
	ProjectID         string         `json:"project_id"`
	SourceID          *string        `json:"source_id,omitempty"`
	Title             string         `json:"title"`
	Status            string         `json:"status"`
	Error             string         `json:"error,omitempty"`
	Progress          map[string]any `json:"progress,omitempty"`
	ResultPages       []string       `json:"result_pages"`
	Processor         string         `json:"processor"`
	CancelRequestedAt *string        `json:"cancel_requested_at,omitempty"`
	StartedAt         *string        `json:"started_at,omitempty"`
	FinishedAt        *string        `json:"finished_at,omitempty"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

func taskJSON(t *Task) taskOut {
	out := taskOut{
		ID: t.ID.String(), ProjectID: t.ProjectID.String(),
		Title: t.Title, Status: t.Status, Error: t.Error,
		Progress:    t.Progress,
		ResultPages: make([]string, 0, len(t.ResultPages)),
		Processor:   t.Processor,
		CreatedAt:   t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.SourceID != nil {
		v := t.SourceID.String()
		out.SourceID = &v
	}
	for _, p := range t.ResultPages {
		out.ResultPages = append(out.ResultPages, p.String())
	}
	if t.CancelRequestedAt != nil {
		v := t.CancelRequestedAt.UTC().Format(time.RFC3339)
		out.CancelRequestedAt = &v
	}
	if t.StartedAt != nil {
		v := t.StartedAt.UTC().Format(time.RFC3339)
		out.StartedAt = &v
	}
	if t.FinishedAt != nil {
		v := t.FinishedAt.UTC().Format(time.RFC3339)
		out.FinishedAt = &v
	}
	return out
}

// ─── Handlers ───────────────────────────────────────────────────

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	uid, ok := s.requireProjectOwner(w, r, pid)
	if !ok {
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	processor := req.Processor
	if processor == "" {
		processor = "server"
	}
	if processor != "server" && processor != "client" {
		writeErr(w, http.StatusBadRequest, "bad_processor",
			"processor must be server or client")
		return
	}
	// 服务端任务要 publish 给 wiki-llm，没有 bus 直接 503；客户端镜像
	// 任务由客户端自己推进，不需要 publisher。
	if processor == "server" && s.Publisher == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_publisher",
			"ingest worker bus not configured on this brain")
		return
	}
	var sid *uuid.UUID
	if req.SourceID != nil && *req.SourceID != "" {
		v, err := uuid.Parse(*req.SourceID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_source_id", err.Error())
			return
		}
		sid = &v
	}
	if sid == nil && req.RawText == "" {
		writeErr(w, http.StatusBadRequest, "missing_input",
			"provide source_id or raw_text")
		return
	}
	t, err := s.Store.Create(r.Context(), CreateInput{
		ProjectID: pid, OwnerID: uid, SourceID: sid,
		RawText: req.RawText, Title: req.Title, Processor: processor,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 客户端镜像任务由客户端自己推进（PATCH），不 publish —— 否则
	// wiki-llm 会接走同一个任务双跑。
	if processor == "client" {
		s.Logger.DebugContext(r.Context(), "wiki ingest: client task created",
			"task_id", t.ID, "project_id", pid, "user_id", uid,
			"source_id", sid, "title", t.Title)
		writeJSON(w, http.StatusCreated, taskJSON(t))
		return
	}
	s.publishTask(r, t)
	s.Logger.DebugContext(r.Context(), "wiki ingest: task created",
		"task_id", t.ID, "project_id", pid, "user_id", uid,
		"source_id", sid, "raw_bytes", len(req.RawText), "title", t.Title)
	writeJSON(w, http.StatusCreated, taskJSON(t))
}

// publishTask 把任务按创建时的 payload 形态发给 wiki-llm（两段式 subject，
// 对齐 handleCreate 的修复）。失败不回滚行 —— reaper 会重发卡 pending
// 的任务。供 handleCreate / handleRetry 共用。
func (s *Server) publishTask(r *http.Request, t *Task) {
	payload := map[string]any{
		"task_id":    t.ID.String(),
		"project_id": t.ProjectID.String(),
		"owner_id":   t.OwnerID.String(),
		"title":      t.Title,
		"raw_text":   t.RawText,
	}
	if t.SourceID != nil {
		payload["source_id"] = t.SourceID.String()
	}
	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// topic/kind 两段式：subject 拼成 biumind.<env>.brain.wiki.ingest.requested，
	// 与 wiki-llm 的订阅地址一致（重复段 bug 已修，勿再 topic=kind 同传）。
	if err := s.Publisher.Publish(pubCtx,
		"wiki.ingest", "requested", payload); err != nil {
		s.Logger.Warn("wiki ingest publish failed",
			"task_id", t.ID, "err", err)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if _, ok := s.requireProjectOwner(w, r, pid); !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			limit = n
		}
	}
	tasks, err := s.Store.ListByProject(r.Context(), pid, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]taskOut, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadOwnedTask(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(t))
}

// handlePatch 推进客户端镜像任务（W2）。仅 processor=client 的任务可
// PATCH —— 服务端任务由 wiki-llm 经 NATS 推进，允许客户端改会与 worker
// 写入打架。每次成功 PATCH 刷新 updated_at，即 reaper 的惰性心跳。
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadOwnedTask(w, r)
	if !ok {
		return
	}
	if t.Processor != "client" {
		writeErr(w, http.StatusConflict, "not_client_task",
			"only processor=client tasks accept PATCH")
		return
	}
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Status == "" && req.Progress == nil {
		writeErr(w, http.StatusBadRequest, "empty_patch",
			"provide status and/or progress")
		return
	}
	if req.Progress != nil {
		if err := s.Store.UpdateProgress(r.Context(), t.ID, req.Progress); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusConflict, "already_terminal", t.Status)
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	switch req.Status {
	case "":
		// 纯 progress 心跳
	case StatusRunning:
		if err := s.Store.MarkRunning(r.Context(), t.ID); err != nil {
			writeErr(w, statusErrCode(err), "already_terminal", err.Error())
			return
		}
	case StatusDone, StatusFailed, StatusCancelled:
		if err := s.Store.MarkTerminal(r.Context(), t.ID, req.Status, req.Error); err != nil {
			writeErr(w, statusErrCode(err), "already_terminal", err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "bad_status",
			"status must be running/done/failed/cancelled")
		return
	}
	updated, err := s.Store.Get(r.Context(), t.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(updated))
}

// statusErrCode maps store errors from PATCH transitions to HTTP status.
func statusErrCode(err error) int {
	if errors.Is(err, ErrNotFound) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadOwnedTask(w, r)
	if !ok {
		return
	}
	if err := s.Store.RequestCancel(r.Context(), t.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Task became terminal between Get and Cancel — race we
			// treat as user-visible 409: their cancel request didn't
			// take because the work is already finished.
			writeErr(w, http.StatusConflict, "already_terminal", t.Status)
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 广播取消信号：wiki-llm 所有实例订阅 cancel subject（非 queue
	// group），在任务拾取 / 流式 chunk 边界处检查并中止。fire-and-forget
	// 的洞（广播时无 worker 在线）由 reaper 的取消清扫兜底。
	if s.Publisher != nil {
		pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Publisher.Publish(pubCtx,
			"wiki.ingest", "cancel",
			map[string]any{"task_id": t.ID.String()}); err != nil {
			s.Logger.Warn("wiki ingest cancel broadcast failed",
				"task_id", t.ID, "err", err)
		}
	}
	writeJSON(w, http.StatusAccepted,
		map[string]any{"id": t.ID.String(), "cancel_requested": true})
}

// handleRetry 把 failed/cancelled 的服务端任务重置回 pending 并重发。
// 客户端镜像任务（processor=client）由客户端自己的队列重试，走 PATCH
// 而非本端点；done 任务无重试语义。
func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadOwnedTask(w, r)
	if !ok {
		return
	}
	if t.Processor == "client" {
		writeErr(w, http.StatusConflict, "client_task",
			"client-mirror tasks are retried from the client queue, not here")
		return
	}
	if t.Status != StatusFailed && t.Status != StatusCancelled {
		writeErr(w, http.StatusConflict, "not_retryable",
			"only failed/cancelled tasks can be retried")
		return
	}
	if s.Publisher == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_publisher",
			"ingest worker bus not configured on this brain")
		return
	}
	rt, err := s.Store.Retry(r.Context(), t.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusConflict, "not_retryable", t.Status)
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.publishTask(r, rt)
	s.Logger.DebugContext(r.Context(), "wiki ingest: task retried",
		"task_id", rt.ID, "project_id", rt.ProjectID)
	writeJSON(w, http.StatusAccepted, taskJSON(rt))
}

// ─── Auth helpers ──────────────────────────────────────────────

// requireProjectOwner verifies the JWT user owns the project. Returns
// owner uuid and ok=true on success; writes the appropriate error
// response and ok=false otherwise.
func (s *Server) requireProjectOwner(w http.ResponseWriter, r *http.Request, pid uuid.UUID) (uuid.UUID, bool) {
	claims := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return uuid.Nil, false
	}
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return uuid.Nil, false
	}
	if proj.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return uuid.Nil, false
	}
	return uid, true
}

// loadOwnedTask handles the {tid} path param: parse, fetch, ownership
// check, and verify the task's project_id matches the URL {pid} (so a
// task uuid from project A can't be queried via project B's URL).
func (s *Server) loadOwnedTask(w http.ResponseWriter, r *http.Request) (*Task, bool) {
	tid, err := uuid.Parse(r.PathValue("tid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_task_id", "")
		return nil, false
	}
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return nil, false
	}
	t, err := s.Store.Get(r.Context(), tid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "task")
			return nil, false
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return nil, false
	}
	claims := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil || uid != t.OwnerID || t.ProjectID != pid {
		// Indistinguishable 404 — don't leak existence to non-owners or
		// across-project guess attempts.
		writeErr(w, http.StatusNotFound, "not_found", "task")
		return nil, false
	}
	return t, true
}

// handleEvents 是 SSE 历史重放 stub。完整实现（B2）从 events_outbox
// 表读 (task_id=tid) 的所有事件并按顺序回写为 `event:`+`data:` SSE 帧。
// 当前 stub 校验 ownership 后直接发一帧 placeholder 并关闭。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadOwnedTask(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	_, _ = fmt.Fprintf(w, "event: stub\ndata: {\"task_id\":%q,\"status\":%q}\n\n",
		t.ID.String(), t.Status)
	if flusher != nil {
		flusher.Flush()
	}
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
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

// ─── Helpers ───────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
