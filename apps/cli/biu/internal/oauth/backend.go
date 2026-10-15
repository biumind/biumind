// Token persistence backend.
//
// `Store` (in pkce.go) holds zero filesystem knowledge — it just
// forwards Load/Save/Delete to the backend wired up here. Since the
// OS-keychain backend was removed (macOS Keychain ACL prompts are
// uncontrollable across build identities; a 0600 file matches the
// threat model of every peer CLI), fileBackend is the only backend:
// JSON at ~/.biu/auth.json, 0600 perms, atomic tmp+rename writes.

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
	// Path returns a human-readable "where do tokens live" string —
	// the absolute file path.
	Path() string
	// Name returns a stable identifier reported by Store.Backend()
	// — surfaces in `biu doctor` and `biu auth status`.
	Name() string
}

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
