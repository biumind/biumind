// Package graph —— wiki 项目图谱 HTTP 面（与 biumind 顶层 internal/graph
// 是两套独立体系：顶层是 memory graph，本包是 wiki page 图谱）。
//
// 路由：
//
//	GET  /v1/wiki/projects/{pid}/graph          nodes + edges（读 brain.pages + page_relevance）
//	POST /v1/wiki/projects/{pid}/graph/recompute trigger Louvain（B3 批次，仍 501）
//	POST /v1/wiki/projects/{pid}/graph/insights  结构启发式 surprising connections + knowledge gaps
//
// insights 是纯算法（结构启发式，零 LLM），
// 数据全来自 relevance worker 预算的 page_relevance + pages.community_id，
// 无新表。前置：relevance worker 至少跑过一次（RELEVANCE_INTERVAL_HOURS>0）。
package graph

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

const moduleName = "wiki.graph"

type Server struct {
	Store    *Store
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(store *Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: store, Wiki: w, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/graph", auth(s.handleGet))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/graph/recompute", auth(s.handleRecompute))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/graph/insights", auth(s.handleInsights))
}

// handleGet returns the full page graph (nodes + relevance edges) for
// client-side force layout. Empty project → empty arrays (200, not 404).
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	pid, ok := s.parseProject(w, r)
	if !ok {
		return
	}
	g, err := s.Store.LoadProjectGraph(r.Context(), pid)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, g)
}

// handleInsights runs the structural heuristics over the project graph.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	pid, ok := s.parseProject(w, r)
	if !ok {
		return
	}
	g, err := s.Store.LoadProjectGraph(r.Context(), pid)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, ComputeInsights(g))
}

// handleRecompute — B3 批次，仍 501。relevance worker 周期重算，
// 手动触发单项目重算待 B3 落地（需把 relevance.Worker.scanProject 暴露）。
func (s *Server) handleRecompute(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "recompute")
}

// ─── helpers ───────────────────────────────────────────────────

// parseProject extracts + validates the {pid} path project, enforcing
// ownership. Writes the error response + returns ok=false on failure.
func (s *Server) parseProject(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "project")
		return uuid.Nil, false
	}
	uid := wikicommon.MustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		// Indistinguishable 404 — no existence leak across tenants.
		if errors.Is(err, wikistore.ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "project")
			return uuid.Nil, false
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return uuid.Nil, false
	}
	if proj.OwnerID != uid {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "project")
		return uuid.Nil, false
	}
	return pid, true
}
