// Package apitokens —— wiki Personal API Tokens stub（B6）。
//
//	POST   /v1/wiki/tokens             create token
//	GET    /v1/wiki/tokens             list user's tokens
//	GET    /v1/wiki/tokens/whoami      current bearer info
//	DELETE /v1/wiki/tokens/{id}        revoke
//
// 完整实现复用 biumind identity 的 token 机制 + 加 wiki 维度 scope；
// 当前 stub 返回空数组 / whoami 回显 claims。
package apitokens

import (
	"log/slog"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

const moduleName = "wiki.apitokens"

type Server struct {
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	mux.HandleFunc("POST /v1/wiki/tokens", auth(s.handleCreate))
	mux.HandleFunc("GET /v1/wiki/tokens", auth(s.handleList))
	mux.HandleFunc("GET /v1/wiki/tokens/whoami", auth(s.handleWhoami))
	mux.HandleFunc("DELETE /v1/wiki/tokens/{id}", auth(s.handleRevoke))
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "create")
}
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"tokens": []any{}})
}
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	uid := wikicommon.MustUserID(r)
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": uid.String(),
	})
}
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "revoke")
}
