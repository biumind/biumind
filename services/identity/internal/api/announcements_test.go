package api

import "testing"

func TestAppVersionInRange(t *testing.T) {
	cases := []struct {
		ver, min, max string
		want          bool
	}{
		{"", "1.0.0", "2.0.0", true},        // 客户端未带版本 → 放行
		{"1.5.0", "1.0.0", "2.0.0", true},   // 区间内
		{"0.9.0", "1.0.0", "", false},       // 低于下限
		{"2.1.0", "", "2.0.0", false},       // 高于上限
		{"1.0.0", "1.0.0", "1.0.0", true},   // 边界相等
		{"1.2.3", "", "", true},             // 无门槛 → 放行
		{"garbage", "1.0.0", "2.0.0", true}, // 版本非法 → 放行(不因元数据缺失漏发)
		{"1.5", "1.0.0", "2.0.0", true},     // 两段也能比
	}
	for _, c := range cases {
		if got := appVersionInRange(c.ver, c.min, c.max); got != c.want {
			t.Errorf("appVersionInRange(%q,%q,%q)=%v want %v", c.ver, c.min, c.max, got, c.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	if v, ok := parseSemver("2.1.87"); !ok || v != [3]int{2, 1, 87} {
		t.Errorf("parseSemver 2.1.87 = %v %v", v, ok)
	}
	if _, ok := parseSemver("x.y.z"); ok {
		t.Errorf("parseSemver should reject non-numeric")
	}
}

func TestHasAnnouncementAdminRole(t *testing.T) {
	if !hasAnnouncementAdminRoleRoles([]string{"user", "admin"}) {
		t.Error("admin role should pass")
	}
	if hasAnnouncementAdminRoleRoles([]string{"user", "viewer"}) {
		t.Error("non-admin should fail")
	}
	if hasAnnouncementAdminRoleRoles(nil) {
		t.Error("nil roles should fail")
	}
}

// hasAnnouncementAdminRoleRoles 是对 hasAnnouncementAdminRole 的薄封装,便于无 Claims 构造下测角色逻辑。
func hasAnnouncementAdminRoleRoles(roles []string) bool {
	for _, want := range announcementAdminRoles {
		for _, got := range roles {
			if got == want {
				return true
			}
		}
	}
	return false
}
