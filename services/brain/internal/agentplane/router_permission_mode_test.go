// permissionModeFromAutoApprove 映射单测（P1 遗留 2）：'plan' 落库值
// 升级为 "plan"，云端 task 以 ModePlan 只读运行；'whitelist' / 'manual'
// / 未知值不升级（worker 端保持现状行为）。

package agentplane

import "testing"

func TestPermissionModeFromAutoApprove(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"auto", "auto"},
		{"plan", "plan"},
		{"manual", ""},
		{"whitelist", ""}, // 老词汇（读时已映射,不该落库）,不升级
		{"", ""},
		{"bypass", ""},
	}
	for _, c := range cases {
		if got := permissionModeFromAutoApprove(c.in); got != c.want {
			t.Errorf("permissionModeFromAutoApprove(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
