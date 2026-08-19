package api

import "testing"

// matchRedirectURI 矩阵 — RFC 8252 §7.3 loopback 端口放宽:
//   - loopback (127.0.0.1 / [::1] / localhost) 同 scheme+host+path+query,
//     仅端口不同 → 放行 (CLI 用 OS 随机端口, 注册时无法预知)
//   - 127.0.0.1 与 localhost 视为不同 host (严格, 不互放)
//   - 非 loopback 保持精确匹配 (回归)
func TestMatchRedirectURI(t *testing.T) {
	registered := []string{
		"http://127.0.0.1/callback",
		"http://localhost/callback",
		"http://[::1]/callback",
		"https://app.example.com/cb",
	}
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// 精确匹配 (现状回归)
		{"non-loopback exact", "https://app.example.com/cb", true},
		{"loopback exact no port", "http://127.0.0.1/callback", true},
		// loopback 端口放宽
		{"loopback 127.0.0.1 random port", "http://127.0.0.1:55321/callback", true},
		{"loopback localhost random port", "http://localhost:8080/callback", true},
		{"loopback ::1 random port", "http://[::1]:4200/callback", true},
		// 拒绝: path 不同
		{"loopback different path", "http://127.0.0.1:55321/other", false},
		{"loopback missing path", "http://127.0.0.1:55321", false},
		// 拒绝: host 不同 (127.0.0.1 vs localhost 严格区分)
		{"127.0.0.1 vs localhost", "http://localhost:55321/callbackx", false},
		{"loopback not-registered host", "http://127.0.0.2:55321/callback", false},
		// 拒绝: scheme 不同
		{"loopback https vs http", "https://127.0.0.1:443/callback", false},
		// 拒绝: 非 loopback 端口不同
		{"non-loopback different port", "https://app.example.com:8443/cb", false},
		{"non-loopback different path", "https://app.example.com/other", false},
		// 拒绝: 伪 loopback 域名
		{"fake loopback domain", "http://127.0.0.1.evil.com/callback", false},
		{"userinfo trick", "http://127.0.0.1@evil.com/callback", false},
		// 垃圾输入
		{"empty", "", false},
		{"not a url", "callback", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchRedirectURI(registered, tc.in); got != tc.want {
				t.Errorf("matchRedirectURI(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// query 参与比较 — "仅端口不同" 意味着 query 也必须一致.
func TestMatchRedirectURI_Query(t *testing.T) {
	registered := []string{"http://127.0.0.1/callback?fixed=1"}
	if !matchRedirectURI(registered, "http://127.0.0.1:9999/callback?fixed=1") {
		t.Error("same query, different port should match")
	}
	if matchRedirectURI(registered, "http://127.0.0.1:9999/callback?fixed=2") {
		t.Error("different query must not match")
	}
	if matchRedirectURI(registered, "http://127.0.0.1:9999/callback") {
		t.Error("missing query must not match")
	}
}

// 注册的 loopback URI 本身带端口时: 精确命中仍放行, 非精确则按端口放宽走.
func TestMatchRedirectURI_RegisteredWithPort(t *testing.T) {
	registered := []string{"http://127.0.0.1:1234/callback"}
	if !matchRedirectURI(registered, "http://127.0.0.1:1234/callback") {
		t.Error("exact match (with port) should pass")
	}
	if !matchRedirectURI(registered, "http://127.0.0.1:5678/callback") {
		t.Error("loopback different port should pass per RFC 8252 §7.3")
	}
}
