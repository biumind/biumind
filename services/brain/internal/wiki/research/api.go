// HTTP surface for deep-research tasks.
//
//	POST /v1/wiki/projects/{pid}/research        kick off a task; returns 202 + task_id
//	GET  /v1/wiki/projects/{pid}/research        list project tasks
//	GET  /v1/wiki/projects/{pid}/research/{id}   read single task (poll for status)
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
}

// ─── Wire types ────────────────────────────────────────────────

type taskOut struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"project_id"`
	Topic      string   `json:"topic"`
	Queries    []string `json:"queries"`
	Status     string   `json:"status"`
	PageID     *string  `json:"page_id,omitempty"`
	WebResults []WebHit `json:"web_results,omitempty"`
	Synthesis  string   `json:"synthesis,omitempty"`
	Error      string   `json:"error,omitempty"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
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
	if out.Queries == nil {
		out.Queries = []string{}
	}
	return out
}

// ─── Handlers ──────────────────────────────────────────────────

type createReq struct {
	Topic   string   `json:"topic"`
	Queries []string `json:"queries"`
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
	task, err := s.Store.Create(r.Context(), pid, uid, topic, queries)
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
