// HTTP layer for chat threads.
//
//	GET    /v1/threads                          list (cursor pagination; updated_after=RFC3339 for incremental sync)
//	POST   /v1/threads                          create
//	GET    /v1/threads/{id}                     get one
//	PATCH  /v1/threads/{id}                     rename / pin / archive / model
//	DELETE /v1/threads/{id}                     hard delete (CASCADE messages)
//
//	GET    /v1/threads/{id}/messages            list (position-cursor)
//	POST   /v1/threads/{id}/messages            append (direct mode + tool turns)
//	PATCH  /v1/threads/{id}/messages/{mid}      edit / status / tokens
//	DELETE /v1/threads/{id}/messages/{mid}      delete one
//
//	POST   /v1/threads/{id}/send                cloud-mode streaming send
//	POST   /v1/threads/{id}/messages/{mid}/cancel  pause stream
//
// All endpoints require a Bearer JWT and are owner-scoped (mismatched
// user → 404 not 403, to avoid leaking thread existence).

package chat

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
	// Sender handles the streaming /send endpoint. Optional — when nil,
	// /send returns 503 (cloud mode disabled, e.g. no model-relay configured).
	Sender Sender
}

// Sender abstracts the cloud-mode streaming pipeline (Brain → model-relay → LLM
// → persisted assistant message). See send.go for the production impl.
type Sender interface {
	HandleSend(w http.ResponseWriter, r *http.Request, threadID, userID uuid.UUID)
	HandleCancel(w http.ResponseWriter, r *http.Request, msgID, userID uuid.UUID)
	HandleRegenerate(w http.ResponseWriter, r *http.Request,
		threadID, userMsgID, userID uuid.UUID)
}

func NewServer(store *Store, v *bauth.Verifier, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{Store: store, Verifier: v, Logger: logger}
}

// WithSender wires the streaming pipeline. Returns the same server.
func (s *Server) WithSender(snd Sender) *Server {
	s.Sender = snd
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	// Threads
	mux.HandleFunc("GET    /v1/threads", s.requireAuth(s.handleListThreads))
	mux.HandleFunc("POST   /v1/threads", s.requireAuth(s.handleCreateThread))
	mux.HandleFunc("GET    /v1/threads/{id}", s.requireAuth(s.handleGetThread))
	mux.HandleFunc("PATCH  /v1/threads/{id}", s.requireAuth(s.handleUpdateThread))
	mux.HandleFunc("DELETE /v1/threads/{id}", s.requireAuth(s.handleDeleteThread))

	// Messages
	mux.HandleFunc("GET    /v1/threads/{id}/messages", s.requireAuth(s.handleListMessages))
	mux.HandleFunc("POST   /v1/threads/{id}/messages", s.requireAuth(s.handleCreateMessage))
	mux.HandleFunc("PATCH  /v1/threads/{id}/messages/{mid}", s.requireAuth(s.handleUpdateMessage))
	mux.HandleFunc("DELETE /v1/threads/{id}/messages/{mid}", s.requireAuth(s.handleDeleteMessage))

	// Streaming
	mux.HandleFunc("POST /v1/threads/{id}/send", s.requireAuth(s.handleSend))
	mux.HandleFunc("POST /v1/threads/{id}/messages/{mid}/cancel",
		s.requireAuth(s.handleCancel))
	mux.HandleFunc("POST /v1/threads/{id}/messages/{mid}/regenerate",
		s.requireAuth(s.handleRegenerate))

	// Search (cross-thread or scoped). 设计文档:
	// docs/BiuMind-Chat-Search-Design.md
	mux.HandleFunc("POST /v1/chat/search", s.requireAuth(s.handleSearch))

	// Account-level statistics (数据统计 overview page). Read-only.
	mux.HandleFunc("GET /v1/chat/stats", s.requireAuth(s.handleStats))
}

// ─── Stats ───────────────────────────────────────────

// handleStats serves the account-level chat statistics overview. Token /
// spend metrics live in model-relay's /v1/me/usage; this is chat structure:
// threads / messages / models / activity heatmap / ranks.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	heatmapSince := now.AddDate(-1, 0, 0)

	res, err := s.Store.Stats(r.Context(), uid, monthStart, heatmapSince)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stats_failed", err.Error())
		return
	}

	active, cur, max := streakStats(res.Heatmap, now)
	writeJSON(w, http.StatusOK, map[string]any{
		"overview": map[string]any{
			"threads":  res.Threads,
			"messages": res.Messages,
			"models":   res.Models,
		},
		"heatmap":        res.Heatmap,
		"model_rank":     res.ModelRank,
		"topic_rank":     res.TopicRank,
		"active_days":    active,
		"current_streak": cur,
		"max_streak":     max,
	})
}

// streakStats derives active-day count, current streak, and longest streak
// from the (ascending, count>0) heatmap buckets. Days are UTC to match the
// heatmap query. Current streak anchors on today, or yesterday if today has
// no activity yet (so an idle morning doesn't zero a live streak).
func streakStats(heatmap []HeatmapDay, now time.Time) (active, current, max int) {
	active = len(heatmap)
	if active == 0 {
		return 0, 0, 0
	}
	set := make(map[string]bool, len(heatmap))
	for _, h := range heatmap {
		set[h.Date] = true
	}
	const day = "2006-01-02"

	// Longest run over the sorted dates.
	run := 0
	var prev time.Time
	for _, h := range heatmap {
		d, err := time.Parse(day, h.Date)
		if err != nil {
			continue
		}
		if run > 0 && d.Sub(prev) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		if run > max {
			max = run
		}
		prev = d
	}

	// Current streak: walk back from today (or yesterday).
	cursor := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !set[cursor.Format(day)] {
		cursor = cursor.AddDate(0, 0, -1)
		if !set[cursor.Format(day)] {
			return active, 0, max
		}
	}
	for set[cursor.Format(day)] {
		current++
		cursor = cursor.AddDate(0, 0, -1)
	}
	return active, current, max
}

// ─── Threads ─────────────────────────────────────────

type createThreadReq struct {
	ProjectID      *string `json:"project_id"`
	Title          string  `json:"title"`
	Model          *string `json:"model"`
	SystemPrompt   *string `json:"system_prompt"`
	AgentID        *string `json:"agent_id"`
	ParentThreadID *string `json:"parent_thread_id"`
	SyncEnabled    *bool   `json:"sync_enabled"`
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	var req createThreadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := mustUserID(r)
	in := CreateThreadInput{
		UserID:       uid,
		Title:        req.Title,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		SyncEnabled:  true,
	}
	if req.SyncEnabled != nil {
		in.SyncEnabled = *req.SyncEnabled
	}
	if req.ProjectID != nil {
		if id, err := uuid.Parse(*req.ProjectID); err == nil {
			in.ProjectID = &id
		}
	}
	if req.AgentID != nil {
		if id, err := uuid.Parse(*req.AgentID); err == nil {
			in.AgentID = &id
		}
	}
	if req.ParentThreadID != nil {
		if id, err := uuid.Parse(*req.ParentThreadID); err == nil {
			in.ParentThreadID = &id
		}
	}
	t, err := s.Store.CreateThread(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, threadOut(t))
}

func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	q := r.URL.Query()

	in := ListThreadsInput{
		UserID: uid,
		Limit:  atoiDefault(q.Get("limit"), 50),
	}
	if v := q.Get("archived"); v != "" {
		b := v == "true" || v == "1"
		in.Archived = &b
	}
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			in.BeforeUpdatedAt = &t
		}
	}
	// Incremental sync pull: threads touched after this instant
	// (archived included; deleted are gone for good).
	if v := q.Get("updated_after"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			in.UpdatedAfter = &t
		}
	}
	threads, err := s.Store.ListThreads(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(threads))
	for _, t := range threads {
		out = append(out, threadOut(t))
	}
	// Cursor for next page = oldest updated_at on this page.
	var nextCursor string
	if len(threads) == in.Limit {
		nextCursor = threads[len(threads)-1].UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"threads":     out,
		"next_cursor": nextCursor,
	})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	t, err := s.Store.GetThread(r.Context(), uid, tid)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, threadOut(t))
}

type updateThreadReq struct {
	Title        *string `json:"title"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"system_prompt"`
	Pinned       *bool   `json:"pinned"`
	Archived     *bool   `json:"archived"`
	AgentID      *string `json:"agent_id"`
	SyncEnabled  *bool   `json:"sync_enabled"`
	// ModelParams: omit field to leave alone; pass `{}` to clear;
	// pass {temperature, top_p, max_tokens, stop_sequences} to set.
	ModelParams json.RawMessage `json:"model_params,omitempty"`
}

func (s *Server) handleUpdateThread(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req updateThreadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in := UpdateThreadInput{
		UserID:       uid,
		ThreadID:     tid,
		Title:        req.Title,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		Pinned:       req.Pinned,
		Archived:     req.Archived,
		SyncEnabled:  req.SyncEnabled,
		ModelParams:  req.ModelParams,
	}
	if req.AgentID != nil {
		if id, err := uuid.Parse(*req.AgentID); err == nil {
			in.AgentID = &id
		}
	}
	t, err := s.Store.UpdateThread(r.Context(), in)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, threadOut(t))
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.DeleteThread(r.Context(), uid, tid); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Messages ────────────────────────────────────────

type createMessageReq struct {
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	Parts          json.RawMessage `json:"parts"`
	ToolCallID     *string         `json:"tool_call_id"`
	ParentID       *string         `json:"parent_id"`
	Model          *string         `json:"model"`
	Status         string          `json:"status"`
	ClientID       *string         `json:"client_id"`
	AgentID        *string         `json:"agent_id"`
	MessageGroupID *string         `json:"message_group_id"`
}

func (s *Server) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	// Verify thread ownership before writing.
	if _, err := s.Store.GetThread(r.Context(), uid, tid); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	var req createMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in := CreateMessageInput{
		ThreadID:   tid,
		UserID:     uid,
		Role:       req.Role,
		Content:    req.Content,
		Parts:      []byte(req.Parts),
		ToolCallID: req.ToolCallID,
		Model:      req.Model,
		Status:     req.Status,
		ClientID:   req.ClientID,
	}
	if req.ParentID != nil {
		if id, err := uuid.Parse(*req.ParentID); err == nil {
			in.ParentID = &id
		}
	}
	if req.AgentID != nil {
		if id, err := uuid.Parse(*req.AgentID); err == nil {
			in.AgentID = &id
		}
	}
	if req.MessageGroupID != nil {
		if id, err := uuid.Parse(*req.MessageGroupID); err == nil {
			in.MessageGroupID = &id
		}
	}
	m, err := s.Store.CreateMessage(r.Context(), in)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, messageOut(m))
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	q := r.URL.Query()
	in := ListMessagesInput{
		ThreadID: tid,
		UserID:   uid,
		Limit:    atoiDefault(q.Get("limit"), 50),
	}
	if v := q.Get("after"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.AfterPosition = &n
		}
	}
	if v := q.Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.BeforePosition = &n
		}
	}
	msgs, err := s.Store.ListMessages(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageOut(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

type updateMessageReq struct {
	Content          *string         `json:"content"`
	Parts            json.RawMessage `json:"parts"`
	Status           *string         `json:"status"`
	Error            *string         `json:"error"`
	PromptTokens     *int            `json:"prompt_tokens"`
	CompletionTokens *int            `json:"completion_tokens"`
}

func (s *Server) handleUpdateMessage(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req updateMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in := UpdateMessageInput{
		UserID:           uid,
		MessageID:        mid,
		Content:          req.Content,
		Parts:            []byte(req.Parts),
		Status:           req.Status,
		ErrorMsg:         req.Error,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
	}
	m, err := s.Store.UpdateMessage(r.Context(), in)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messageOut(m))
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.DeleteMessage(r.Context(), uid, mid); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Streaming ───────────────────────────────────────

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if s.Sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_sender",
			"streaming send not configured (model-relay URL unset)")
		return
	}
	s.Sender.HandleSend(w, r, tid, uid)
}

func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_thread_id", "")
		return
	}
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_msg_id", "")
		return
	}
	if s.Sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_sender",
			"streaming send not configured (model-relay URL unset)")
		return
	}
	s.Sender.HandleRegenerate(w, r, tid, mid, uid)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if s.Sender == nil {
		// No active streamer — just mark paused in DB (covers stale
		// messages from a prior process).
		_, _ = s.Store.UpdateMessage(r.Context(), UpdateMessageInput{
			UserID:    uid,
			MessageID: mid,
			Status:    pStr(StatusPaused),
		})
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.Sender.HandleCancel(w, r, mid, uid)
}

// ─── Auth ─────────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

// ─── Output shapes ───────────────────────────────────

func threadOut(t *Thread) map[string]any {
	out := map[string]any{
		"id":               t.ID.String(),
		"user_id":          t.UserID.String(),
		"title":            t.Title,
		"last_msg_preview": t.LastMsgPreview,
		"pinned":           t.Pinned,
		"archived":         t.Archived,
		"sync_enabled":     t.SyncEnabled,
		"created_at":       t.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":       t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if t.ProjectID != nil {
		out["project_id"] = t.ProjectID.String()
	}
	if t.Model != nil {
		out["model"] = *t.Model
	}
	if t.SystemPrompt != nil {
		out["system_prompt"] = *t.SystemPrompt
	}
	if t.AgentID != nil {
		out["agent_id"] = t.AgentID.String()
	}
	if t.ParentThreadID != nil {
		out["parent_thread_id"] = t.ParentThreadID.String()
	}
	if t.Summary != nil {
		out["summary"] = *t.Summary
	}
	// Surface model_params so the client doesn't need to parse the
	// raw metadata jsonb. Empty / unset → omit so the client's null
	// check is a clean signal of "use defaults".
	if mp := parseThreadModelParams(t.Metadata); mp.hasAny() {
		out["model_params"] = mp
	}
	return out
}

func messageOut(m *Message) map[string]any {
	out := map[string]any{
		"id":         m.ID.String(),
		"thread_id":  m.ThreadID.String(),
		"role":       m.Role,
		"content":    m.Content,
		"status":     m.Status,
		"position":   m.Position,
		"created_at": m.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": m.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	// Parts JSON: render verbatim if set, else empty array.
	if len(m.Parts) > 0 {
		out["parts"] = json.RawMessage(m.Parts)
	} else {
		out["parts"] = []any{}
	}
	if m.ToolCallID != nil {
		out["tool_call_id"] = *m.ToolCallID
	}
	if m.ParentID != nil {
		out["parent_id"] = m.ParentID.String()
	}
	if m.Model != nil {
		out["model"] = *m.Model
	}
	if m.PromptTokens != nil {
		out["prompt_tokens"] = *m.PromptTokens
	}
	if m.CompletionTokens != nil {
		out["completion_tokens"] = *m.CompletionTokens
	}
	if m.ErrorMsg != nil {
		out["error"] = *m.ErrorMsg
	}
	if m.ClientID != nil {
		out["client_id"] = *m.ClientID
	}
	if m.AgentID != nil {
		out["agent_id"] = m.AgentID.String()
	}
	if m.MessageGroupID != nil {
		out["message_group_id"] = m.MessageGroupID.String()
	}
	return out
}

// ─── Plumbing ────────────────────────────────────────

func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

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

func pStr(s string) *string { return &s }
