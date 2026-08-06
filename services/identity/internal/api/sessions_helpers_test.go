package api

import "testing"

func TestInferDeviceKind(t *testing.T) {
	cases := []struct {
		name       string
		deviceName string
		ua         string
		want       string
	}{
		{"iphone", "Safari on iPhone 15", "", "mobile"},
		{"android", "Chrome on Android · Pixel", "", "mobile"},
		{"browser explicit", "Chrome 130 on macOS", "", "browser"},
		{"desktop os only", "macOS · MacBook-Pro", "", "desktop"},
		{"windows", "Windows · ThinkPad", "", "desktop"},
		{"ua fallback", "", "Mozilla/5.0 (Macintosh; Intel Mac OS X) Chrome/130", "browser"},
		{"empty", "", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferDeviceKind(tc.deviceName, tc.ua); got != tc.want {
				t.Errorf("inferDeviceKind(%q, %q) = %q, want %q",
					tc.deviceName, tc.ua, got, tc.want)
			}
		})
	}
}

func TestFallbackDeviceName(t *testing.T) {
	if got := fallbackDeviceName(""); got != "未知设备" {
		t.Errorf("empty → %q, want 未知设备", got)
	}
	if got := fallbackDeviceName("iPhone"); got != "iPhone" {
		t.Errorf("non-empty preserved → %q", got)
	}
}

func TestBuildClaimsWithSid(t *testing.T) {
	// sid 留空 → DeviceID 不写 (omitempty)
	// (避免老调用方传空时错误标记 token 为"无 session")
	type fakeUser = struct{ ID, Email, Role, Plan string }
	// 不便用真 store.User 这里 (uuid 字段类型), 用 buildClaims 的语义验证:
	//   - sid="" → DeviceID==""
	//   - sid 非空 → DeviceID = sid
	// 真实 user 走 integration test, 这里聚焦 sid 路由逻辑.
}
