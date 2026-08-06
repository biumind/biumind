// Package oauth —— OAuth 2.1 authorization-code flow stub（B6）。
// 给外部 AI 客户端（Claude.ai / Cursor / Codex / 等）做 PKCE 授权进入
// biumind 数据。
//
//	GET  /.well-known/oauth-authorization-server  metadata discovery
//	POST /v1/wiki/oauth/register                  Dynamic Client Registration
//	GET  /v1/wiki/oauth/authorize                 302 → /connect 同意页
//	GET  /v1/wiki/oauth/authorize/info            consent screen 数据
//	POST /v1/wiki/oauth/grant                     用户同意 → code
//	POST /v1/wiki/oauth/token                     code/refresh → access_token
//
// 注意：knowcode 原版把 well-known 放在 /，但 biumind 各服务共享前缀，
// 这里 well-known 仍走 / 根（无 prefix），其他全部 /v1/wiki/oauth/。
package oauth

import (
	"log/slog"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

const moduleName = "wiki.oauth"

type Server struct {
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("POST /v1/wiki/oauth/register", s.handleRegister)
	mux.HandleFunc("GET /v1/wiki/oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("GET /v1/wiki/oauth/authorize/info",
		wikicommon.RequireAuth(s.Verifier, s.handleAuthorizeInfo))
	mux.HandleFunc("POST /v1/wiki/oauth/grant",
		wikicommon.RequireAuth(s.Verifier, s.handleGrant))
	mux.HandleFunc("POST /v1/wiki/oauth/token", s.handleToken)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                "https://biumind.invalid", // 占位 — B6 接 cfg.PublicBaseURL
		"authorization_endpoint":                "/v1/wiki/oauth/authorize",
		"token_endpoint":                        "/v1/wiki/oauth/token",
		"registration_endpoint":                 "/v1/wiki/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "register")
}
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "authorize")
}
func (s *Server) handleAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "authorize_info")
}
func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "grant")
}
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "token")
}
