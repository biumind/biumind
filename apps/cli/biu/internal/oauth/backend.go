// Pluggable token-storage backends.
//
// `Store` (in pkce.go) holds zero filesystem knowledge — it just
// forwards Load/Save/Delete to whatever backend factory wired it up.
// Two backends ship today:
//
//   fileBackend       — JSON at ~/.biu/auth.json, 0600 perms.
//                       Always available; used as fallback.
//   keychainBackend   — OS-native credential store. Implementation
//                       lives in backend_keychain_<goos>.go.
//
// `openKeychainBackend()` is the platform abstraction point: each GOOS
// build file provides one, returning (nil, false) when the host
// keychain isn't reachable (no `security` CLI on minimal darwin
// images, no D-Bus session on a Linux server, etc.). The auto-pick
// in `Open()` then transparently falls back to file.
//
// Why shell out instead of CGO bindings:
//   - keeps CGO_ENABLED=0 for the static binary
//   - GoReleaser stays simple
//   - the platform-native CLI (`security`, `secret-tool`, `wincred`)
//     ships with the OS / desktop environment that has a keychain to
//     begin with — if it's missing, we don't have a keychain anyway.

package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// tokenBackend is the persistence contract Store delegates to.
//
// Implementations MUST be safe for the call pattern Store imposes
// (one Load/Save/Delete at a time — Store guards with its own mutex).
type tokenBackend interface {
	Load() (Tokens, error)
	Save(Tokens) error
	Delete() error
	// Path returns a human-readable "where do tokens live" string.
	// File backend → absolute path. Keychain → label like
	// "macOS Keychain (biu)" so the user knows where to revoke from.
	Path() string
	// Name returns a stable identifier reported by Store.Backend()
	// — surfaces in `biu doctor` and `biu auth status`.
	Name() string
}

// ─── File backend ────────────────────────────────────

type fileBackend struct {
	path string
}

func newFileBackend(override string) (*fileBackend, error) {
	if override != "" {
		return &fileBackend{path: override}, nil
	}
	p, err := fileStorePath()
	if err != nil {
		return nil, err
	}
	return &fileBackend{path: p}, nil
}

func (b *fileBackend) Name() string { return "file" }
func (b *fileBackend) Path() string { return b.path }

func (b *fileBackend) Load() (Tokens, error) {
	raw, err := os.ReadFile(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Tokens{}, nil
		}
		return Tokens{}, err
	}
	var t Tokens
	if err := json.Unmarshal(raw, &t); err != nil {
		return Tokens{}, fmt.Errorf("auth: parse %s: %w", b.path, err)
	}
	return t, nil
}

func (b *fileBackend) Save(t Tokens) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

func (b *fileBackend) Delete() error {
	if err := os.Remove(b.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ─── Keychain backend (shared shape) ─────────────────
//
// The platform-specific files implement openKeychainBackend() and
// return a struct satisfying tokenBackend. They share the canonical
// service / account names below to keep Migrate behaviour consistent.

const (
	// keychainService is the keychain "service" / "schema" name used
	// across platforms. Constant on purpose — `biu auth migrate` and
	// `biu auth logout` both target the same row regardless of which
	// platform wrote it.
	keychainService = "com.biumind.biu"

	// keychainAccount is the account / username field. Single-tenant
	// on purpose: one logged-in user per machine. Multi-account
	// support, when we need it, will key off provider domain.
	keychainAccount = "default"
)

// keychainTestOverride lets tests inject a fake backend without going
// through the platform CLI. Set via SetKeychainBackendForTest;
// production callers never touch it.
var keychainTestOverride func() (tokenBackend, bool)

// SetKeychainBackendForTest swaps in a stub implementation. Returns a
// restore function the test must defer-call. Not safe for concurrent
// tests — caller serialises if needed.
func SetKeychainBackendForTest(f func() (tokenBackend, bool)) func() {
	prev := keychainTestOverride
	keychainTestOverride = f
	return func() { keychainTestOverride = prev }
}

// resolveKeychainBackend is the indirection point that platform files
// call into. It honours the test override before delegating.
func resolveKeychainBackend(real func() (tokenBackend, bool)) (tokenBackend, bool) {
	if keychainTestOverride != nil {
		return keychainTestOverride()
	}
	return real()
}
