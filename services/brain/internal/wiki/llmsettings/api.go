// Package llmsettings —— wiki 项目级 LLM 配置 stub（B6）。
//
//	GET /v1/wiki/projects/{pid}/llm-settings   读取项目 model/provider 偏好
//	PUT /v1/wiki/projects/{pid}/llm-settings   更新偏好
//	GET /v1/wiki/projects/{pid}/llm-status     当前可用 provider/model 健康度
//
// 完整实现继承用户级 LLM 设置 + 项目级覆盖；当前 stub 返回空对象，
// 客户端 fallback 到用户级。
package llmsettings

import (
	"log/slog"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
)

const moduleName = "wiki.llmsettings"

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
	base := "/v1/wiki/projects/{pid}"
	mux.HandleFunc("GET "+base+"/llm-settings", auth(s.handleGetSettings))
	mux.HandleFunc("PUT "+base+"/llm-settings", auth(s.handleUpdateSettings))
	mux.HandleFunc("GET "+base+"/llm-status", auth(s.handleGetStatus))
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"overrides": map[string]any{}})
}
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, moduleName, "update_settings")
}
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"provider": "default",
		"healthy":  true,
	})
}
