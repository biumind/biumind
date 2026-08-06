// Package search —— wiki 项目内搜索 stub。
//
// GET /v1/wiki/projects/{pid}/search?q=...&limit=20
//
// 完整实现（B3）会做 RRF（lexical + semantic + graph 三路融合）并
// 复用 brain 现有 search infra（main.go 的 searchSrv）+ wiki_chunks
// 表 + page_community 表。当前 stub 返回空命中，让 ⌘P 命令面板能跑。
package search

import (
	"log/slog"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

type Server struct {
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/search",
		wikicommon.RequireAuth(s.Verifier, s.handleSearch))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"query": q,
		"hits":  []any{},
	})
}
