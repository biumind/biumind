// Package secretstore — durable storage for agent-plane secrets (device
// token, X25519 privkey) as a plain 0600 file (R6.4 / D7).
//
// The OS-keychain backing was removed: macOS Keychain ACL prompts are
// uncontrollable across build identities, and a 0600 file under ~/.biu
// matches the threat model of the OAuth token store (internal/oauth)
// and of peer CLIs. Users upgrading from a keychain-era biu re-pair /
// re-login once; no migration shim is carried.
//
// Each secret is one opaque string in one file. Binary secrets (e.g.
// raw 32-byte privkey) are hex-encoded by the caller before Set.
package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Store reads/writes one secret in one 0600 file.
type Store struct {
	filePath string
}

// Open returns a Store persisting the secret at filePath (0600,
// created on first Set).
func Open(filePath string) *Store {
	return &Store{filePath: filePath}
}

// Backend reports where the secret lives — always "file"; kept so
// diagnostics (`biu doctor`) read the same as before.
func (s *Store) Backend() string { return "file" }

// Path is a human-readable location for diagnostics (`biu doctor`).
func (s *Store) Path() string { return s.filePath }

// Get returns the stored value, or "" (nil error) when absent.
func (s *Store) Get() (string, error) {
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

// Set stores the value in the 0600 file.
func (s *Store) Set(value string) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.filePath, []byte(value+"\n"), 0o600)
}

// Delete removes the secret file. Absent file is not an error.
func (s *Store) Delete() error {
	if err := os.Remove(s.filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
