package store

import (
	"context"
	"testing"
)

// TestPromoteByEmail — 回归: 曾硬编码 role_assigned_by = 零 UUID 当 system
// actor, 但该列 FK 到 identity.users(id) 且库里无此用户, UPDATE 必违反外键
// (SQLSTATE 23503), bootstrap 提升从未成功过. 现为 NULL, 这里锁住行为.
func TestPromoteByEmail(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	u0, err := s.GetUserByID(ctx, uid)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	email := u0.Email

	// 1) 正常提升: promoted=true, role_assigned_by 必须为 NULL (无 FK 违规).
	promoted, err := s.PromoteByEmail(ctx, email, "superadmin")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !promoted {
		t.Fatal("expected promoted=true")
	}
	u, err := s.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if u.Role != "superadmin" {
		t.Fatalf("role = %q, want superadmin", u.Role)
	}
	if u.RoleAssignedBy != nil {
		t.Fatalf("role_assigned_by = %v, want NULL", u.RoleAssignedBy)
	}
	if u.RoleAssignedReason == nil || *u.RoleAssignedReason == "" {
		t.Fatal("role_assigned_reason should mark bootstrap origin")
	}

	// 2) 幂等: 已是该 role → promoted=false, 不报错.
	promoted, err = s.PromoteByEmail(ctx, email, "superadmin")
	if err != nil {
		t.Fatalf("re-promote: %v", err)
	}
	if promoted {
		t.Fatal("expected promoted=false for noop")
	}

	// 3) 不存在的邮箱 → promoted=false, 不报错.
	promoted, err = s.PromoteByEmail(ctx, "nobody-"+uid.String()[:8]+"@example.com", "superadmin")
	if err != nil {
		t.Fatalf("missing email: %v", err)
	}
	if promoted {
		t.Fatal("expected promoted=false for unknown email")
	}
}
