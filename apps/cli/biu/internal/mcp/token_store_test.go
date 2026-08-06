package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempStore points the package-level token-store path at a
// temp file for the duration of the test. Returns the file path so
// the test can inspect it directly.
func withTempStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-tokens.json")
	t.Cleanup(SetTokenStorePathForTest(path))
	return path
}

func TestTokenStore_RoundTrip(t *testing.T) {
	path := withTempStore(t)

	want := OAuthTokens{
		AccessToken:   "access-zx9k",
		RefreshToken:  "refresh-zx9k",
		TokenType:     "Bearer",
		ExpiresAtUnix: time.Now().Unix() + 3600,
	}
	if err := SaveTokens("github", "https://api.github.example/mcp", "https://auth.github.example/authz", "", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	got, err := LoadTokens("https://api.github.example/mcp")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("got nil tokens")
	}
	if got.AccessToken != want.AccessToken {
		t.Errorf("access_token mismatch: %q vs %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("refresh_token mismatch")
	}
	if got.TokenType != "Bearer" {
		t.Errorf("token_type = %q", got.TokenType)
	}
	if got.ExpiresAtUnix != want.ExpiresAtUnix {
		t.Errorf("expires_at mismatch")
	}
}

func TestTokenStore_MissingKey(t *testing.T) {
	withTempStore(t)
	got, err := LoadTokens("https://no-tokens-yet.example/mcp")
	if err != nil {
		t.Fatalf("expected nil err for missing key, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil tokens, got %+v", got)
	}
}

func TestTokenStore_MultiTenant(t *testing.T) {
	withTempStore(t)
	servers := map[string]OAuthTokens{
		"https://api.github.example/mcp": {AccessToken: "gh-token", TokenType: "Bearer"},
		"https://api.notion.example/mcp": {AccessToken: "notion-token", TokenType: "Bearer"},
		"https://api.linear.example/mcp": {AccessToken: "linear-token", TokenType: "Bearer"},
	}
	for url, t := range servers {
		if err := SaveTokens("name-"+url, url, "", "", t); err != nil {
			panic(err)
		}
	}
	for url, want := range servers {
		got, err := LoadTokens(url)
		if err != nil || got == nil {
			t.Fatalf("load %s: err=%v got=%v", url, err, got)
		}
		if got.AccessToken != want.AccessToken {
			t.Errorf("server %s: token = %q want %q", url, got.AccessToken, want.AccessToken)
		}
	}
	// Loading an unrelated URL doesn't return one of the others.
	got, _ := LoadTokens("https://nope.example/mcp")
	if got != nil {
		t.Errorf("got token for unrelated URL: %+v", got)
	}
}

func TestTokenStore_OverwriteSameServer(t *testing.T) {
	withTempStore(t)
	url := "https://api.example/mcp"
	if err := SaveTokens("ex", url, "", "", OAuthTokens{AccessToken: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveTokens("ex", url, "", "", OAuthTokens{AccessToken: "second"}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadTokens(url)
	if got == nil || got.AccessToken != "second" {
		t.Errorf("expected overwrite to second, got %+v", got)
	}
}

func TestTokenStore_Delete(t *testing.T) {
	withTempStore(t)
	url := "https://api.example/mcp"
	_ = SaveTokens("ex", url, "", "", OAuthTokens{AccessToken: "tk"})
	if err := DeleteTokens(url); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := LoadTokens(url)
	if got != nil {
		t.Errorf("token still present after delete: %+v", got)
	}
	// Deleting again is a no-op (idempotent).
	if err := DeleteTokens(url); err != nil {
		t.Errorf("delete-missing: %v", err)
	}
}

func TestTokenStore_FileMode(t *testing.T) {
	path := withTempStore(t)
	if err := SaveTokens("ex", "https://x/mcp", "", "", OAuthTokens{AccessToken: "tk"}); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %o, want 0600", stat.Mode().Perm())
	}
}

func TestTokenStore_AtomicWrite(t *testing.T) {
	path := withTempStore(t)
	// Pre-seed with a known good doc.
	if err := SaveTokens("ex", "https://x/mcp", "", "", OAuthTokens{AccessToken: "good"}); err != nil {
		t.Fatal(err)
	}
	// Confirm no leftover .tmp files in the directory after a save.
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".tmp" || (len(name) > 4 && name[len(name)-4:] == ".tmp") {
			t.Errorf("temp leftover after atomic write: %s", name)
		}
	}
}

func TestTokenStore_CorruptFile(t *testing.T) {
	path := withTempStore(t)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Load surfaces the decode error so the user can fix or delete
	// the file rather than silently losing tokens.
	if _, err := LoadTokens("https://x/mcp"); err == nil {
		t.Error("expected decode error on corrupt file, got nil")
	}
	// Save proceeds anyway — overwrites the corrupt doc with a
	// fresh one rather than refusing forever.
	if err := SaveTokens("ex", "https://x/mcp", "", "", OAuthTokens{AccessToken: "fresh"}); err != nil {
		t.Errorf("save after corrupt: %v", err)
	}
	got, err := LoadTokens("https://x/mcp")
	if err != nil || got == nil || got.AccessToken != "fresh" {
		t.Errorf("recovery failed: err=%v got=%+v", err, got)
	}
}

func TestTokenStore_VersionField(t *testing.T) {
	path := withTempStore(t)
	_ = SaveTokens("ex", "https://x/mcp", "", "", OAuthTokens{AccessToken: "tk"})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc tokenStoreDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != currentTokenStoreVersion {
		t.Errorf("version = %d, want %d", doc.Version, currentTokenStoreVersion)
	}
}

func TestTokenKey_DeterministicAndDistinct(t *testing.T) {
	a1 := tokenKey("https://api.example/mcp")
	a2 := tokenKey("https://api.example/mcp")
	if a1 != a2 {
		t.Errorf("same URL should produce same key, got %q vs %q", a1, a2)
	}
	b := tokenKey("https://api.other.example/mcp")
	if a1 == b {
		t.Error("different URLs collided on key")
	}
	if tokenKey("") != "" {
		t.Error("empty URL should produce empty key")
	}
}

func TestExpiringSoon(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name  string
		exp   int64
		slack time.Duration
		want  bool
	}{
		{"no-expiry", 0, time.Minute, false},
		{"already-expired", now - 60, time.Minute, true},
		{"expiring-within-slack", now + 30, time.Minute, true},
		{"safely-future", now + 3600, time.Minute, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := OAuthTokens{ExpiresAtUnix: tt.exp}.expiringSoon(tt.slack)
			if got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

// TestNewHTTP_LoadsPersistedTokens proves the integration with
// HTTPClient: tokens written to disk by a previous biu session are
// auto-loaded on the next NewHTTP call so the first Initialize
// goes through with Authorization already set.
func TestNewHTTP_LoadsPersistedTokens(t *testing.T) {
	withTempStore(t)
	url := "https://api.example/mcp"
	persisted := OAuthTokens{AccessToken: "persisted-zx9k", TokenType: "Bearer"}
	if err := SaveTokens("ex", url, "", "", persisted); err != nil {
		t.Fatal(err)
	}

	c := NewHTTP(HTTPConfig{Name: "ex", URL: url})
	tokens := c.auth.Tokens()
	if tokens == nil {
		t.Fatal("NewHTTP didn't load persisted tokens")
	}
	if tokens.AccessToken != persisted.AccessToken {
		t.Errorf("loaded token = %q, want %q", tokens.AccessToken, persisted.AccessToken)
	}
	if c.NeedsAuth() {
		t.Error("client should not be in needs-auth after token load")
	}
}

// TestSetOAuthTokens_Persists proves the round-trip from in-memory
// SetOAuthTokens through to the JSON file.
func TestSetOAuthTokens_Persists(t *testing.T) {
	withTempStore(t)
	url := "https://api.example/mcp"
	c := NewHTTP(HTTPConfig{Name: "ex", URL: url})

	tk := OAuthTokens{AccessToken: "fresh-zx9k", TokenType: "Bearer"}
	c.SetOAuthTokens(tk, "")

	// Confirm in-memory.
	if got := c.auth.Tokens(); got == nil || got.AccessToken != tk.AccessToken {
		t.Errorf("in-memory token = %+v", got)
	}
	// Confirm persisted: a fresh client picks them up.
	c2 := NewHTTP(HTTPConfig{Name: "ex", URL: url})
	if got := c2.auth.Tokens(); got == nil || got.AccessToken != tk.AccessToken {
		t.Errorf("persisted token not picked up by fresh client: %+v", got)
	}
}
