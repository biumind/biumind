// Package api 是 services/aigc 的 HTTP endpoint 集合.
//
// 设计风格参考 services/identity/internal/api: net/http stdlib 1.22+ 路由模式,
// Server struct 持有所有依赖 (store / authz / billing / verifier), Mount 一次性
// 挂全部路由.
//
// 现行不变量 (BiuMind-Chat-WebSocket-Migration §10):
//   - 双向流走 WS SDK Protocol (chat/agent/task), Realtime 多 topic 通知保留 SSE
//   - AIGC 任务进度通过 Realtime SSE topic=aigc.user.{uid}.tasks 推送 (服务端写
//     NATS, 由 services/realtime 转 SSE), 不在本 api 包内处理.
package api

import (
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/aigc/internal/authz"
	"github.com/biumind/biumind/services/aigc/internal/billing"
	"github.com/biumind/biumind/services/aigc/internal/store"
)

// Server 持有 endpoint 依赖. main.go 装配后调 Mount.
type Server struct {
	Store    *store.Store
	Authz    authz.Decider   // nil 时 endpoint 退化到 AlwaysAllow (dev), main.go 须打 warning
	Billing  *billing.Client // nil 时积分相关 endpoint 返 503
	Verifier *bauth.Verifier // 用户 JWT 验签
	Blob     BlobGetter      // nil 时 /v1/aigc/files-by-sha 返 503 (dev 无 MinIO)

	// submitDeps — 由 SetSubmitDeps 注入, 含 NATS Bus.
	// nil Bus 走 NoopBus 行为 (静默, 不阻塞响应).
	submitDeps SubmitDeps
}

// Mount 把全部路由挂到给定 mux. 顺序参考 services/identity/internal/api/handlers.go
// (具体路径要在 /{id} 之前挂 /others / /export 这种字面量片段).
func (s *Server) Mount(mux *http.ServeMux) {
	// ── 公开 (无需登录) ─────────────────────────
	mux.HandleFunc("GET /v1/models", s.handleListModels)
	// P4.S3.6: GET /v1/providers (admin only) 已下线 — provider 字典统一在
	// model-relay 的 /v1/admin/providers (admin Vue 单源).
	mux.HandleFunc("GET /v1/gallery", s.handleListGallery)

	// ── 用户态 (需登录) ─────────────────────────
	// 生成任务核心 endpoints (POST submit / GET task / mine / visibility / delete / cancel)
	s.MountGenerations(mux)

	// 数字人角色 + 音色字典 (P5-4)
	s.MountCharacters(mux)
	mux.HandleFunc("GET /v1/voices", s.requireAuth(s.handleListVoices))

	// CAS 产物下载 — 客户端 cas:<sha> 解析到这里 (output_thumbnail.dart).
	// 命名空间 /v1/aigc/ 与 brain 的通用文件 /v1/files/by-sha 消歧 (site nginx
	// 单 origin 下路径唯一决定上游, 不能两服务都占顶层 /v1/files-by-sha).
	mux.HandleFunc("GET /v1/aigc/files-by-sha/{sha}", s.requireAuth(s.handleDownloadBySha))

	// (后续) /v1/hotparse/* / /v1/prompt/optimize
}

// ─── auth middleware ───────────────────────────────────

// requireAuth 解 Bearer JWT, 把 claims 注入 ctx; 失败直接 401.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		if s.Verifier == nil {
			writeErr(w, http.StatusInternalServerError, "verifier_not_wired", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		s.submitLogger().DebugContext(r.Context(), "aigc api: request",
			"user_id", claims.UserID, "method", r.Method, "path", r.URL.Path)
		r = r.WithContext(bauth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}
