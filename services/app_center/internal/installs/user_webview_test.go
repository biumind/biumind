package installs

import (
	"net/url"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

// validateURL is the front gate of the user_webview path. We don't
// exhaustively cover URL parsing (that's net/url's job); we pin the
// rejection criteria so a future refactor doesn't accidentally let
// javascript:/file:/ftp:/data: through.
func TestValidateURL_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"javascript scheme", "javascript:alert(1)"},
		{"data scheme", "data:text/html,<h1>x</h1>"},
		{"file scheme", "file:///etc/passwd"},
		{"ftp scheme", "ftp://archive.example.com/foo"},
		{"missing host", "https://"},
		{"non-fqdn host", "https://kimi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := validateURL(c.in); err == nil {
				t.Errorf("expected error for %q", c.in)
			}
		})
	}
}

func TestValidateURL_Acceptances(t *testing.T) {
	cases := []string{
		"https://kimi.moonshot.cn",
		"https://kimi.moonshot.cn/chat",
		"https://www.doubao.com/",
		"http://intranet.acme.io:8080/dash",
		"http://localhost:3000",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			u, err := validateURL(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u.Hostname() == "" {
				t.Errorf("hostname empty after parse")
			}
		})
	}
}

// deriveIdentifier must be deterministic for the same (user, host)
// pair and must not collide across users.
func TestDeriveIdentifier_DeterministicPerUser(t *testing.T) {
	a := deriveIdentifier("user-1", "kimi.moonshot.cn")
	b := deriveIdentifier("user-1", "kimi.moonshot.cn")
	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
}

func TestDeriveIdentifier_DistinctPerUser(t *testing.T) {
	a := deriveIdentifier("user-1", "kimi.moonshot.cn")
	b := deriveIdentifier("user-2", "kimi.moonshot.cn")
	if a == b {
		t.Errorf("two users should derive different identifiers")
	}
}

// All-host slug normalisation: weird hosts should not break the slug
// regex (we'd hit the validator, but defensively).
func TestDeriveIdentifier_HostSlugSafe(t *testing.T) {
	a := deriveIdentifier("u", "Foo.Bar.example.com")
	if !strings.HasPrefix(a, "webview-foo-bar-example-com-") {
		t.Errorf("unexpected slug: %s", a)
	}
}

// Synthesised manifest must validate cleanly under the standard
// validator — otherwise the install path will reject our own output.
func TestSynthesiseManifest_Validates(t *testing.T) {
	u, _ := url.Parse("https://kimi.moonshot.cn")
	id := deriveIdentifier("user-1", "kimi.moonshot.cn")
	m := synthesiseManifest(id, "Kimi", u, "")
	if err := biuapp.Validate(&m); err != nil {
		t.Fatalf("synthesised manifest failed validation: %v", err)
	}
	// Sanity: one webview view, kind=webview, permission for the host.
	if m.Kind != "webview" {
		t.Errorf("kind=%q, want webview", m.Kind)
	}
	if len(m.Views) != 1 || m.Views[0].Layout != biuapp.LayoutWebView {
		t.Errorf("expected one webview view, got %+v", m.Views)
	}
	if m.Views[0].URL != "https://kimi.moonshot.cn" {
		t.Errorf("view URL = %q", m.Views[0].URL)
	}
	if len(m.Permissions) != 1 || m.Permissions[0] != "net.outbound:kimi.moonshot.cn" {
		t.Errorf("permissions = %+v", m.Permissions)
	}
}

// Localhost is accepted for dev/intranet usage. The synthesised host
// permission must reflect that.
func TestSynthesiseManifest_LocalhostAccepted(t *testing.T) {
	u, _ := url.Parse("http://localhost:3000/")
	id := deriveIdentifier("u", "localhost")
	m := synthesiseManifest(id, "Local Dev", u, "")
	if err := biuapp.Validate(&m); err != nil {
		t.Fatalf("validate localhost manifest: %v", err)
	}
}

// 设计 §10A: user_webview 接 IconFileHash 后 manifest.Icon 应该写
// "cas:<hash>" 让客户端识别拉 brain by-sha 渲染。
func TestSynthesiseManifest_IconHashWritesCasPrefix(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	id := deriveIdentifier("user-x", "example.com")
	const sha = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	m := synthesiseManifest(id, "Example", u, sha)
	if m.Icon != "cas:"+sha {
		t.Errorf("Icon = %q, want %q", m.Icon, "cas:"+sha)
	}
	mEmpty := synthesiseManifest(id, "Example", u, "")
	if mEmpty.Icon != "" {
		t.Errorf("empty hash → Icon should be empty, got %q", mEmpty.Icon)
	}
}
