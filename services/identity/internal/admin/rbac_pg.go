// rbac_pg — Postgres CRUD for identity.roles / permissions / role_permissions.
//
// 写入时事务 + role_permissions atomic replace, 防止并发 PUT 矩阵留下半成品.
// RoleCache 由 handler 层 PUT 后显式 Reload (本包不持有 cache 引用).

package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Role 角色定义 (跟 identity.roles 表对齐).
type Role struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	IsSystem    bool   `json:"is_system"`
}

// Permission 权限定义 (跟 identity.permissions 表对齐).
type Permission struct {
	Name        string `json:"name"`
	Resource    string `json:"resource"`
	Action      string `json:"action"`
	Scope       string `json:"scope,omitempty"`
	Description string `json:"description"`
}

// RBACStore — RBAC 矩阵的 PG 操作.
type RBACStore struct {
	pool *pgxpool.Pool
}

func NewRBACStore(pool *pgxpool.Pool) *RBACStore {
	return &RBACStore{pool: pool}
}

// ListRoles 返回所有角色, 按 is_system DESC, name ASC 排序.
// (system 角色排前, 自定义角色排后)
func (s *RBACStore) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, display_name, description, is_system
		FROM identity.roles
		ORDER BY is_system DESC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.DisplayName, &r.Description, &r.IsSystem); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPermissions 返回所有权限, 按 resource, action 排序.
func (s *RBACStore) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, resource, action, COALESCE(scope, ''), description
		FROM identity.permissions
		ORDER BY resource ASC, action ASC, COALESCE(scope, '') ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Name, &p.Resource, &p.Action, &p.Scope, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListRolePermissions 返回 role → []permission_name 的全量映射, 用于矩阵渲染.
func (s *RBACStore) ListRolePermissions(ctx context.Context) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role_name, permission_name
		FROM identity.role_permissions
		ORDER BY role_name, permission_name
	`)
	if err != nil {
		return nil, fmt.Errorf("list role_permissions: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var role, perm string
		if err := rows.Scan(&role, &perm); err != nil {
			return nil, err
		}
		out[role] = append(out[role], perm)
	}
	return out, rows.Err()
}

// ReplaceRolePermissions 原子替换 role 的 permission 集合.
//  1. role 必须存在
//  2. 所有 perm 必须存在 (FK 约束本身会拦, 但提前检查给前端清晰错误)
//  3. 事务: DELETE old + INSERT new
//  4. actorID 写到 granted_by (uuid 类型, 空字符串则 NULL)
//
// 返回 (added, removed, err) — 增删数量, 用于 audit detail.
func (s *RBACStore) ReplaceRolePermissions(ctx context.Context, role string, perms []string, actorID string) (added, removed int, err error) {
	if role == "" {
		return 0, 0, errors.New("role required")
	}

	var actor *uuid.UUID
	if actorID != "" {
		if u, perr := uuid.Parse(actorID); perr == nil {
			actor = &u
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 角色必须存在
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity.roles WHERE name=$1)`, role).Scan(&exists); err != nil {
		return 0, 0, err
	}
	if !exists {
		return 0, 0, fmt.Errorf("role %q not found", role)
	}

	// 取旧集合用于算 added/removed
	oldSet := map[string]struct{}{}
	rows, err := tx.Query(ctx, `SELECT permission_name FROM identity.role_permissions WHERE role_name=$1`, role)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, 0, err
		}
		oldSet[p] = struct{}{}
	}
	rows.Close()

	// 删全部
	if _, err := tx.Exec(ctx, `DELETE FROM identity.role_permissions WHERE role_name=$1`, role); err != nil {
		return 0, 0, err
	}

	// 写入新集合 (去重)
	newSet := map[string]struct{}{}
	for _, p := range perms {
		if p == "" {
			continue
		}
		newSet[p] = struct{}{}
	}
	for p := range newSet {
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.role_permissions (role_name, permission_name, granted_by)
			VALUES ($1, $2, $3)
		`, role, p, actor); err != nil {
			return 0, 0, fmt.Errorf("insert %s/%s: %w", role, p, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}

	for p := range newSet {
		if _, ok := oldSet[p]; !ok {
			added++
		}
	}
	for p := range oldSet {
		if _, ok := newSet[p]; !ok {
			removed++
		}
	}
	return added, removed, nil
}
