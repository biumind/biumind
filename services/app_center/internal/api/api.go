// Package api implements the App Center HTTP surface.
//
//	GET    /v1/apps                       list registered apps (manifests)
//	GET    /v1/apps/{name}                get one manifest
//	POST   /v1/apps/{name}/invoke         {action, input}: invoke an app action
//	POST   /v1/apps/repo/analyze          {repo_url}: analyse a GitHub repo (M1.4)
//	POST   /v1/apps/repo/installs         {repo_url, ref_type, config}: install a repo app
//	GET    /v1/apps/installs/{id}/runtime repo-app runtime status
//	GET    /v1/apps/installs/{id}/builds  repo-app build history
//	POST   /v1/apps/installs/{id}/redeploy queue a repo-app redeploy
//
// All routes JWT-gated. The invoke path additionally runs Authz against
// the caller's installation row (lookup by scope=user + sub + identifier),
// short-circuits on not-installed / disabled, and only then dispatches to
// the registry — see handleInvoke. App-level permissions (declared in the
// Manifest) remain enforced inside the registry.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/installs"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
	"github.com/biumind/biumind/services/app_center/internal/sidebar"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InvokeAuthorizer 是 invoke 路径的鉴权 hook。生产由 *installs.Installer
// 实现 (它已带 GetByIdentifier + AuthorizeInvoke); 单测可注入 stub
// 不依赖 *pgxpool.Pool。
type InvokeAuthorizer interface {
	GetByIdentifier(ctx context.Context, scope, scopeID, identifier string) (*installs.Installation, error)
	AuthorizeInvoke(ctx context.Context, in *installs.Installation, userID, orgID string, roles []string) error
}

type Server struct {
	Reg       *biuapp.Registry
	Verifier  *bauth.Verifier
	Logger    *slog.Logger
	Installer *installs.Installer // optional; nil = v1.0 stateless mode

	// Pool + Registry mirror Reg, attached separately so the webhook
	// handler (api/webhook.go, mounted on a different muxpoint) can
	// reach them without re-running auth. Set via SetPool /
	// SetBiuappRegistry from the runtime daemon main.go after the
	// pool is created.
	Pool     *pgxpool.Pool
	Registry *biuapp.Registry

	// SidebarSvc — lazy-initialised on first MountSidebar call from
	// pool. Tests construct directly to inject stubs.
	SidebarSvc *sidebar.Service

	// invokeAuth — 可选 invoke 鉴权 override; nil = fallback 到
	// Installer。测试用 WithInvokeAuthorizer 注入 stub 隔离 DB。
	invokeAuth InvokeAuthorizer

	// RepoAnalyzer — GitHub client behind the /v1/apps/repo/* endpoints
	// (M1.4). Constructed unconditionally in main (empty GITHUB_TOKEN =
	// anonymous access); nil here means the endpoints 503 repo_disabled.
	RepoAnalyzer *repoanalyze.Client
}

// WithInvokeAuthorizer 注入 invoke 鉴权 stub (单测用)。生产通过
// WithInstaller 自动获得鉴权 (Installer 实现了 InvokeAuthorizer)。
func (s *Server) WithInvokeAuthorizer(a InvokeAuthorizer) *Server {
	s.invokeAuth = a
	return s
}

// currentInvokeAuth 选当前生效的 InvokeAuthorizer。优先 stub override,
// 然后 Installer; 都缺失返回 nil → handleInvoke 走 stateless 模式。
func (s *Server) currentInvokeAuth() InvokeAuthorizer {
	if s.invokeAuth != nil {
		return s.invokeAuth
	}
	if s.Installer != nil {
		return s.Installer
	}
	return nil
}

func NewServer(reg *biuapp.Registry, v *bauth.Verifier, l *slog.Logger) *Server {
	if l == nil {
		l = slog.Default()
	}
	return &Server{Reg: reg, Verifier: v, Logger: l}
}

// WithInstaller wires the install lifecycle path. Without it, the
// installs / sidebar endpoints return 503; the v1.0 catalogue and
// invoke routes still work.
func (s *Server) WithInstaller(i *installs.Installer) *Server {
	s.Installer = i
	return s
}

// WithRepoAnalyzer wires the GitHub analysis client for the repo-app
// endpoints (M1.4).
func (s *Server) WithRepoAnalyzer(c *repoanalyze.Client) *Server {
	s.RepoAnalyzer = c
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/apps", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/apps/{name}", s.requireAuth(s.handleGet))
	mux.HandleFunc("POST /v1/apps/{name}/invoke", s.requireAuth(s.handleInvoke))

	// v1.5 install lifecycle (require Installer to be wired).
	mux.HandleFunc("GET /v1/apps/installs", s.requireAuth(s.handleListInstalls))
	mux.HandleFunc("POST /v1/apps/installs", s.requireAuth(s.handleInstall))
	mux.HandleFunc("GET /v1/apps/installs/{id}", s.requireAuth(s.handleGetInstall))
	mux.HandleFunc("DELETE /v1/apps/installs/{id}", s.requireAuth(s.handleUninstall))
	mux.HandleFunc("PATCH /v1/apps/installs/{id}", s.requireAuth(s.handleToggleInstall))

	// v2.0 — user_webview create-and-install (M12).
	mux.HandleFunc("POST /v1/apps/user_webview", s.requireAuth(s.handleCreateUserWebView))

	// Per-agent grants (M3.3).
	mux.HandleFunc("GET /v1/apps/installs/{id}/agents", s.requireAuth(s.handleListAgentGrants))
	mux.HandleFunc("POST /v1/apps/installs/{id}/agents", s.requireAuth(s.handleGrantAgent))
	mux.HandleFunc("DELETE /v1/apps/installs/{id}/agents/{agentID}", s.requireAuth(s.handleRevokeAgent))

	// Upgrade flow (M15).
	mux.HandleFunc("GET /v1/apps/installs/{id}/upgrade", s.requireAuth(s.handleCheckUpgrade))
	mux.HandleFunc("POST /v1/apps/installs/{id}/upgrade", s.requireAuth(s.handleUpgrade))

	// Repo Apps (M1.4) — analyse / install / runtime / builds / redeploy.
	mux.HandleFunc("POST /v1/apps/repo/analyze", s.requireAuth(s.handleRepoAnalyze))
	mux.HandleFunc("POST /v1/apps/repo/installs", s.requireAuth(s.handleRepoInstall))
	mux.HandleFunc("GET /v1/apps/installs/{id}/runtime", s.requireAuth(s.handleRepoRuntime))
	mux.HandleFunc("GET /v1/apps/installs/{id}/builds", s.requireAuth(s.handleRepoBuilds))
	mux.HandleFunc("POST /v1/apps/installs/{id}/redeploy", s.requireAuth(s.handleRepoRedeploy))
}

// catalogApp is the catalogue wire shape: the manifest fields plus the
// repo-app columns (tier / repo_meta) merged in from app_center.apps for
// gh_*-source rows. The client identifies a repo app by repo_meta != null.
type catalogApp struct {
	biuapp.Manifest
	Tier     string          `json:"tier,omitempty"`
	RepoMeta json.RawMessage `json:"repo_meta,omitempty"`
}

// repoCatalogExtras holds the per-identifier repo columns fetched from
// the catalogue table.
type repoCatalogExtras struct {
	tier     string
	repoMeta json.RawMessage
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	manifests := s.Reg.List()
	ids := make([]string, 0, len(manifests))
	for _, m := range manifests {
		ids = append(ids, m.Slug())
	}
	extras := s.fetchRepoCatalogExtras(r.Context(), ids)
	apps := make([]catalogApp, 0, len(manifests))
	for _, m := range manifests {
		entry := catalogApp{Manifest: m}
		if e, ok := extras[m.Slug()]; ok {
			entry.Tier = e.tier
			entry.RepoMeta = e.repoMeta
		}
		apps = append(apps, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	app, ok := s.Reg.Get(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	entry := catalogApp{Manifest: app.Manifest()}
	if extras := s.fetchRepoCatalogExtras(r.Context(), []string{entry.Slug()}); extras != nil {
		if e, ok := extras[entry.Slug()]; ok {
			entry.Tier = e.tier
			entry.RepoMeta = e.repoMeta
		}
	}
	writeJSON(w, http.StatusOK, entry)
}

// fetchRepoCatalogExtras batch-loads tier / repo_meta for the gh_*-source
// catalogue rows among identifiers. Stateless mode (nil pool) and query
// failures degrade to "no extras" — the catalogue must keep serving
// bundled apps even when the repo columns can't be read.
func (s *Server) fetchRepoCatalogExtras(ctx context.Context, identifiers []string) map[string]repoCatalogExtras {
	if s.Pool == nil || len(identifiers) == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (identifier) identifier, COALESCE(tier, ''), repo_meta
		  FROM app_center.apps
		 WHERE source LIKE 'gh\_%' AND identifier = ANY($1)
		 ORDER BY identifier, created_at DESC
	`, identifiers)
	if err != nil {
		if s.Logger != nil {
			s.Logger.WarnContext(ctx, "app_center: repo catalog extras lookup failed", "err", err)
		}
		return nil
	}
	defer rows.Close()

	out := map[string]repoCatalogExtras{}
	for rows.Next() {
		var (
			id   string
			tier string
			meta []byte
		)
		if err := rows.Scan(&id, &tier, &meta); err != nil {
			if s.Logger != nil {
				s.Logger.WarnContext(ctx, "app_center: repo catalog extras scan failed", "err", err)
			}
			return nil
		}
		out[id] = repoCatalogExtras{tier: tier, repoMeta: meta}
	}
	return out
}

type invokeReq struct {
	Action string          `json:"action"`
	Input  json.RawMessage `json:"input"`
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	var req invokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Action == "" {
		writeErr(w, http.StatusBadRequest, "missing_action", "")
		return
	}

	name := r.PathValue("name")

	// Authz 链路 (P0 — invoke 之前曾经只过 Bearer 校验, 任何登录 user
	// 都能调任意 app 的任意 action; 现已对齐 Install/Toggle/Uninstall):
	//
	//   1. lookup install by (scope=user, sub, identifier=name) — 没装 403
	//   2. enabled 检查 — disabled 的 install 不接受 invoke
	//   3. Authz.Check action=app:invoke — DENY 返 403
	//
	// Stateless v1.0 模式 (Installer/InvokeAuthorizer 都没挂, e.g. dev /
	// 单测) 跳过这层并打 WARN。生产部署必挂 Installer。
	if auth := s.currentInvokeAuth(); auth != nil {
		claims, _ := bauth.ClaimsFrom(r.Context())
		if claims == nil {
			writeErr(w, http.StatusUnauthorized, "no_claims", "")
			return
		}
		install, err := auth.GetByIdentifier(r.Context(), "user", claims.UserID, name)
		if err != nil {
			if errors.Is(err, installs.ErrNotFound) {
				writeErr(w, http.StatusForbidden, "not_installed",
					"install required before invoking actions")
				return
			}
			writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
			return
		}
		if !install.Enabled {
			writeErr(w, http.StatusForbidden, "install_disabled",
				"this installation is currently disabled")
			return
		}
		if err := auth.AuthorizeInvoke(
			r.Context(), install, claims.UserID, claims.OrgID, claims.Roles,
		); err != nil {
			if errors.Is(err, installs.ErrPermissionDenied) {
				writeErr(w, http.StatusForbidden, "permission_denied", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "authz_failed", err.Error())
			return
		}
	} else {
		s.Logger.Warn("invoke without Installer (stateless mode); Authz skipped",
			"app", name, "action", req.Action)
	}

	invokeStart := time.Now()
	out, err := s.Reg.Invoke(r.Context(), name, req.Action, req.Input)
	if err != nil {
		// Map common cases to right status code; everything else is 500.
		msg := err.Error()
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "app_center: invoke failed",
				"app", name, "action", req.Action,
				"latency_ms", time.Since(invokeStart).Milliseconds(),
				"err", msg)
		}
		switch {
		case stringContains(msg, "unknown app"):
			writeErr(w, http.StatusNotFound, "not_found", msg)
		case stringContains(msg, "no action"):
			writeErr(w, http.StatusBadRequest, "unknown_action", msg)
		default:
			writeErr(w, http.StatusInternalServerError, "invoke_failed", msg)
		}
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "app_center: invoke ok",
			"app", name, "action", req.Action,
			"latency_ms", time.Since(invokeStart).Milliseconds())
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": out})
}

// ─── helpers ──────────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		raw := auth[7:]
		claims, err := s.Verifier.Verify(raw)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		// 单点记录 app_center 全部 protected 路由 (apps/installs/agents/sidebar/
		// rss/radar/rankings/permissions/triggers...)。debug 模式下能一行
		// 看到调用者 + path,排查"App 触发为什么没跑起来"最有用。
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "app_center api: request",
				"user_id", claims.UserID, "method", r.Method,
				"path", r.URL.Path)
		}
		ctx := bauth.WithRawToken(bauth.WithClaims(r.Context(), claims), raw)
		next(w, r.WithContext(ctx))
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

func stringContains(s, sub string) bool { return strings.Contains(s, sub) }
