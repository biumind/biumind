package otel

import "testing"

// stripURLScheme 是 OTLP endpoint env 写法跟 SDK WithEndpoint 期望形态
// 之间的 shim. 遗留 bug: 写 "http://otel-collector:4317" → "too many colons
// in address" 把日志刷爆. 这里覆盖各种写法保证不再误退.
func TestStripURLScheme(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"otel-collector:4317", "otel-collector:4317"},        // 期望形态, 不动
		{"http://otel-collector:4317", "otel-collector:4317"}, // 历史 bug 形态
		{"https://otel.prod.example.com:4317", "otel.prod.example.com:4317"},
		{"http://otel-collector:4317/", "otel-collector:4317"},    // 尾 / 也清掉
		{"  http://otel-collector:4317  ", "otel-collector:4317"}, // 周边空白
		{"localhost:4317", "localhost:4317"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := stripURLScheme(tc.in)
			if got != tc.want {
				t.Errorf("stripURLScheme(%q): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
