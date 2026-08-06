// Package api implements Brain.Memory HTTP endpoints.
//
//	POST   /v1/memory                 store a memory
//	GET    /v1/memory                 list mine (filter by project_id, kind)
//	GET    /v1/memory/recall          recall by query string
//	DELETE /v1/memory/{id}            delete one I own
//
// All endpoints require a Bearer JWT and scope memories to the caller's
// user id. Project membership is enforced via wiki.Store ownership checks
// — memories live under a project the caller owns.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type Server struct {
	Memory   *memstore.Store
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	// Embedder is optional. When non-nil, /v1/memory/recall computes a
	// query embedding and asks the store for hybrid (semantic + lexical)
	// ranking. When nil, the recall path stays lexical-only.
	Embedder embed.Embedder
	Logger   *slog.Logger
}

func NewServer(m *memstore.Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Memory: m, Wiki: w, Verifier: v, Logger: l}
}

// WithEmbedder enables hybrid recall ranking. Returns the same Server
// for chaining.
func (s *Server) WithEmbedder(e embed.Embedder) *Server {
	s.Embedder = e
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/memory", s.requireAuth(s.handleStore))
	mux.HandleFunc("GET /v1/memory", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/memory/recall", s.requireAuth(s.handleRecall))
	mux.HandleFunc("DELETE /v1/memory/{id}", s.requireAuth(s.handleDelete))
}

// ─── Store ──────────────────────────────────────────────

type storeReq struct {
	ProjectID string  `json:"project_id"`
	Kind      string  `json:"kind"`
	Content   string  `json:"content"`
	Salience  float32 `json:"salience"`
}

func (s *Server) handleStore(w http.ResponseWriter, r *http.Request) {
	var req storeReq
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
	if !s.ownsProject(w, r, pid) {
		return
	}
	s.warnDeprecatedKind(r, req.Kind, w)
	m, err := s.Memory.Create(r.Context(), memstore.StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: req.Kind,
		Content: req.Content, Salience: req.Salience,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "memory api: store",
			"project_id", pid, "user_id", uid, "kind", req.Kind,
			"content_bytes", len(req.Content), "salience", req.Salience,
			"memory_id", m.ID)
	}
	writeJSON(w, http.StatusOK, memOut(m))
}

// ─── List ───────────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pidStr := r.URL.Query().Get("project_id")
	pid, err := uuid.Parse(pidStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	kind := r.URL.Query().Get("kind")
	s.warnDeprecatedKind(r, kind, w)
	ms, err := s.Memory.List(r.Context(), memstore.ListInput{
		ProjectID: pid, OwnerID: uid, Kind: kind, Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, memOut(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": out})
}

// ─── Recall ─────────────────────────────────────────────

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	pidStr := r.URL.Query().Get("project_id")
	pid, err := uuid.Parse(pidStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	uid := mustUserID(r)
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "missing_q", "query string `q` required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	// If an embedder is configured, compute a query embedding so the
	// store can rank by hybrid semantic + lexical score. Embedding
	// failures fall through to lexical-only — recall must keep working
	// when the provider hiccups.
	var qvec []float32
	mode := "lexical"
	if s.Embedder != nil {
		ec, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		v, err := s.Embedder.Embed(ec, q)
		cancel()
		if err == nil {
			qvec = v
			mode = "hybrid"
		} else if s.Logger != nil {
			s.Logger.Warn("recall embed failed; falling back to lexical",
				"err", err)
		}
	}

	kind := r.URL.Query().Get("kind")
	s.warnDeprecatedKind(r, kind, w)
	ms, err := s.Memory.Recall(r.Context(), memstore.RecallInput{
		ProjectID: pid, OwnerID: uid, Query: q,
		QueryEmbedding: qvec,
		Kind:           kind, Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if s.Logger != nil {
		var topScore float32
		if len(ms) > 0 {
			topScore = ms[0].Score
		}
		s.Logger.DebugContext(r.Context(), "memory api: recall",
			"project_id", pid, "user_id", uid,
			"query_bytes", len(q), "kind", kind, "limit", limit,
			"mode", mode, "hits", len(ms), "top_score", topScore)
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		row := memOut(m)
		row["score"] = m.Score
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"memories": out, "query": q, "mode": mode,
	})
}

// ─── Delete ─────────────────────────────────────────────

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := mustUserID(r)
	if err := s.Memory.Delete(r.Context(), uid, id); err != nil {
		if errors.Is(err, memstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// ─── helpers ────────────────────────────────────────────

// warnDeprecatedKind emits a one-shot warning + Deprecation response
// header when the caller used the legacy "skill" kind. The store layer
// silently rewrites it to "habit"; this signals to client authors that
// they should migrate before the alias is removed (2026-08-25).
func (s *Server) warnDeprecatedKind(r *http.Request, kind string, w http.ResponseWriter) {
	if _, deprecated := memstore.NormalizeKind(kind); !deprecated {
		return
	}
	if w != nil {
		// RFC 8594 Deprecation header. Sunset header carries the cutoff.
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Sunset", "Mon, 25 Aug 2026 00:00:00 GMT")
		w.Header().Add("Link",
			`<https://docs.biumind.app/skills#memory-kind-rename>; rel="deprecation"`)
	}
	if s.Logger != nil {
		s.Logger.Warn("memory.kind=skill is deprecated; use 'habit'",
			"path", r.URL.Path,
			"user", mustUserID(r).String())
	}
}

func (s *Server) ownsProject(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := mustUserID(r)
	p, err := s.Wiki.GetProject(r.Context(), pid)
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
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func memOut(m *memstore.Memory) map[string]any {
	return map[string]any{
		"id":               m.ID.String(),
		"project_id":       m.ProjectID.String(),
		"kind":             m.Kind,
		"content":          m.Content,
		"salience":         m.Salience,
		"last_accessed_at": m.LastAccessedAt.UTC().Format(time.RFC3339),
		"created_at":       m.CreatedAt.UTC().Format(time.RFC3339),
	}
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
