package oauth

import (
	"testing"
)

// C1（方案 D4）：relay endpoint → OAuth 端点推导矩阵。
func TestDeriveEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		relay    string
		wantOK   bool
		wantBase string
	}{
		{"default prod", "https://api.biu.app", true, "https://api.biu.app"},
		{"trailing slash", "https://api.biu.app/", true, "https://api.biu.app"},
		{"path dropped", "http://localhost:7001/v1", true, "http://localhost:7001"},
		{"empty", "", false, ""},
		{"no scheme", "api.biu.app", false, ""},
		{"non-http scheme", "ftp://api.biu.app", false, ""},
		{"no host", "https://", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DeriveEndpoints(c.relay)
			if !c.wantOK {
				if err == nil {
					t.Fatalf("relay %q: expected error, got %+v", c.relay, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("relay %q: %v", c.relay, err)
			}
			if got.AuthorizeURL != c.wantBase+"/oauth/authorize" ||
				got.TokenURL != c.wantBase+"/oauth/token" ||
				got.RevokeURL != c.wantBase+"/oauth/revoke" {
				t.Errorf("relay %q: got %+v", c.relay, got)
			}
			if got.ClientID != DefaultClientID {
				t.Errorf("client_id default: got %q want %q", got.ClientID, DefaultClientID)
			}
		})
	}
}

// C1 优先级：[auth] TOML > BIU_OAUTH_* env > 推导值。
func TestConfigFromSourcesPriority(t *testing.T) {
	t.Setenv("BIU_OAUTH_AUTHORIZE_URL", "")
	t.Setenv("BIU_OAUTH_TOKEN_URL", "")
	t.Setenv("BIU_OAUTH_REVOKE_URL", "")
	t.Setenv("BIU_OAUTH_CLIENT_ID", "")
	t.Setenv("BIU_OAUTH_MANUAL_REDIRECT", "")

	// 纯推导：TOML/env 全空。
	got, err := ConfigFromSources(Config{}, "https://api.biu.app")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorizeURL != "https://api.biu.app/oauth/authorize" ||
		got.TokenURL != "https://api.biu.app/oauth/token" ||
		got.RevokeURL != "https://api.biu.app/oauth/revoke" ||
		got.ClientID != "biu-cli" {
		t.Errorf("derive fallback wrong: %+v", got)
	}
	if len(got.Scopes) == 0 {
		t.Errorf("default scopes missing")
	}

	// env 盖推导。
	t.Setenv("BIU_OAUTH_TOKEN_URL", "https://env.example/token")
	got, err = ConfigFromSources(Config{}, "https://api.biu.app")
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenURL != "https://env.example/token" {
		t.Errorf("env should beat derived: %+v", got)
	}

	// TOML 盖 env。
	got, err = ConfigFromSources(Config{TokenURL: "https://toml.example/token"}, "https://api.biu.app")
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenURL != "https://toml.example/token" {
		t.Errorf("toml should beat env: %+v", got)
	}

	// 推导失败且 TOML/env 未补全 → 报错。
	t.Setenv("BIU_OAUTH_TOKEN_URL", "")
	if _, err := ConfigFromSources(Config{}, "notaurl"); err == nil {
		t.Errorf("invalid endpoint with no [auth] should error")
	}
	// 推导失败但 TOML 补全 → 正常（自部署 escape hatch）。
	got, err = ConfigFromSources(Config{
		AuthorizeURL: "https://id.example/authorize",
		TokenURL:     "https://id.example/token",
	}, "notaurl")
	if err != nil {
		t.Fatalf("explicit [auth] should not need derivation: %v", err)
	}
	if got.ClientID != DefaultClientID {
		t.Errorf("client_id default: %+v", got)
	}
}
