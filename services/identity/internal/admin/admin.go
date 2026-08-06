// Package admin exposes the operator-facing REST API: list users,
// inspect a single user's plan + quota usage, override a plan
// manually (for support cases that bypass Stripe), and read the
// audit trail of admin actions.
//
// Auth: every endpoint requires the JWT to carry the `admin` scope.
// Without it requests get 403.
//
// Audit: every mutation is appended to an in-memory ring buffer.
// The MVP keeps the last 1000 events; production wires this to a
// persistent table (audit_log) once we agree on retention SLAs.

package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/credits"
)

// User is the projection admin handlers return — minimal PII, no
// password hash, no tokens.
//
// Role 是后台 RBAC 主键. 'user' 表示终端用户 (无后台权限);
// 'admin'/'support'/'finance'/'ops'/'viewer'/'superadmin' 是后台角色.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Plan      string    `json:"plan"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is the slice of identity.Store the admin API needs. Keeping
// this thin lets tests inject fakes without depending on real Postgres.
type Store interface {
	ListUsers(query string, limit, offset int) ([]User, int, error)
	GetUser(id string) (*User, error)
	SetUserPlan(id string, plan billing.Plan, actor string) error

	// SetUserRole 改用户 role + 记录 actor / reason. 返回 ErrNotFound
	// 当 id 不存在.
	SetUserRole(id, role, actor, reason string) error

	// RevokeAllSessions 撤销该用户所有未失效 refresh_token, 返回受影响数.
	// 改 role 后联动调用, 让旧 access token 自然过期 (≤15min) 后强制重登.
	RevokeAllSessions(id string) (int64, error)

	// CountUsersByRole 用于 "不能删最后一个 superadmin" 校验.
	CountUsersByRole(role string) (int, error)
}

// AuditEvent is one entry in the audit ring.
//
// 字段对齐 audit.events 表 (services/identity/migrations/00003_rbac.sql).
// ActorID 留空表示匿名 (登录失败 / 未认证调用); Success=false 时
// ErrorCode/ErrorMessage 应填上下文.
type AuditEvent struct {
	At           time.Time `json:"at"`
	ActorID      string    `json:"actor_id,omitempty"`
	ActorEmail   string    `json:"actor_email,omitempty"`
	ActorRole    string    `json:"actor_role,omitempty"`
	ActorIP      string    `json:"actor_ip,omitempty"`
	ActorUA      string    `json:"actor_ua,omitempty"`
	Action       string    `json:"action"`
	Resource     string    `json:"resource,omitempty"`
	Target       string    `json:"target,omitempty"`
	TargetType   string    `json:"target_type,omitempty"`
	Detail       string    `json:"detail,omitempty"`
	Success      bool      `json:"success"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// Auditor — 审计日志读写抽象.
//
// 实现:
//   *AuditLog      — 内存 ring buffer (1000 条上限, 重启丢失)
//   *pgAudit       — Postgres 持久化 (audit.events 表)
//   compositeAudit — 双写: ring + pg, 读时优先 pg, 失败 fallback ring
//
// 默认生产用 composite (main.go wire), 单元测试用 *AuditLog.
type Auditor interface {
	Append(AuditEvent)
	Recent(n int) []AuditEvent
}

// AuditLog is the ring buffer. Concurrent-safe; capacity is set
// at construction. 仍保留作为 PG 不可用时的 fallback (composite 内部).
type AuditLog struct {
	mu  sync.Mutex
	buf []AuditEvent
	cap int
	pos int // next write index
	full bool
}

func NewAuditLog(capacity int) *AuditLog {
	if capacity <= 0 {
		capacity = 1000
	}
	return &AuditLog{buf: make([]AuditEvent, capacity), cap: capacity}
}

func (a *AuditLog) Append(ev AuditEvent) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buf[a.pos] = ev
	a.pos = (a.pos + 1) % a.cap
	if a.pos == 0 {
		a.full = true
	}
}

// Recent returns the last `n` events newest-first; n<=0 returns all.
func (a *AuditLog) Recent(n int) []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	size := a.pos
	if a.full {
		size = a.cap
	}
	out := make([]AuditEvent, 0, size)
	// Walk backwards from pos-1 to oldest.
	for i := 0; i < size; i++ {
		idx := (a.pos - 1 - i + a.cap) % a.cap
		out = append(out, a.buf[idx])
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}

// CompositeAudit 双写 — DB 主要, ring 保底.
type CompositeAudit struct {
	pg   *pgAudit
	ring *AuditLog
	log  *slog.Logger
}

func NewCompositeAudit(pg *pgAudit, ring *AuditLog, log *slog.Logger) *CompositeAudit {
	if log == nil {
		log = slog.Default()
	}
	return &CompositeAudit{pg: pg, ring: ring, log: log}
}

// Append 双写: 总是落 ring (内存兜底), DB 失败只 warn 不阻塞业务.
func (c *CompositeAudit) Append(ev AuditEvent) {
	c.ring.Append(ev)
	if c.pg == nil {
		return
	}
	if err := c.pg.Append(ev); err != nil {
		c.log.Warn("audit: db append failed, kept in ring buffer",
			"err", err, "action", ev.Action)
	}
}

// Recent 优先 DB 查 (有完整历史), DB 失败降级 ring (最近 1000 条).
func (c *CompositeAudit) Recent(n int) []AuditEvent {
	if c.pg == nil {
		return c.ring.Recent(n)
	}
	events, err := c.pg.Recent(n)
	if err != nil {
		c.log.Warn("audit: db query failed, falling back to ring",
			"err", err)
		return c.ring.Recent(n)
	}
	return events
}

// Server is the HTTP bundle.
type Server struct {
	Store        Store
	Audit        Auditor
	Verifier     *bauth.Verifier
	Logger       *slog.Logger
	Monitor      *Monitor           // 可选
	SystemConfig *SystemConfigStore // 可选, nil 时 system config endpoints 返 503

	// RBAC 矩阵管理 — nil 时 /v1/admin/rbac/* 返 503.
	// RoleCache 注入用于 PUT 后即时 reload, nil 时改完需服务重启才生效.
	RBAC      *RBACStore
	RoleCache *bauth.RoleCache

	// AuditSummary 注入后, /v1/admin/audit/summary 返 dashboard 卡片数据.
	// 跟 Audit (Auditor 接口) 分开 — Summary 是只读聚合查询, 不属 write 路径.
	AuditSummary *pgAudit

	// W2-9 plans 仓储注入. 非 nil 时 PlanLimits 从 DB 读 (billing.plans),
	// 合法性校验也走 DB. nil 时退化用 billing.DefaultLimits 内置 (向下兼容).
	Plans *billing.PlansRepo

	// Credits 积分服务 — 注入后 /v1/admin/users/{id}/credits/grant 可用,
	// handleGetUser 返回余额. nil 时充值 endpoint 返 503, 余额返 nil.
	Credits *credits.Service
}

// New 构造 admin Server. audit 传 nil 时退化为内存 ring buffer (单元测试用),
// 生产应该传 NewCompositeAudit 或 pgAudit.
func New(store Store, audit Auditor, verifier *bauth.Verifier, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if audit == nil {
		audit = NewAuditLog(1000)
	}
	return &Server{Store: store, Audit: audit, Verifier: verifier, Logger: logger}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("GET /v1/admin/users/{id}", s.requireAdmin(s.handleGetUser))
	mux.HandleFunc("POST /v1/admin/users/{id}/plan", s.requireAdmin(s.handleSetPlan))
	// 充值积分 — admin/superadmin 可调 (requireAdmin). kind 固定 permanent,
	// source 服务端硬编码 SourceAdmin (防伪造 recharge/refund 流水).
	mux.HandleFunc("POST /v1/admin/users/{id}/credits/grant", s.requireAdmin(s.handleCreditsGrant))
	// 改 role + 撤 session 仅 superadmin 可调 (handler 内二次校验)
	mux.HandleFunc("PATCH /v1/admin/users/{id}/role", s.requireAdmin(s.handleSetRole))
	mux.HandleFunc("DELETE /v1/admin/users/{id}/sessions", s.requireAdmin(s.handleRevokeSessions))
	mux.HandleFunc("GET /v1/admin/audit", s.requireAdmin(s.handleAudit))
	mux.HandleFunc("GET /v1/admin/audit/summary", s.requireAdmin(s.handleAuditSummary))
	// monitor — 比 admin 范围宽 (admin/superadmin/ops/viewer 都能看)
	mux.HandleFunc("GET /v1/admin/monitor/services",   s.requireMonitorRead(s.handleMonitorServices))
	mux.HandleFunc("GET /v1/admin/monitor/query",      s.requireMonitorRead(s.handleMonitorQuery))

	// system config — admin/superadmin 读, superadmin 写 (handler 内校验)
	mux.HandleFunc("GET /v1/admin/system/config",        s.requireAdmin(s.handleSystemConfigList))
	mux.HandleFunc("PUT /v1/admin/system/config/{key}",  s.requireAdmin(s.handleSystemConfigSet))
	// 发测试邮件 — superadmin 验证 SMTP 配置好不好用 (handler 内校验)
	mux.HandleFunc("POST /v1/admin/system/test-email",   s.requireAdmin(s.handleTestEmail))

	// RBAC 矩阵 — admin/superadmin 读 (查权限矩阵), superadmin 写 (handler 内校验)
	mux.HandleFunc("GET /v1/admin/rbac/matrix",                       s.requireAdmin(s.handleRBACMatrix))
	mux.HandleFunc("PUT /v1/admin/rbac/roles/{role}/permissions",     s.requireAdmin(s.handleSetRolePermissions))

	// internal — alertmanager webhook, biu-net 内部访问, 无 JWT
	// nginx 已配 /v1/internal/* deny all (公网拦截)
	mux.HandleFunc("POST /v1/internal/alerts/email", s.handleAlertWebhook)
}

// ─── Handlers ───────────────────────────────────────────

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	users, total, err := s.Store.ListUsers(q, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": users, "total": total,
		"limit": limit, "offset": offset,
	})
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := s.Store.GetUser(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// W2-9: 优先从 DB 读 (billing.plans), nil 时 fallback 到 DefaultLimits.
	var limits billing.PlanLimits
	if s.Plans != nil {
		limits = s.Plans.ResolveLimits(r.Context(), billing.Plan(u.Plan))
	} else {
		limits = billing.LimitsFor(billing.Plan(u.Plan))
	}
	// 余额展示 — Credits 未注入或 user_id 非法时返 nil, 不让详情页因余额查询挂掉.
	var balance any
	if s.Credits != nil {
		if uid, perr := uuid.Parse(id); perr == nil {
			if bal, err := s.Credits.GetBalance(r.Context(), uid); err == nil {
				balance = bal
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    u,
		"limits":  limits,
		"balance": balance,
	})
}

type setPlanReq struct {
	Plan   string `json:"plan"`
	Reason string `json:"reason"`
}

func (s *Server) handleSetPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req setPlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	plan := billing.Plan(req.Plan)
	// W2-9: 合法性校验 — 优先 DB, 同步 fallback DefaultLimits.
	planValid := false
	if s.Plans != nil {
		if _, err := s.Plans.Get(r.Context(), plan); err == nil {
			planValid = true
		}
	}
	if !planValid {
		_, planValid = billing.DefaultLimits[plan]
	}
	if !planValid {
		writeErr(w, http.StatusBadRequest, "invalid_plan",
			fmt.Sprintf("unknown plan %q", req.Plan))
		return
	}
	actor := actorID(r)
	if err := s.Store.SetUserPlan(id, plan, actor); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "user.plan.override",
		Resource:   "user",
		Target:     id,
		TargetType: "user",
		Detail:     fmt.Sprintf("plan=%s reason=%q", plan, req.Reason),
		Success:    true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"updated": id, "plan": plan})
}

// creditsGrantReq — 管理员手动充值入账. kind 固定 permanent (产品决策仅永久),
// source 服务端硬编码 SourceAdmin (前端不传, 防伪造 recharge/refund 流水).
// reason 必填 (审计用). idempotency_key 前端每次打开 dialog 生成 uuid 传入
// (双击同一笔确认被幂等拦截); 未传时服务端兜底自动生成.
type creditsGrantReq struct {
	Amount         int64  `json:"amount"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (s *Server) handleCreditsGrant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.Credits == nil {
		writeErr(w, http.StatusServiceUnavailable, "credits_disabled", "credits service not wired")
		return
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user_id", "invalid user id")
		return
	}
	var req creditsGrantReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Amount <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_amount", "amount must be > 0")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeErr(w, http.StatusBadRequest, "reason_required", "充值必须填写原因 (审计用)")
		return
	}
	actor := actorID(r)
	idem := req.IdempotencyKey
	if idem == "" {
		idem = "admin:" + actor + ":" + uuid.NewString()
	}
	remark := fmt.Sprintf("admin %s: %s", actor, strings.TrimSpace(req.Reason))
	pkg, bal, err := s.Credits.Grant(r.Context(), credits.GrantArgs{
		UserID:         uid,
		Amount:         req.Amount,
		Kind:           credits.KindPermanent,
		Source:         credits.SourceAdmin,
		Remark:         remark,
		IdempotencyKey: idem,
	})
	if err != nil {
		status, code := http.StatusInternalServerError, "internal"
		switch {
		case errors.Is(err, credits.ErrInvalidAmount):
			status, code = http.StatusBadRequest, "invalid_amount"
		case errors.Is(err, credits.ErrInvalidKindExpiresAt):
			status, code = http.StatusBadRequest, "invalid_kind"
		}
		writeErr(w, status, code, err.Error())
		return
	}
	// 审计双写: credit_logs 无 operator_id 列, 操作人靠 admin.AuditEvent 记录
	// (credit_logs.remark 也含 admin id, 双重可追溯).
	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "user.credits.grant",
		Resource:   "user",
		Target:     id,
		TargetType: "user",
		Detail:     fmt.Sprintf("amount=%d kind=permanent reason=%q", req.Amount, req.Reason),
		Success:    true,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"package": pkg,
		"balance": bal,
	})
}

// 已知 7 个 role. 改成 DB 查 identity.roles 是 commit 后续优化项;
// 当前硬编码足够 (跟 schema CHECK + RoleCache seed 一致).
var validRoles = map[string]bool{
	"user": true, "support": true, "finance": true, "ops": true,
	"admin": true, "superadmin": true, "viewer": true,
}

type setRoleReq struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// PATCH /v1/admin/users/{id}/role
//
// 业务规则:
//  1. 调用方必须是 superadmin (claims.Roles 含 superadmin)
//  2. 不能改自己 role (避免 admin 互升 / superadmin 自降锁死)
//  3. 不能把最后一个 superadmin 降级 (防止系统失主)
//  4. role 必须在 7 个已知值里
//  5. reason 必填 (audit 必须有 why)
//  6. 改完后撤所有 session, 让旧 access token (≤15min) 过期后必须重登
//  7. 全程入 audit, action='user.role.change', detail 含 from/to/reason
func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if !hasAnyRole(claims, "superadmin") {
		writeErr(w, http.StatusForbidden, "forbidden",
			"only superadmin can change roles")
		return
	}
	id := r.PathValue("id")
	actor := actorID(r)

	var req setRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !validRoles[req.Role] {
		writeErr(w, http.StatusBadRequest, "invalid_role",
			fmt.Sprintf("unknown role %q", req.Role))
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeErr(w, http.StatusBadRequest, "reason_required",
			"reason is required for role change (audit)")
		return
	}
	if id == actor {
		writeErr(w, http.StatusBadRequest, "self_change_forbidden",
			"cannot change own role")
		return
	}

	// 查目标用户拿当前 role (用于"防降最后一个 superadmin" + audit detail)
	target, err := s.Store.GetUser(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// 防降最后一个 superadmin: 当前是 super 且新 role 不是 super 时检查
	if target.Role == "superadmin" && req.Role != "superadmin" {
		count, err := s.Store.CountUsersByRole("superadmin")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if count <= 1 {
			writeErr(w, http.StatusBadRequest, "last_superadmin",
				"cannot demote the last superadmin")
			return
		}
	}

	// 已是该 role 直接 noop, 但仍写 audit 记录尝试 (运维误操作排查用)
	if target.Role == req.Role {
		s.Audit.Append(AuditEvent{
			ActorID:    actor,
			ActorIP:    ClientIP(r),
			ActorUA:    r.UserAgent(),
			Action:     "user.role.change.noop",
			Resource:   "user",
			Target:     id,
			TargetType: "user",
			Detail:     fmt.Sprintf("already %s, reason=%q", req.Role, req.Reason),
			Success:    true,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"updated": id, "role": req.Role, "noop": true,
		})
		return
	}

	if err := s.Store.SetUserRole(id, req.Role, actor, req.Reason); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// 撤所有 session — 改完立即生效
	revoked, _ := s.Store.RevokeAllSessions(id) // 失败不阻塞 (token 自然过期兜底)

	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "user.role.change",
		Resource:   "user",
		Target:     id,
		TargetType: "user",
		Detail: fmt.Sprintf("from=%s to=%s reason=%q sessions_revoked=%d",
			target.Role, req.Role, req.Reason, revoked),
		Success: true,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"updated":           id,
		"role":              req.Role,
		"sessions_revoked":  revoked,
	})
}

// DELETE /v1/admin/users/{id}/sessions
//
// 撤销目标用户所有 refresh_token. 用于:
//   - 用户密码泄露后 admin 主动踢人
//   - 改完 role / plan 后强制 reload claims (虽然改 role 会自动撤)
//
// 鉴权: superadmin 或 admin (有 sessions:revoke perm 的角色, 当前简化为
// admin/superadmin 都可)
func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if !hasAnyRole(claims, "admin", "superadmin") {
		writeErr(w, http.StatusForbidden, "forbidden",
			"sessions:revoke required")
		return
	}
	id := r.PathValue("id")
	actor := actorID(r)

	revoked, err := s.Store.RevokeAllSessions(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "user.sessions.revoke",
		Resource:   "user",
		Target:     id,
		TargetType: "user",
		Detail:     fmt.Sprintf("revoked=%d", revoked),
		Success:    true,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"target":   id,
		"revoked":  revoked,
	})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	n := atoiDefault(r.URL.Query().Get("limit"), 100)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": s.Audit.Recent(n),
	})
}

// handleAuditSummary — Dashboard 用的聚合数据. ?window=24h 默认.
// 没注入 AuditSummary (单元测试) 直接返零值, 不报错.
func (s *Server) handleAuditSummary(w http.ResponseWriter, r *http.Request) {
	if s.AuditSummary == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"summary": map[string]any{"window_seconds": 0},
		})
		return
	}
	window := 24 * time.Hour
	if w := r.URL.Query().Get("window"); w != "" {
		if d, err := time.ParseDuration(w); err == nil {
			window = d
		}
	}
	sum, err := s.AuditSummary.Summary(window)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": sum})
}

// ─── Auth middleware ────────────────────────────────────

// adminRoles 任一即可访问 admin endpoint. RBAC 完整版会按 endpoint 细分
// permission, 当前 commit 先做 "admin/superadmin 全权" 的最小可用版.
//
// 后续 commit 引入 RequirePermission("users:read:full") 之类替换这里.
var adminRoles = []string{"admin", "superadmin"}

// requireAdmin: Bearer 缺失 → 401; JWT 有效但 roles 不含 admin/superadmin
// → 403; 通过 → claims 注入 context 给 handler.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		if !hasAnyRole(claims, adminRoles...) {
			writeErr(w, http.StatusForbidden, "forbidden",
				"admin role required")
			return
		}
		ctx := bauth.WithClaims(r.Context(), claims)
		next(w, r.WithContext(ctx))
	}
}

// hasAnyRole 检查 claims.Roles 是否含 wanted 中任一. 将来 RBAC 完整版
// 在 packages/go-sdk/biu/auth 提供, 当前先就近放这里.
func hasAnyRole(c *bauth.Claims, wanted ...string) bool {
	for _, w := range wanted {
		for _, r := range c.Roles {
			if r == w {
				return true
			}
		}
	}
	return false
}

func actorID(r *http.Request) string {
	c, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		return ""
	}
	return c.UserID
}

// ClientIP 提取客户端 IP. 信任 X-Forwarded-For 链头一个 (nginx/CF 设的),
// 退化用 r.RemoteAddr. 拿不到合法值返 "" — pgAudit 写 NULL.
//
// 注意: 部署时 nginx 必须保证 X-Forwarded-For 是 set 而非 add, 否则恶意
// client 自造 XFF 头会污染审计源 IP. 当前 deploy/nginx/admin.conf 设了
// `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`, 链头是
// 真实 client IP.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF 可能是 "client, proxy1, proxy2" — 取第一个
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		ip := strings.TrimSpace(xff)
		if ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// r.RemoteAddr 形如 "1.2.3.4:5678" 或 "[::1]:5678"
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		// 处理 ipv6 [::1]:port
		host := addr[:i]
		host = strings.TrimPrefix(host, "[")
		host = strings.TrimSuffix(host, "]")
		return host
	}
	return addr
}

// ─── Plumbing ───────────────────────────────────────────

func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fallback
	}
	return n
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

// quiet — keep errors importable for future error wrapping.
var _ = errors.New
