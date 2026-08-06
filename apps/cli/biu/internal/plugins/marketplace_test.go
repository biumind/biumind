package plugins

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/skillpack"
)

// ─── ParseMarketplaceBytes ────────────────────────────────────

func TestParseMarketplace_minimal(t *testing.T) {
	data := []byte(`{
		"name": "my-mp",
		"plugins": [
			{ "name": "code-review", "source": { "type": "local", "path": "./cr" } }
		]
	}`)
	mp, err := ParseMarketplaceBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Name != "my-mp" {
		t.Errorf("Name = %q", mp.Name)
	}
	if len(mp.Plugins) != 1 || mp.Plugins[0].Name != "code-review" {
		t.Errorf("plugins = %+v", mp.Plugins)
	}
}

func TestParseMarketplace_validation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // expected field in errors
	}{
		{
			name: "missing name",
			body: `{"plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`,
			want: "name",
		},
		{
			name: "no plugins",
			body: `{"name":"m"}`,
			want: "plugins",
		},
		{
			name: "duplicate plugin",
			body: `{"name":"m","plugins":[
				{"name":"x","source":{"type":"local","path":"."}},
				{"name":"x","source":{"type":"local","path":"."}}
			]}`,
			want: "duplicate",
		},
		{
			name: "missing source type",
			body: `{"name":"m","plugins":[{"name":"x","source":{}}]}`,
			want: "source.type",
		},
		{
			name: "git missing repo",
			body: `{"name":"m","plugins":[{"name":"x","source":{"type":"git"}}]}`,
			want: "source.repo",
		},
		{
			name: "url missing url",
			body: `{"name":"m","plugins":[{"name":"x","source":{"type":"url"}}]}`,
			want: "source.url",
		},
		{
			name: "unknown source type",
			body: `{"name":"m","plugins":[{"name":"x","source":{"type":"smoke"}}]}`,
			want: "source.type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMarketplaceBytes([]byte(tc.body))
			if err == nil {
				t.Fatal("want error")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %T: %v", err, err)
			}
			matched := false
			for _, f := range ve.Fields {
				if strings.Contains(f.Field+f.Message, tc.want) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("expected %q in errors, got %+v", tc.want, ve.Fields)
			}
		})
	}
}

// ─── Lookup + SplitPluginRef ──────────────────────────────────

func TestSplitPluginRef(t *testing.T) {
	cases := []struct {
		in     string
		plugin string
		market string
		ok     bool
	}{
		{"code-review@biumind", "code-review", "biumind", true},
		{"x@y", "x", "y", true},
		{"x@y_z", "x", "y_z", true},
		{"plain-name", "", "", false},
		{"@only-market", "", "", false},
		{"plugin@", "", "", false},
		{"a@b@c", "", "", false},
		{"Bad@Lower", "", "", false}, // uppercase rejected
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p, m, ok := SplitPluginRef(tc.in)
			if ok != tc.ok || p != tc.plugin || m != tc.market {
				t.Errorf("SplitPluginRef(%q) = (%q,%q,%v); want (%q,%q,%v)",
					tc.in, p, m, ok, tc.plugin, tc.market, tc.ok)
			}
		})
	}
}

// ─── FetchMarketplace: local ──────────────────────────────────

func TestFetchMarketplace_localFile(t *testing.T) {
	dir := t.TempDir()
	body := `{"name":"m","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`
	mfPath := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(mfPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mp, base, err := FetchMarketplace(mfPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Name != "m" {
		t.Errorf("Name = %q", mp.Name)
	}
	if base != dir {
		t.Errorf("base = %q, want %q", base, dir)
	}
}

// Regression: when the manifest lives under .claude-plugin/, the
// returned baseDir must be the marketplace ROOT (parent of
// .claude-plugin) so plugin source paths like "demo" resolve to
// the sibling directory, not <root>/.claude-plugin/demo.
func TestFetchMarketplace_baseDirIsMarketplaceRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"m","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`
	if err := os.WriteFile(
		filepath.Join(dir, ".claude-plugin", "marketplace.json"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	_, base, err := FetchMarketplace(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if base != dir {
		t.Errorf("baseDir = %q, want marketplace root %q", base, dir)
	}

	// Same expectation when the user passes the marketplace.json
	// path directly.
	_, baseDirect, err := FetchMarketplace(filepath.Join(dir, ".claude-plugin", "marketplace.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if baseDirect != dir {
		t.Errorf("direct-path baseDir = %q, want %q", baseDirect, dir)
	}
}

func TestFetchMarketplace_localDirPreferred(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"preferred","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`
	if err := os.WriteFile(
		filepath.Join(dir, ".claude-plugin", "marketplace.json"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "marketplace.json"),
		[]byte(`{"name":"fallback","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	mp, _, err := FetchMarketplace(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Name != "preferred" {
		t.Errorf("preferred location should win, got %q", mp.Name)
	}
}

// ─── FetchMarketplace: signature ──────────────────────────────

func TestFetchMarketplace_signedOK(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	body := []byte(`{"name":"m","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`)
	mfPath := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(mfPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := skillpack.Sign(body, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mfPath+".sig", []byte(sig), 0o644); err != nil {
		t.Fatal(err)
	}

	mp, _, err := FetchMarketplace(mfPath, pub)
	if err != nil {
		t.Fatalf("signed fetch should succeed: %v", err)
	}
	if mp.Name != "m" {
		t.Errorf("Name = %q", mp.Name)
	}
}

func TestFetchMarketplace_signedTampered(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	body := []byte(`{"name":"original","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`)
	sig, _ := skillpack.Sign(body, priv)

	// Write tampered body but keep the original signature.
	tampered := []byte(`{"name":"hijacked","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`)
	mfPath := filepath.Join(dir, "marketplace.json")
	_ = os.WriteFile(mfPath, tampered, 0o644)
	_ = os.WriteFile(mfPath+".sig", []byte(sig), 0o644)

	_, _, err := FetchMarketplace(mfPath, pub)
	if err == nil {
		t.Fatal("tampered manifest should fail signature check")
	}
	if !errors.Is(err, ErrMarketplaceSignature) {
		t.Errorf("err should wrap ErrMarketplaceSignature, got %v", err)
	}
}

func TestFetchMarketplace_signedMissingSig(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	body := []byte(`{"name":"m","plugins":[{"name":"x","source":{"type":"local","path":"."}}]}`)
	mfPath := filepath.Join(dir, "marketplace.json")
	_ = os.WriteFile(mfPath, body, 0o644)

	_, _, err := FetchMarketplace(mfPath, pub)
	if err == nil {
		t.Fatal("missing .sig should fail when pinnedKey is set")
	}
}

// ─── ResolveInstall ───────────────────────────────────────────

func TestResolveInstall_local(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "my-plugin")
	if err := os.MkdirAll(filepath.Join(pluginDir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(
		filepath.Join(pluginDir, ".claude-plugin", "plugin.json"),
		[]byte(`{"name":"my-plugin","version":"1","author":{"name":"a"}}`),
		0o644,
	)
	entry := &MarketplacePlugin{
		Name:   "my-plugin",
		Source: PluginSource{Type: "local", Path: "my-plugin"},
	}
	got, err := ResolveInstall(entry, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != pluginDir {
		t.Errorf("got %q, want %q", got, pluginDir)
	}
}

func TestResolveInstall_localAbsolute(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "abs-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := &MarketplacePlugin{
		Source: PluginSource{Type: "local", Path: pluginDir},
	}
	got, err := ResolveInstall(entry, "/unrelated/base")
	if err != nil {
		t.Fatal(err)
	}
	if got != pluginDir {
		t.Errorf("absolute path should be respected, got %q", got)
	}
}

func TestResolveInstall_localMissing(t *testing.T) {
	entry := &MarketplacePlugin{
		Source: PluginSource{Type: "local", Path: "no-such-thing"},
	}
	_, err := ResolveInstall(entry, "/tmp")
	if err == nil {
		t.Fatal("missing local source should error")
	}
}

func TestResolveInstall_urlReturnsStub(t *testing.T) {
	entry := &MarketplacePlugin{
		Source: PluginSource{Type: "url", URL: "https://example.com/plug.tar.gz"},
	}
	_, err := ResolveInstall(entry, "")
	if err == nil {
		t.Fatal("url source should error in PP7")
	}
	if !errors.Is(err, ErrMarketplaceSourceUnsupported) {
		t.Errorf("err should wrap ErrMarketplaceSourceUnsupported, got %v", err)
	}
}

// ─── MarketplaceStore: persistence ────────────────────────────

func TestMarketplaceStore_addRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := LoadMarketplaceStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Marketplaces) != 0 {
		t.Errorf("fresh store should be empty, got %v", store.Marketplaces)
	}
	if err := store.Add(MarketplaceEntry{Name: "x", Source: "/path"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := LoadMarketplaceStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Marketplaces) != 1 || again.Marketplaces[0].Name != "x" {
		t.Errorf("round-trip lost entry: %+v", again.Marketplaces)
	}
}

func TestMarketplaceStore_duplicateAddRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, _ := LoadMarketplaceStore()
	_ = store.Add(MarketplaceEntry{Name: "x", Source: "/a"})
	if err := store.Add(MarketplaceEntry{Name: "x", Source: "/b"}); err == nil {
		t.Error("duplicate add should error")
	}
}

func TestMarketplaceStore_remove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, _ := LoadMarketplaceStore()
	_ = store.Add(MarketplaceEntry{Name: "x", Source: "/a"})
	_ = store.Add(MarketplaceEntry{Name: "y", Source: "/b"})
	if err := store.Remove("x"); err != nil {
		t.Fatal(err)
	}
	if len(store.Marketplaces) != 1 || store.Marketplaces[0].Name != "y" {
		t.Errorf("remove failed: %v", store.Marketplaces)
	}
	if err := store.Remove("missing"); !errors.Is(err, ErrMarketplaceUnknown) {
		t.Errorf("want ErrMarketplaceUnknown, got %v", err)
	}
}

func TestMarketplaceStore_invalidName(t *testing.T) {
	store, _ := LoadMarketplaceStore()
	if err := store.Add(MarketplaceEntry{Name: "Bad Name!", Source: "/a"}); err == nil {
		t.Error("invalid name should error")
	}
}

// ─── ParsePinnedKey ───────────────────────────────────────────

func TestParsePinnedKey_validKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pin := "ed25519:" + base64.StdEncoding.EncodeToString(pubDER)
	got, err := ParsePinnedKey(pin)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(pub) {
		t.Error("round-tripped key doesn't match original")
	}
}

func TestParsePinnedKey_emptyReturnsNil(t *testing.T) {
	got, err := ParsePinnedKey("")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("empty pin should return nil key, got %v", got)
	}
}

func TestParsePinnedKey_missingScheme(t *testing.T) {
	if _, err := ParsePinnedKey("just-base64"); err == nil {
		t.Error("missing scheme prefix should error")
	}
}
