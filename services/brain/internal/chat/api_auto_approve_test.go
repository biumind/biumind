package chat

import "testing"

func TestNormalizeAutoApprove(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"auto", "auto", true},
		{"manual", "manual", true},
		// plan 原样落库：00013 起 DB check 约束含 'plan'，agentplane 的
		// permission_mode 映射把它升级为 "plan"（runtime ModePlan 只读）。
		{"plan", "plan", true},
		// 老词汇 / 非法值一律拒绝（客户端读时已映射，不该再上行）。
		{"whitelist", "", false},
		{"", "", false},
		{"bypass", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeAutoApprove(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("normalizeAutoApprove(%q) = %q,%v; want %q,%v",
				c.in, got, ok, c.want, c.ok)
		}
	}
}
