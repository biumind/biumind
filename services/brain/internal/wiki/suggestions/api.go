// Package suggestions HTTP API —— 用户反馈 / 路线图。
//
// 简化设计（vs reference/knowcode 多端点拆分）：
//   GET    /v1/wiki/suggestions               public list
//   GET    /v1/wiki/suggestions/me            my list
//   GET    /v1/wiki/suggestions/{sid}         detail
//   POST   /v1/wiki/suggestions               submit
//   PATCH  /v1/wiki/suggestions/{sid}         author edit
//   DELETE /v1/wiki/suggestions/{sid}         author soft-delete
//   POST   /v1/wiki/suggestions/{sid}/votes   vote up
//   DELETE /v1/wiki/suggestions/{sid}/votes   remove vote
//
// 暂未实现（B6.x 后段）：comments / roadmap 视图（按 status 分桶）/
// release-bound suggestions / admin 状态变更。
package suggestions

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(s *Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	mux.HandleFunc("GET /v1/wiki/suggestions", auth(s.handleListPublic))
	mux.HandleFunc("GET /v1/wiki/suggestions/me", auth(s.handleListMine))
	mux.HandleFunc("GET /v1/wiki/suggestions/{sid}", auth(s.handleGet))
	mux.HandleFunc("POST /v1/wiki/suggestions", auth(s.handleCreate))
	mux.HandleFunc("PATCH /v1/wiki/suggestions/{sid}", auth(s.handlePatch))
	mux.HandleFunc("DELETE /v1/wiki/suggestions/{sid}", auth(s.handleDelete))
	mux.HandleFunc("POST /v1/wiki/suggestions/{sid}/votes", auth(s.handleVote))
	mux.HandleFunc("DELETE /v1/wiki/suggestions/{sid}/votes", auth(s.handleUnvote))
	// TODO: comments / roadmap / per-release listing 留 B6.x 后段
	mux.HandleFunc("GET /v1/wiki/roadmap", auth(s.handleRoadmap))
	mux.HandleFunc("GET /v1/wiki/suggestions/{sid}/comments",
		auth(s.handleListComments))
	mux.HandleFunc("POST /v1/wiki/suggestions/{sid}/comments",
		auth(s.handleAddComment))
	mux.HandleFunc("GET /v1/wiki/releases/{ver}/suggestions",
		auth(s.handleListByRelease))
}

type out struct {
	ID        string `json:"id"`
	AuthorID  string `json:"author_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Category  string `json:"category"`
	Status    string `json:"status"`
	Votes     int    `json:"votes"`
	MyVote    bool   `json:"my_vote"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toJSON(s *Suggestion) out {
	return out{
		ID:        s.ID.String(),
		AuthorID:  s.AuthorID.String(),
		Title:     s.Title,
		Body:      s.Body,
		Category:  s.Category,
		Status:    s.Status,
		Votes:     s.Votes,
		MyVote:    s.MyVote,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) handleListPublic(w http.ResponseWriter, r *http.Request) {
	uid := wikicommon.MustUserID(r)
	rows, err := s.Store.ListPublic(r.Context(), uid, 100)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	items := make([]out, 0, len(rows))
	for _, x := range rows {
		items = append(items, toJSON(x))
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleListMine(w http.ResponseWriter, r *http.Request) {
	uid := wikicommon.MustUserID(r)
	rows, err := s.Store.ListMine(r.Context(), uid, 100)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	items := make([]out, 0, len(rows))
	for _, x := range rows {
		items = append(items, toJSON(x))
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	v, err := s.Store.Get(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "suggestion")
			return
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, toJSON(v))
}

type createReq struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Title == "" {
		wikicommon.WriteErr(w, http.StatusBadRequest, "missing_title", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	v, err := s.Store.Create(r.Context(), uid, req.Title, req.Body, req.Category)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusCreated, toJSON(v))
}

type patchReq struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"`
	Status   string `json:"status"`
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := wikicommon.MustUserID(r)
	// status 字段当前仅 author 可改（admin 调状态走另一端点 — TODO）
	v, err := s.Store.Patch(r.Context(), id, uid,
		req.Title, req.Body, req.Category, req.Status)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "suggestion")
			return
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, toJSON(v))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	if err := s.Store.SoftDelete(r.Context(), id, uid); err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "suggestion")
			return
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	n, err := s.Store.Vote(r.Context(), id, uid, true)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"votes": n, "my_vote": true,
	})
}

func (s *Server) handleUnvote(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	n, err := s.Store.Vote(r.Context(), id, uid, false)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"votes": n, "my_vote": false,
	})
}

// ── TODO B6.x 后段 ─────────────────────────────────────────────

func (s *Server) handleRoadmap(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"buckets": []any{}})
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, "wiki.suggestions", "add_comment")
}

func (s *Server) handleListByRelease(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}
