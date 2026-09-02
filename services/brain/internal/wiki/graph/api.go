// Package graph —— wiki 项目图谱 HTTP 面（与 biumind 顶层 internal/graph
// 是两套独立体系：顶层是 memory graph，本包是 wiki page 图谱）。
//
// 路由：
//
//	GET  /v1/wiki/projects/{pid}/graph          nodes + edges（读 brain.pages + page_relevance）
//	POST /v1/wiki/projects/{pid}/graph/recompute 手动触发单项目 Louvain 重算（202 异步）
//	POST /v1/wiki/projects/{pid}/graph/insights  结构启发式 surprising connections + knowledge gaps
//
// insights 是纯算法（结构启发式，零 LLM），
// 数据全来自 relevance worker 预算的 page_relevance + pages.community_id，
// 无新表。前置：relevance worker 至少跑过一次（RELEVANCE_INTERVAL_HOURS>0）。
package graph

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

const moduleName = "wiki.graph"

// RecomputeFunc 单项目重算入口（relevance.Worker.RecomputeProject）。
type RecomputeFunc func(ctx context.Context, projectID uuid.UUID) (int, error)

type Server struct {
	Store    *Store
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger

	recompute RecomputeFunc
	inflight  sync.Map // projectID → struct{}，在飞去重
}

func NewServer(store *Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: store, Wiki: w, Verifier: v, Logger: l}
}

// WithRecompute 接线手动重算（main.go 在 relevance worker 建好后来调）。
// 未接线时 recompute 端点保持 501。
func (s *Server) WithRecompute(fn RecomputeFunc) *Server {
	s.recompute = fn
	return s
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

// handleRecompute —— 手动触发单项目重算：异步执行，立即 202。
// 同项目在飞则去重（仍 202，幂等语义）；未接线 recompute（relevance
// worker 被禁用）时保持 501 诚实降级。
func (s *Server) handleRecompute(w http.ResponseWriter, r *http.Request) {
	pid, ok := s.parseProject(w, r)
	if !ok {
		return
	}
	if s.recompute == nil {
		wikicommon.NotImplemented(w, moduleName, "recompute")
		return
	}
	if _, loaded := s.inflight.LoadOrStore(pid, struct{}{}); loaded {
		wikicommon.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "already_running"})
		return
	}
	go func() {
		defer s.inflight.Delete(pid)
		// 脱离请求 ctx（响应即关）；上限放宽到 5min 覆盖大项目。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		n, err := s.recompute(ctx, pid)
		if err != nil {
			s.Logger.Warn("graph recompute failed", "project_id", pid, "err", err)
			return
		}
		s.Logger.Info("graph recompute done", "project_id", pid, "pairs", n)
	}()
	wikicommon.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "recomputing"})
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
