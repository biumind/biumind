// rbac_handlers — RBAC 矩阵 admin REST API.
//
//   GET /v1/admin/rbac/matrix
//     列所有 role + permission + role→perms 映射, 一次性返回供前端渲染
//
//   PUT /v1/admin/rbac/roles/{role}/permissions
//     原子替换 role 的 permission 集合, body: {"permissions": ["..."]}
//     成功后 trigger RoleCache.Reload, 立即生效 (无需重启 / refresh)
//
// 鉴权:
//   GET 矩阵        — roles:read (admin/superadmin/viewer 都能读)
//   PUT 改矩阵     — 仅 superadmin (改权限 = 提权, 必须最高权限)
//
// 兜底: 不能把 superadmin 的 '*' 权限去掉 (防止系统失主).

package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// RBACMatrix — GET /v1/admin/rbac/matrix 响应.
type RBACMatrix struct {
	Roles       []Role              `json:"roles"`
	Permissions []Permission        `json:"permissions"`
	Matrix      map[string][]string `json:"matrix"` // role → permissions
}

func (s *Server) handleRBACMatrix(w http.ResponseWriter, r *http.Request) {
	if s.RBAC == nil {
		writeErr(w, http.StatusServiceUnavailable, "rbac_disabled", "RBAC store not wired")
		return
	}
	roles, err := s.RBAC.ListRoles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_roles", err.Error())
		return
	}
	perms, err := s.RBAC.ListPermissions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_permissions", err.Error())
		return
	}
	matrix, err := s.RBAC.ListRolePermissions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_role_permissions", err.Error())
		return
	}
	if matrix == nil {
		matrix = map[string][]string{}
	}
	writeJSON(w, http.StatusOK, RBACMatrix{
		Roles: roles, Permissions: perms, Matrix: matrix,
	})
}

type setRolePermissionsReq struct {
	Permissions []string `json:"permissions"`
}

func (s *Server) handleSetRolePermissions(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if !hasAnyRole(claims, "superadmin") {
		writeErr(w, http.StatusForbidden, "forbidden",
			"only superadmin can change role permissions")
		return
	}
	if s.RBAC == nil {
		writeErr(w, http.StatusServiceUnavailable, "rbac_disabled", "RBAC store not wired")
		return
	}
	role := r.PathValue("role")
	if role == "" {
		writeErr(w, http.StatusBadRequest, "role_required", "")
		return
	}
	var req setRolePermissionsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// 防呆: superadmin 的 '*' 权限不能被剥夺 (会让系统失主).
	if role == "superadmin" {
		hasStar := false
		for _, p := range req.Permissions {
			if p == "*" {
				hasStar = true
				break
			}
		}
		if !hasStar {
			writeErr(w, http.StatusBadRequest, "superadmin_must_keep_star",
				"superadmin must retain '*' permission")
			return
		}
	}

	// 防呆: user 角色应保持空 (终端用户没有后台权限). 允许但 warn.
	if role == "user" && len(req.Permissions) > 0 && s.Logger != nil {
		s.Logger.Warn("rbac: granting permissions to 'user' role — terminal users will gain admin access",
			"perms", req.Permissions)
	}

	actor := actorID(r)
	added, removed, err := s.RBAC.ReplaceRolePermissions(r.Context(), role, req.Permissions, actor)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "replace_failed", err.Error())
		return
	}

	// 立刻 reload RoleCache, 让权限变化对所有在线用户生效 (不用等 token 过期).
	// reload 失败不阻塞响应 (DB 已落定), 但要 audit.
	reloadErr := ""
	if s.RoleCache != nil {
		if err := s.RoleCache.Reload(r.Context()); err != nil {
			reloadErr = err.Error()
			if s.Logger != nil {
				s.Logger.Error("rbac: reload after PUT failed", "err", err, "role", role)
			}
		}
	}

	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "rbac.role.permissions.update",
		Resource:   "role",
		Target:     role,
		TargetType: "role",
		Detail: fmt.Sprintf("added=%d removed=%d total=%d perms=%s",
			added, removed, len(req.Permissions),
			strings.Join(req.Permissions, ",")),
		Success: true,
	})

	resp := map[string]any{
		"role":    role,
		"added":   added,
		"removed": removed,
		"total":   len(req.Permissions),
	}
	if reloadErr != "" {
		resp["reload_warning"] = reloadErr
	}
	writeJSON(w, http.StatusOK, resp)
}
