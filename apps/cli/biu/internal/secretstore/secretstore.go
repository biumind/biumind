// Package secretstore — durable storage for agent-plane secrets (device
// token, X25519 privkey) that prefers the OS keychain and falls back to
// a 0600 file (R6.4 / D7).
//
// Layered on internal/oskeychain (the shared keychain primitive) so it
// reuses the same `security` / `secret-tool` shell-out as the OAuth token
// store, without duplicating platform code. Each secret is one
// (service, account) entry holding an opaque string. Binary secrets
// (e.g. raw 32-byte privkey) are hex-encoded by the caller before Set.
//
// Fallback: when no OS keychain is reachable (headless server, minimal
// container), a 0600 file at the configured path is used — identical to
// the pre-R6.4 behaviour, so nothing regresses on those hosts.
package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/oskeychain"
)

// Store reads/writes one secret identified by (service, account).
type Store struct {
	service  string
	account  string
	kc       oskeychain.Keychain // nil when keychain unavailable → file
	filePath string              // 0600 fallback path
}

// Open returns a Store for (service, account). It prefers the OS
// keychain; when unavailable it uses the 0600 file at filePath. filePath
// must be set so the fallback (and back-compat reads of pre-R6.4 files)
// works on keychain-less hosts.
func Open(service, account, filePath string) *Store {
	s := &Store{service: service, account: account, filePath: filePath}
	if k, ok := oskeychain.Open(); ok {
		s.kc = k
	}
	return s
}

// Backend reports where the secret lives: keychain backend name or "file".
func (s *Store) Backend() string {
	if s.kc != nil {
		return s.kc.Name()
	}
	return "file"
}

// Path is a human-readable location for diagnostics (`biu doctor`).
func (s *Store) Path() string {
	if s.kc != nil {
		return "OS keychain (" + s.service + "/" + s.account + ")"
	}
	return s.filePath
}

// usingKeychain reports whether the keychain backend is active.
func (s *Store) usingKeychain() bool { return s.kc != nil }

// Get returns the stored value, or "" (nil error) when absent. On a
// keychain-backed store it ALSO falls back to reading a pre-R6.4 0600
// file if the keychain has no entry yet — so existing users' on-disk
// secrets keep working until the next Set migrates them.
func (s *Store) Get() (string, error) {
	if s.kc != nil {
		v, found, err := s.kc.Get(s.service, s.account)
		if err != nil {
			return "", err
		}
		if found {
			return v, nil
		}
		// keychain empty → try legacy file (migration read).
		return s.readFile()
	}
	return s.readFile()
}

// Set stores the value. On a keychain-backed store it writes the keychain
// then best-effort removes any legacy 0600 file (one-time migration). On
// a file-only host it writes the 0600 file.
func (s *Store) Set(value string) error {
	if s.kc != nil {
		if err := s.kc.Set(s.service, s.account, value); err != nil {
			return err
		}
		_ = os.Remove(s.filePath) // best-effort migrate;残留无害
		return nil
	}
	return s.writeFile(value)
}

// Delete removes the secret from whichever backend holds it (and any
// legacy file).
func (s *Store) Delete() error {
	if s.kc != nil {
		_ = os.Remove(s.filePath)
		return s.kc.Delete(s.service, s.account)
	}
	if err := os.Remove(s.filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) readFile() (string, error) {
	if s.filePath == "" {
		return "", nil
	}
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Store) writeFile(value string) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.filePath, []byte(value+"\n"), 0o600)
}
