// rbac — Role-Based Access Control 共享中间件.
//
// 用法 (in service main.go):
//
//   cache := auth.NewRoleCache(pool)
//   if err := cache.Reload(ctx); err != nil { ... }
//
//   mux.HandleFunc("GET /v1/admin/users",
//       cache.RequirePermission(verifier, "users:read:safe")(handleListUsers))
//
// 设计:
//   - DB 是 source of truth (identity.role_permissions 表)
//   - 服务启动时全量 Load 进内存 byRole map
//   - 通配匹配: '*' / 'users:*' / 'users:read:*' 都被识别
//   - 不在 token 里塞 permissions (token 只放 roles, 由本 cache 解析)
//   - 后续: PG NOTIFY/LISTEN 监听 role_permissions 改动实时刷新

package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleCache 内存缓存 role → permissions.
//
// 线程安全: 读用 RWMutex 读锁 (热路径); Reload 用写锁.
type RoleCache struct {
	pool *pgxpool.Pool

	mu      sync.RWMutex
	byRole  map[string]map[string]struct{} // role → set of permission names
}

// NewRoleCache 构造空缓存. 必须先调 Reload 才有数据.
func NewRoleCache(pool *pgxpool.Pool) *RoleCache {
	return &RoleCache{
		pool:   pool,
		byRole: map[string]map[string]struct{}{},
	}
}

// Reload 从 DB 读全量 role_permissions 重建 cache. 启动时调一次,
// 之后 superadmin 改了权限矩阵也调 (后续 commit 上 PG NOTIFY 自动触发).
//
// 失败不影响已有 cache (出错时保留旧数据). 返回 err 让调用方决定是否
// fail-loud.
func (c *RoleCache) Reload(ctx context.Context) error {
	rows, err := c.pool.Query(ctx, `
		SELECT role_name, permission_name FROM identity.role_permissions
	`)
	if err != nil {
		return fmt.Errorf("rbac: load role_permissions: %w", err)
	}
	defer rows.Close()

	next := map[string]map[string]struct{}{}
	for rows.Next() {
		var role, perm string
		if err := rows.Scan(&role, &perm); err != nil {
			return fmt.Errorf("rbac: scan: %w", err)
		}
		set, ok := next[role]
		if !ok {
			set = map[string]struct{}{}
			next[role] = set
		}
		set[perm] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rbac: rows: %w", err)
	}

	c.mu.Lock()
	c.byRole = next
	c.mu.Unlock()
	return nil
}

// HasPermission 判断 role 是否拥有 perm. 通配规则:
//
//   '*'              → 任何 perm 都通过
//   'users:*'        → 任何 'users:*' perm 都通过
//   'users:read:*'   → 任何 'users:read:*' perm 都通过
//   精确字符串       → 完全匹配
func (c *RoleCache) HasPermission(role, perm string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	perms, ok := c.byRole[role]
	if !ok {
		return false
	}
	// 全权
	if _, ok := perms["*"]; ok {
		return true
	}
	// 精确匹配
	if _, ok := perms[perm]; ok {
		return true
	}
	// 通配:逐级缩短查 prefix:*
	parts := strings.Split(perm, ":")
	for i := len(parts) - 1; i >= 1; i-- {
		wildcard := strings.Join(parts[:i], ":") + ":*"
		if _, ok := perms[wildcard]; ok {
			return true
		}
	}
	return false
}

// RoleNames 返回所有已加载 role 名称, 用于诊断日志.
func (c *RoleCache) RoleNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byRole))
	for r := range c.byRole {
		out = append(out, r)
	}
	return out
}

// PermissionsForRole 返回 role 的所有 permissions (用于 /v1/auth/me 给前端).
// 返回 nil 表示 role 不存在或无权限. 排序稳定方便前端缓存比对.
func (c *RoleCache) PermissionsForRole(role string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	perms, ok := c.byRole[role]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	// 按字符串排序方便前端 deep-equal 检测变化
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// RequirePermission middleware. Bearer 缺失 → 401; 无该权限 → 403; 通过 →
// claims 注入 context.
//
// 检查顺序:
//   1. Bearer token 解析 (verifier.Verify)
//   2. roles 取 claims.Roles[0] (当前单角色; 多角色后期扩展)
//   3. cache.HasPermission(role, perm)
//   4. claims 注入 ctx, 调用 next
func (c *RoleCache) RequirePermission(verifier *Verifier, perm string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, err := authenticate(r, verifier)
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
				return
			}
			role := primaryRole(claims)
			if !c.HasPermission(role, perm) {
				writeAuthErr(w, http.StatusForbidden, "forbidden",
					fmt.Sprintf("permission %q required (role %q)", perm, role))
				return
			}
			next(w, r.WithContext(WithClaims(r.Context(), claims)))
		}
	}
}

// RequireAnyRole middleware. 角色列表任一即可. 比 RequirePermission 粗,
// 用于"只 superadmin 可改 role" 这种场景.
func (c *RoleCache) RequireAnyRole(verifier *Verifier, roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	wanted := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		wanted[r] = struct{}{}
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, err := authenticate(r, verifier)
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
				return
			}
			role := primaryRole(claims)
			if _, ok := wanted[role]; !ok {
				writeAuthErr(w, http.StatusForbidden, "forbidden",
					fmt.Sprintf("role %q not allowed", role))
				return
			}
			next(w, r.WithContext(WithClaims(r.Context(), claims)))
		}
	}
}

// HasPermissionFromContext 给 handler 内部用 — 比如要按是否有
// users:read:full 字段决定 DTO 脱敏.
func (c *RoleCache) HasPermissionFromContext(ctx context.Context, perm string) bool {
	claims, ok := ClaimsFrom(ctx)
	if !ok {
		return false
	}
	return c.HasPermission(primaryRole(claims), perm)
}

// ─── 内部工具 ───────────────────────────────────────────

// authenticate 从 r 解析 Bearer token, 返回 claims.
func authenticate(r *http.Request, verifier *Verifier) (*Claims, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("missing bearer token")
	}
	return verifier.Verify(strings.TrimPrefix(auth, "Bearer "))
}

// primaryRole 拿 claims.Roles 第一个; 空数组返 "user" 兜底.
func primaryRole(c *Claims) string {
	if c == nil || len(c.Roles) == 0 {
		return "user"
	}
	return c.Roles[0]
}

// writeAuthErr 简单 JSON 错误响应. 跟 admin/handlers 风格一致.
func writeAuthErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, msg)
}
