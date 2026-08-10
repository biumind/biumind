package agentplane

import (
	"testing"
)

// ── resolveCreds(chat 去 env 化后) ─────────────────────────────
//
// chat 模式一律 model-relay PassThrough:user JWT 当 Bearer 透传,RelayURL
// 当 endpoint,useBearer 恒 true。RelayURL 空 / userBearer 空 → ok=false,
// 调用方 finalize failed (missing_bearer)。BYOK 不再由 brain 进程内解析,
// 由 relay 侧统一接住。

// PassThrough happy path:RelayURL 设 + bearer 非空 → (userJWT, RelayURL, true, true)。
func TestResolveCreds_PassThrough_UsesBearerAndRelay(t *testing.T) {
	r := &ChatRunner{
		RelayURL: "http://model-relay:7001",
		Logger:   nopLogger(),
	}
	k, e, bearer, ok := r.resolveCreds("user-jwt-xyz")
	if !ok {
		t.Fatal("expect ok=true with RelayURL + bearer")
	}
	if k != "user-jwt-xyz" {
		t.Errorf("APIKey should be user JWT; got %q", k)
	}
	if e != "http://model-relay:7001" {
		t.Errorf("Endpoint should be RelayURL; got %q", e)
	}
	if !bearer {
		t.Errorf("useBearer should be true (PassThrough); got false")
	}
}

// bearer 空 → ok=false(即使 RelayURL 配了)。生产上这意味着 router 没把
// user JWT 塞进 WorkPayload,是 bug,必须 finalize failed 而不是静默降级。
func TestResolveCreds_EmptyBearer_NotOK(t *testing.T) {
	r := &ChatRunner{
		RelayURL: "http://model-relay:7001",
		Logger:   nopLogger(),
	}
	if _, _, _, ok := r.resolveCreds(""); ok {
		t.Errorf("empty bearer should not resolve")
	}
}

// RelayURL 空(dev 未配 MODEL_RELAY_URL)→ ok=false,即使 bearer 在。
func TestResolveCreds_NoRelayURL_NotOK(t *testing.T) {
	r := &ChatRunner{Logger: nopLogger()}
	if _, _, _, ok := r.resolveCreds("user-jwt-xyz"); ok {
		t.Errorf("empty RelayURL should not resolve")
	}
}

// bearerFromAuthHeader 单独测,直接保护 createChatSession 入口的 token
// 抽取逻辑。"Bearer " 前缀大小写敏感(JWT 客户端规范都首字母大写)。
func TestBearerFromAuthHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Bearer ", ""},
		{"Bearer abc", "abc"},
		{"Bearer eyJhb.xyz.zzz", "eyJhb.xyz.zzz"},
		{"bearer abc", ""},
		{"Token abc", ""},
		{"abc", ""},
	}
	for _, c := range cases {
		if got := bearerFromAuthHeader(c.in); got != c.want {
			t.Errorf("bearerFromAuthHeader(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
