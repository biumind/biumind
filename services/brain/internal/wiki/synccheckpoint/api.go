// Package synccheckpoint —— wiki 客户端 sync 断点续传（B2）。
//
//	GET  /v1/wiki/projects/{pid}/sync/checkpoint    读 last-seen change_id
//	POST /v1/wiki/projects/{pid}/sync/checkpoint    保存 last-seen change_id
//
// knowcode 用此机制让客户端断网重连后从指定位置 catchup events，避免
// 全量重 sync。(user_id, project_id) → change_id 持久化在
// brain.wiki_sync_checkpoints（migration 00050），重启不丢。
package synccheckpoint

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

type Server struct {
	pool     *pgxpool.Pool
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(pool *pgxpool.Pool, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{pool: pool, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/sync/checkpoint", auth(s.handleGet))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/sync/checkpoint", auth(s.handleSet))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	uid := wikicommon.MustUserID(r).String()
	pid := r.PathValue("pid")
	var changeID string
	// No row → empty change_id (client starts from the beginning).
	_ = s.pool.QueryRow(r.Context(), `
		SELECT change_id FROM brain.wiki_sync_checkpoints
		WHERE user_id = $1 AND project_id = $2
	`, uid, pid).Scan(&changeID)
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"change_id": changeID})
}

func (s *Server) handleSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChangeID string `json:"change_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	uid := wikicommon.MustUserID(r).String()
	pid := r.PathValue("pid")
	if _, err := uuid.Parse(pid); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `
		INSERT INTO brain.wiki_sync_checkpoints (user_id, project_id, change_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, project_id)
		DO UPDATE SET change_id = EXCLUDED.change_id, updated_at = now()
	`, uid, pid, body.ChangeID); err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
