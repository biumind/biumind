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
		// plan 落库为 manual：DB check 约束（migration 00038）不含 'plan'，
		// agentplane permission_mode 映射只升级 'auto'，天然兼容。
		{"plan", "manual", true},
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
