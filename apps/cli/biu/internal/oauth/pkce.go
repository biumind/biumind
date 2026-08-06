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
	"errors"
	"fmt"
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

// Expired reports whether ExpiresAt has passed (with a 30s leeway so
// we proactively refresh before the upstream rejects).
func (t Tokens) Expired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(30 * time.Second).After(t.ExpiresAt)
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
// Store is a thin wrapper that delegates to a swappable `backend`.
// Two backends ship out of the box (see backend.go):
//
//   - fileBackend     — JSON at ~/.biu/auth.json, 0600 perms.
//   - keychainBackend — OS credential store (macOS Keychain on darwin,
//                       Secret Service via `secret-tool` on linux).
//
// `NewStore("")` keeps the historical contract: file-backed at
// $HOME/.biu/auth.json. Use `Open()` for the auto-pick (keychain →
// file) recommended for end-user CLI commands.

// Store wraps a tokenBackend with a write mutex and the public CRUD
// surface the rest of biu uses.
type Store struct {
	mu      sync.Mutex
	backend tokenBackend
}

// NewStore returns a file-backed store rooted at $HOME/.biu/auth.json
// (or the supplied override). Kept for back-compat with callers /
// tests that need a deterministic file path; new callers should use
// Open(), which auto-picks keychain when available.
func NewStore(override string) (*Store, error) {
	b, err := newFileBackend(override)
	if err != nil {
		return nil, err
	}
	return &Store{backend: b}, nil
}

// Open returns a Store using the most secure backend the host
// supports: OS keychain when reachable, falling back to the file
// store. Use this for `biu auth login` / `status` / `logout`.
//
// `fileOverride` is forwarded to the file backend if keychain isn't
// available — empty string keeps the default path.
func Open(fileOverride string) (*Store, error) {
	if b, ok := openKeychainBackend(); ok {
		return &Store{backend: b}, nil
	}
	return NewStore(fileOverride)
}

// Backend returns the active backend's stable name (e.g. "file",
// "darwin-keychain", "secret-service"). Surfaced by `biu doctor`
// and `biu auth status`.
func (s *Store) Backend() string { return s.backend.Name() }

// Path returns a human-readable identifier for where tokens live.
// For the file backend this is the absolute filesystem path; for
// keychain backends it's a label like "macOS Keychain (biu)".
func (s *Store) Path() string { return s.backend.Path() }

// Load returns the saved tokens. Missing/empty entry yields (zero,
// nil) so callers can branch on `tokens.AccessToken == ""`.
func (s *Store) Load() (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.Load()
}

// Save writes tokens to the active backend.
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

// Migrate copies tokens from `src` into this store and removes them
// from `src` on success. Returns (false, nil) when src has no tokens
// to migrate. The transactional shape (write target → delete source)
// guarantees no token is ever lost — at worst it lives in both
// places, which `biu auth migrate` recovers from on next run.
func (s *Store) Migrate(src *Store) (migrated bool, err error) {
	if src == nil {
		return false, errors.New("oauth: nil source store")
	}
	t, err := src.Load()
	if err != nil {
		return false, fmt.Errorf("oauth: read source: %w", err)
	}
	if t.AccessToken == "" {
		return false, nil
	}
	if err := s.Save(t); err != nil {
		return false, fmt.Errorf("oauth: write target: %w", err)
	}
	if err := src.Delete(); err != nil {
		// Target already has the token — leak the source rather than
		// fail loud, but tell the caller so they can log a warning.
		return true, fmt.Errorf("oauth: source delete: %w", err)
	}
	return true, nil
}

// fileStorePath returns the canonical $HOME/.biu/auth.json — used by
// `biu auth migrate` to talk to the legacy file directly even when
// the keychain backend is the default.
func fileStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "auth.json"), nil
}

// FileStorePath is the exported variant for cmd/biu use.
func FileStorePath() (string, error) { return fileStorePath() }
