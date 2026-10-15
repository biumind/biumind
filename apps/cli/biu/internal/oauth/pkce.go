// Package oauth implements a provider-agnostic OAuth 2.0 + PKCE
// client suitable for the `biu auth login` CLI flow.
//
// Implements PKCE with localhost callback, manual fallback, and
// refresh-token handling, but stays vendor-neutral: the user / config
// supplies authorize_url, token_url, client_id, scopes. BiuMind's own
// auth server, Anthropic's platform.claude.com, or any OIDC-compatible
// endpoint can drive it.
//
// PKCE generates a high-entropy code_verifier; the SHA-256 of the
// verifier (base64url encoded) becomes the code_challenge. The
// verifier never leaves the device until the token-exchange POST.
//
// Storage: tokens land in ~/.biu/auth.json with mode 0600.

package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config selects an OAuth provider.
type Config struct {
	// AuthorizeURL is the user-facing /oauth/authorize endpoint that
	// `biu auth login` will open in the browser.
	AuthorizeURL string

	// TokenURL is the server-side endpoint that exchanges code →
	// {access_token, refresh_token, expires_in}.
	TokenURL string

	// RevokeURL is the RFC 7009 revocation endpoint. Optional — only
	// `biu auth logout` uses it; empty means "skip upstream revoke".
	RevokeURL string

	// ClientID identifies this CLI to the provider.
	ClientID string

	// Scopes requested at /authorize. Joined with spaces.
	Scopes []string

	// CallbackPort is the loopback port the local HTTP listener binds
	// to (must be in the ClientID's redirect_uri whitelist on the
	// provider side). 0 = pick any free port.
	CallbackPort int

	// ManualRedirectURL is the fallback redirect for environments
	// where the browser can't reach localhost (SSH session, CI).
	// When set and Login(...) is called with manual=true, the user
	// pastes the code returned at this URL.
	ManualRedirectURL string
}

// Tokens is the on-disk credential record.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`

	// Provider records which AuthorizeURL minted these tokens — when
	// the user reconfigures we can warn that stored tokens belong to
	// a different IdP.
	Provider string `json:"provider,omitempty"`
}

// Expired reports whether ExpiresAt has passed (with a 5min leeway so
// we proactively refresh well before the upstream rejects — 对齐
// claude / pi / kimi-code 的缓冲；access token TTL 15min 量级下 30s
// 太容易在服务端先过期).
func (t Tokens) Expired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(t.ExpiresAt)
}

// Authorization returns the value to set in `Authorization:`. Empty
// string when no access token (callers fall back to API key).
func (t Tokens) Authorization() string {
	if t.AccessToken == "" {
		return ""
	}
	tokenType := t.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	return tokenType + " " + t.AccessToken
}

// ─── PKCE primitives ─────────────────────────────────

// GeneratePKCE returns a verifier + its SHA-256 code_challenge. Both
// are URL-safe base64-encoded without padding (RFC 7636).
func GeneratePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 64) // 512 bits — well above the 256-bit minimum
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// RandomState returns a cryptographically random `state` parameter
// for CSRF protection on the authorize redirect.
func RandomState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ─── Token storage ───────────────────────────────────
//
// Store is a thin wrapper that delegates to a `tokenBackend`. The
// file backend (JSON at ~/.biu/auth.json, 0600 perms — see
// backend.go) is the ONLY store: the OS-keychain backend was removed
// because macOS Keychain ACL prompts can't be controlled across build
// identities, and a 0600 file matches the threat model peer CLIs
// ship with. Users upgrading from a keychain-era biu re-login once
// (`biu auth login`); no migration shim is carried.

// Store wraps a tokenBackend with a write mutex and the public CRUD
// surface the rest of biu uses.
type Store struct {
	mu      sync.Mutex
	backend tokenBackend
}

// NewStore returns a file-backed store rooted at $HOME/.biu/auth.json
// (or the supplied override).
func NewStore(override string) (*Store, error) {
	b, err := newFileBackend(override)
	if err != nil {
		return nil, err
	}
	return &Store{backend: b}, nil
}

// Open returns the token store for `biu auth login` / `status` /
// `logout`. Identical to NewStore — kept as the named entry point so
// call sites read as "open the credential store", and `fileOverride`
// (empty = default path) keeps tests on a temp file.
func Open(fileOverride string) (*Store, error) {
	return NewStore(fileOverride)
}

// Backend returns the backend's stable name ("file"). Surfaced by
// `biu doctor` and `biu auth status`.
func (s *Store) Backend() string { return s.backend.Name() }

// Path returns the absolute path of the token file.
func (s *Store) Path() string { return s.backend.Path() }

// Load returns the saved tokens. Missing/empty entry yields (zero,
// nil) so callers can branch on `tokens.AccessToken == ""`.
func (s *Store) Load() (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.Load()
}

// Save writes tokens to the backend.
func (s *Store) Save(t Tokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.Save(t)
}

// Delete removes the saved tokens. Missing entry is not an error.
func (s *Store) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.Delete()
}

// fileStorePath returns the canonical $HOME/.biu/auth.json.
func fileStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "auth.json"), nil
}

// FileStorePath is the exported variant for cmd/biu use.
func FileStorePath() (string, error) { return fileStorePath() }
