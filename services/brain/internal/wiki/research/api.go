// HTTP surface for deep-research tasks.
//
//	POST   /v1/wiki/projects/{pid}/research              kick off a task; returns 202 + task_id
//	GET    /v1/wiki/projects/{pid}/research              list project tasks
//	GET    /v1/wiki/projects/{pid}/research/{id}         read single task (poll for status)
//	DELETE /v1/wiki/projects/{pid}/research/{id}         hard-delete a terminal task (409 while active)
//	POST   /v1/wiki/projects/{pid}/research/{id}/rerun   reset a terminal task to queued and re-run
//	POST   /v1/wiki/projects/{pid}/research/optimize     LLM-expand a raw topic into {topic, queries}
//
// Auth: same Bearer + project-ownership check as the rest of brain
// wiki.
package research

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// Server hosts the research HTTP routes.
type Server struct {
	Store    *Store
	Wiki     *wikistore.Store
	Orch     *Orchestrator
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(s *Store, w *wikistore.Store, o *Orchestrator, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Wiki: w, Orch: o, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/research", s.requireAuth(s.handleCreate))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/research", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/research/{id}", s.requireAuth(s.handleGet))
	mux.HandleFunc("DELETE /v1/wiki/projects/{pid}/research/{id}", s.requireAuth(s.handleDelete))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/research/{id}/rerun", s.requireAuth(s.handleRerun))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/research/optimize", s.requireAuth(s.handleOptimize))
}

// ─── Wire types ────────────────────────────────────────────────

type taskOut struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id"`
	Topic          string   `json:"topic"`
	Queries        []string `json:"queries"`
	Status         string   `json:"status"`
	PageID         *string  `json:"page_id,omitempty"`
	WebResults     []WebHit `json:"web_results,omitempty"`
	Synthesis      string   `json:"synthesis,omitempty"`
	Error          string   `json:"error,omitempty"`
	SourceReviewID *string  `json:"source_review_id,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func taskJSON(t *Task) taskOut {
	out := taskOut{
		ID:         t.ID.String(),
		ProjectID:  t.ProjectID.String(),
		Topic:      t.Topic,
		Queries:    t.Queries,
		Status:     t.Status,
		WebResults: t.WebResults,
		Synthesis:  t.Synthesis,
		Error:      t.ErrorMessage,
		CreatedAt:  t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.PageID != nil {
		v := t.PageID.String()
		out.PageID = &v
	}
	if t.SourceReviewID != nil {
		v := t.SourceReviewID.String()
		out.SourceReviewID = &v
	}
	if out.Queries == nil {
		out.Queries = []string{}
	}
	return out
}

// ─── Handlers ──────────────────────────────────────────────────

type createReq struct {
	Topic   string   `json:"topic"`
	Queries []string `json:"queries"`
	// SourceReviewID marks the task as spawned from a review queue entry
	// (reviews_page「研究」action). The orchestrator auto-resolves that
	// review once the research page lands. Optional.
	SourceReviewID string `json:"source_review_id"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		writeErr(w, http.StatusBadRequest, "missing_topic", "")
		return
	}
	queries := make([]string, 0, len(req.Queries))
	for _, q := range req.Queries {
		q = strings.TrimSpace(q)
		if q != "" {
			queries = append(queries, q)
		}
	}
	if s.Orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "research_disabled",
			"deep research is not configured on this server")
		return
	}
	var sourceReviewID *uuid.UUID
	if raw := strings.TrimSpace(req.SourceReviewID); raw != "" {
		rid, err := uuid.Parse(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_source_review_id", "")
			return
		}
		sourceReviewID = &rid
	}
	task, err := s.Store.Create(r.Context(), pid, uid, topic, queries, sourceReviewID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Detach from the request context so cancelling the HTTP call
	// doesn't kill the orchestrator goroutine. The orchestrator owns
	// its own timeouts internally.
	go s.Orch.Run(context.Background(), task.ID)
	writeJSON(w, http.StatusAccepted, taskJSON(task))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	tasks, err := s.Store.ListByProject(r.Context(), pid, 50)
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
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_task_id", "")
		return
	}
	t, err := s.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if t.ProjectID != pid {
		// Don't leak existence — same response as not_found.
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(t))
}

// loadTask fetches the task and verifies it belongs to the URL project.
// Writes the error response and returns nil on failure (missing or
// cross-project both collapse to 404 — don't leak existence).
func (s *Server) loadTask(w http.ResponseWriter, r *http.Request, pid uuid.UUID) *Task {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_task_id", "")
		return nil
	}
	t, err := s.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return nil
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return nil
	}
	if t.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return nil
	}
	return t
}

// handleDelete hard-deletes a terminal task (research_tasks has no
// soft-delete column). Active tasks are refused with 409 — the research
// state machine has no cancel signal (contrast ingest's
// cancel_requested_at), so the aligned semantic is "wait for a terminal
// state, then delete". The produced wiki page (if any) is user content
// and survives.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	t := s.loadTask(w, r, pid)
	if t == nil {
		return
	}
	if IsActiveStatus(t.Status) {
		writeErr(w, http.StatusConflict, "task_active",
			"task is still running; wait for a terminal status before deleting")
		return
	}
	ok, err := s.Store.Delete(r.Context(), t.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !ok {
		// Raced with a rerun that flipped the task back to active.
		writeErr(w, http.StatusConflict, "task_active", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": t.ID.String()})
}

// handleRerun resets a terminal (done/error) task to 'queued' and
// re-runs it through the normal pipeline (same path as boot Recover →
// Orch.Run). Topic + queries are kept; phase outputs are cleared. The
// conditional reset in ResetForRerun makes concurrent reruns of the same
// task collapse to one — the loser gets 409. No rerunOfTaskId lineage is
// recorded: the rerun reuses the same task row, so the old row is the
// lineage.
func (s *Server) handleRerun(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if s.Orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "research_disabled",
			"deep research is not configured on this server")
		return
	}
	t := s.loadTask(w, r, pid)
	if t == nil {
		return
	}
	if IsActiveStatus(t.Status) {
		writeErr(w, http.StatusConflict, "task_active",
			"task is still running; wait for a terminal status before rerunning")
		return
	}
	ok, err := s.Store.ResetForRerun(r.Context(), t.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !ok {
		// Lost a race with another rerun / delete.
		writeErr(w, http.StatusConflict, "task_active", "")
		return
	}
	// Same detach-from-request-context convention as handleCreate.
	go s.Orch.Run(context.Background(), t.ID)
	// Re-read so the response reflects the reset (cleared outputs,
	// status=queued) rather than the pre-reset row we loaded above.
	if fresh, err := s.Store.Get(r.Context(), t.ID); err == nil {
		t = fresh
	}
	writeJSON(w, http.StatusAccepted, taskJSON(t))
}

type optimizeReq struct {
	Topic string `json:"topic"`
}

// handleOptimize expands a rough one-line topic into {topic, queries}
// via the orchestrator's LLM caller (model-relay 内部车道，计费归
// owner)。LLM 失败 / 输出不可解析 → 502；orchestrator 未配置 → 503。
func (s *Server) handleOptimize(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if s.Orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "research_disabled",
			"deep research is not configured on this server")
		return
	}
	var req optimizeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		writeErr(w, http.StatusBadRequest, "missing_topic", "")
		return
	}
	uid := mustUserID(r)
	out, err := s.Orch.OptimizeTopic(r.Context(), uid, topic)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "optimize_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── Auth helpers (parallel to wiki/reviews) ──────────────────
func (s *Server) ownsProject(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := mustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return false
	}
	if proj.OwnerID != uid {
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
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
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
