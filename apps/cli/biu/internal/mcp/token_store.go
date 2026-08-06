// Multi-tenant token persistence for MCP HTTP clients (P20.49c-#2).
//
// internal/oauth.Store is single-tenant — fits biu's own login flow
// (one Anthropic / model-relay account) but not MCP, where each server has
// its own auth context. We need:
//
//   - Per-server tokens, keyed by the canonical server URL so a user
//     who uses the same OAuth client_id against multiple resource
//     servers doesn't collide.
//   - Survival across biu restarts (otherwise every fresh `biu`
//     reopens the browser dance).
//   - 0600 file mode + atomic temp+rename so a crash mid-write can't
//     corrupt the store.
//
// Storage: a single JSON file at $HOME/.biu/mcp-tokens.json:
//
//   {
//     "version": 1,
//     "tokens": {
//       "<sha256(server URL)>": {
//         "server_name":   "github",
//         "server_url":    "https://api.githubcopilot.com/mcp",
//         "authorize_url": "https://github.com/login/oauth/authorize",
//         "access_token":  "...",
//         "refresh_token": "...",
//         "token_type":    "Bearer",
//         "expires_at":    1717689600
//       }
//     }
//   }
//
// Why a single file (vs one file per server): atomic writes are
// trivial, the typical user has 2-5 servers, and listing for `/mcp`
// diagnostics is one read. Per-file would balloon the inode count
// without measurable benefit.
//
// Keychain backing (macOS / linux libsecret) is intentionally NOT
// wired in this phase — internal/oauth.backend_keychain_* are
// single-tenant. Multi-tenant keychain storage is a follow-up.
// Plaintext-on-disk is no worse than today's cfg.headers["Authoriz-
// ation"] = "Bearer …" pattern.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// tokenStorePath is the resolved location of the JSON file. Tests
// override via SetTokenStorePathForTest. The default path is
// $HOME/.biu/mcp-tokens.json — matches biu's other dotfile layout
// (sibling to ~/.biu/config.toml, ~/.biu/sessions/, ~/.biu/usage.jsonl).
var (
	tokenStorePathMu sync.RWMutex
	tokenStorePath   string // empty → default path resolution
)

// SetTokenStorePathForTest pins the JSON file for tests. Returns a
// cleanup that restores the previous value. Always pair with defer
// to avoid bleeding state across t.Run subtests.
func SetTokenStorePathForTest(path string) func() {
	tokenStorePathMu.Lock()
	prev := tokenStorePath
	tokenStorePath = path
	tokenStorePathMu.Unlock()
	return func() {
		tokenStorePathMu.Lock()
		tokenStorePath = prev
		tokenStorePathMu.Unlock()
	}
}

// tokenStoreFile returns the resolved path. Empty home (the rare
// "user has no $HOME" case) is reported as an error so callers can
// gracefully no-op rather than panic.
func tokenStoreFile() (string, error) {
	tokenStorePathMu.RLock()
	override := tokenStorePath
	tokenStorePathMu.RUnlock()
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("mcp/tokens: no $HOME: %w", err)
	}
	return filepath.Join(home, ".biu", "mcp-tokens.json"), nil
}

// tokenStoreEntry is what we serialize to disk per server.
//
// TokenURL (P20.49c-#4) is the resolved token endpoint — via cfg or
// RFC 8414 discovery. We persist it so refresh_token grants can run
// without re-running discovery: critical because by the time tokens
// expire, the original challenge / discovery path is long gone.
type tokenStoreEntry struct {
	ServerName    string `json:"server_name"`
	ServerURL     string `json:"server_url"`
	AuthorizeURL  string `json:"authorize_url,omitempty"`
	TokenURL      string `json:"token_url,omitempty"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	TokenType     string `json:"token_type"`
	ExpiresAtUnix int64  `json:"expires_at,omitempty"`
}

// tokenStoreFile shape with versioning so we can evolve without
// silently breaking older biu installs.
type tokenStoreDoc struct {
	Version int                        `json:"version"`
	Tokens  map[string]tokenStoreEntry `json:"tokens"`
}

const currentTokenStoreVersion = 1

// tokenKey hashes the canonical server URL into a stable, opaque
// key. Why hash: lets the JSON file's keys stay short + URL-safe
// regardless of whatever weirdness the server URL has (paths,
// queries, ports). Why not server name: name is user-chosen and
// might collide across configs; URL is the canonical identifier.
func tokenKey(serverURL string) string {
	if serverURL == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(serverURL))
	return hex.EncodeToString(sum[:16]) // 32 hex chars; full collision space irrelevant for ~10 entries
}

// loadTokenStore reads and decodes the JSON file. Missing file →
// empty doc, no error (first-run case). Decode errors are surfaced
// so the caller can quarantine the bad file rather than silently
// dropping persisted tokens.
func loadTokenStore() (*tokenStoreDoc, error) {
	path, err := tokenStoreFile()
	if err != nil {
		return &tokenStoreDoc{Version: currentTokenStoreVersion, Tokens: map[string]tokenStoreEntry{}}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &tokenStoreDoc{Version: currentTokenStoreVersion, Tokens: map[string]tokenStoreEntry{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc tokenStoreDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if doc.Tokens == nil {
		doc.Tokens = map[string]tokenStoreEntry{}
	}
	return &doc, nil
}

// saveTokenStore atomically writes the doc. Uses temp+rename to
// avoid a half-written file if biu crashes / the disk fills mid-
// write.
func saveTokenStore(doc *tokenStoreDoc) error {
	if doc.Tokens == nil {
		doc.Tokens = map[string]tokenStoreEntry{}
	}
	doc.Version = currentTokenStoreVersion

	path, err := tokenStoreFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-tokens-*.json.tmp")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// LoadTokens fetches saved tokens for the given server URL. Returns
// (nil, nil) when no tokens are stored — caller treats that as "user
// hasn't authenticated yet, run the OAuth flow".
//
// For refresh-grant flows (P20.49c-#4) the caller also needs the
// token endpoint URL — use loadTokenEntry instead, which returns the
// full persisted record.
func LoadTokens(serverURL string) (*OAuthTokens, error) {
	entry, err := loadTokenEntry(serverURL)
	if err != nil || entry == nil {
		return nil, err
	}
	tt := entry.TokenType
	if tt == "" {
		tt = "Bearer"
	}
	return &OAuthTokens{
		AccessToken:   entry.AccessToken,
		RefreshToken:  entry.RefreshToken,
		TokenType:     tt,
		ExpiresAtUnix: entry.ExpiresAtUnix,
	}, nil
}

// loadTokenEntry returns the full persisted record (tokens +
// authorize/token URLs + metadata) for a server URL. Internal helper
// so HTTPClient.NewHTTP can recover the resolved TokenURL alongside
// the tokens.
func loadTokenEntry(serverURL string) (*tokenStoreEntry, error) {
	key := tokenKey(serverURL)
	if key == "" {
		return nil, nil
	}
	doc, err := loadTokenStore()
	if err != nil {
		return nil, err
	}
	entry, ok := doc.Tokens[key]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

// SaveTokens persists tokens for one server. Round-trips through
// load → mutate → save so concurrent writers (a rare race when two
// biu sessions auth simultaneously) at least overwrite atomically
// rather than half-merge.
//
// tokenURL is the resolved token endpoint (post-discovery) — saved
// so refresh-grant flows can run after the original challenge is
// gone (P20.49c-#4). Pass empty string when the caller doesn't have
// a resolved value; refresh will then be impossible until the user
// re-authenticates.
func SaveTokens(serverName, serverURL, authorizeURL, tokenURL string, t OAuthTokens) error {
	key := tokenKey(serverURL)
	if key == "" {
		return errors.New("mcp/tokens: empty server URL")
	}
	doc, err := loadTokenStore()
	if err != nil {
		// Couldn't read — start a fresh doc rather than refuse to
		// persist; the alternative is silently dropping tokens.
		doc = &tokenStoreDoc{Version: currentTokenStoreVersion,
			Tokens: map[string]tokenStoreEntry{}}
	}
	doc.Tokens[key] = tokenStoreEntry{
		ServerName:    serverName,
		ServerURL:     serverURL,
		AuthorizeURL:  authorizeURL,
		TokenURL:      tokenURL,
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		TokenType:     t.TokenType,
		ExpiresAtUnix: t.ExpiresAtUnix,
	}
	return saveTokenStore(doc)
}

// DeleteTokens removes the entry for one server. Used by `biu mcp
// logout <server>` (future) and the rare "force re-auth" UX. Idempotent
// on missing keys.
func DeleteTokens(serverURL string) error {
	key := tokenKey(serverURL)
	if key == "" {
		return errors.New("mcp/tokens: empty server URL")
	}
	doc, err := loadTokenStore()
	if err != nil {
		// Nothing to delete from — done.
		return nil
	}
	if _, ok := doc.Tokens[key]; !ok {
		return nil
	}
	delete(doc.Tokens, key)
	return saveTokenStore(doc)
}

// expiringSoon reports whether tokens are close enough to expiry
// that the caller should warn / pre-refresh. P20.49c-#2 just exposes
// the predicate; the actual refresh path lands in P20.49c-#4.
func (t OAuthTokens) expiringSoon(slack time.Duration) bool {
	if t.ExpiresAtUnix == 0 {
		return false // no expiry advertised
	}
	return time.Now().Add(slack).Unix() >= t.ExpiresAtUnix
}
