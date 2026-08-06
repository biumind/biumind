// Package trust manages the per-directory "is this project trusted to
// run shell hooks / status-line scripts?" gate.
//
// The threat model: someone clones a malicious repo with a poisoned
// `.biumind/settings.json` (statusLine command, hook command, etc).
// Without this gate, biu would auto-execute the hooked shell on the
// first prompt — RCE. With it, biu reads the user's persistent allow-
// list at `~/.biumind/trust.json` and only fires hooks under trusted
// roots. Untrusted directories prompt the user once, then biu records
// the decision so they're not re-asked.
//
// Concurrency: every public method on Store is safe under concurrent
// calls. The on-disk file is rewritten atomically via temp-file +
// rename so a crashed Save can't half-write the JSON.

package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileName is the JSON document under HOME that persists trusted
// directories. Path stable across versions so user upgrades don't
// lose grants.
const FileName = "trust.json"

// EnvBypass, when set to a truthy value, treats every directory as
// trusted for the lifetime of the process. Used for CI / scripted
// invocations where there's no human to answer the dialog.
//
//	BIU_TRUST=1     → trust everything this run
//	BIU_TRUST=true  → same
//
// Other values fall through to the persistent gate.
const EnvBypass = "BIU_TRUST"

// Store is the persistence layer for trusted directories. Construct
// via Load; mutate via Trust / Untrust; persist via Save.
type Store struct {
	mu      sync.RWMutex
	path    string   // resolved on disk path; "" for in-memory-only stores
	trusted []string // sorted, normalised absolute paths

	// session-only grants — applied during IsTrusted lookup but
	// never persisted. Used by the REPL when the user picks "trust
	// for this session only" in the prompt dialog.
	sessionTrusted []string
}

// Load reads `~/.biumind/<FileName>`. Missing file yields an empty
// store, not an error — first-run users haven't trusted anything
// yet.
//
// `home` is the resolved $HOME. Empty home = in-memory-only Store
// (Save is a no-op).
func Load(home string) (*Store, error) {
	s := &Store{}
	if home == "" {
		return s, nil
	}
	path := filepath.Join(home, ".biumind", FileName)
	s.path = path

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh store — first Save will create the file.
			return s, nil
		}
		return nil, fmt.Errorf("trust: read %s: %w", path, err)
	}
	var doc fileShape
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("trust: parse %s: %w (delete the file to reset)", path, err)
		}
	}
	s.trusted = normaliseList(doc.Trusted)
	return s, nil
}

// fileShape is the wire format. Kept minimal so future additions
// (per-directory expiry, per-user notes) can extend the same shape
// without breaking existing files.
type fileShape struct {
	Trusted []string `json:"trusted"`
}

// IsTrusted reports whether `dir` (or any of its ancestors) is
// trusted. Honours the BIU_TRUST env bypass so CI runs aren't
// blocked. Empty `dir` returns false — there's no path to check.
func (s *Store) IsTrusted(dir string) bool {
	if envBypassed() {
		return true
	}
	if s == nil {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil || abs == "" {
		return false
	}
	abs = filepath.Clean(abs)

	s.mu.RLock()
	trusted := append([]string(nil), s.trusted...)
	session := append([]string(nil), s.sessionTrusted...)
	s.mu.RUnlock()

	for _, list := range [][]string{trusted, session} {
		for _, t := range list {
			if isAncestor(t, abs) {
				return true
			}
		}
	}
	return false
}

// Trust marks `dir` as trusted (persistent). Idempotent — re-trusting
// an already-trusted directory is a no-op. Returns the canonicalised
// path that was stored so callers can echo "trusted /Users/x/proj".
func (s *Store) Trust(dir string) (string, error) {
	abs, err := canonicalise(dir)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.trusted {
		if t == abs {
			return abs, nil
		}
	}
	s.trusted = append(s.trusted, abs)
	sort.Strings(s.trusted)
	return abs, s.saveLocked()
}

// TrustForSession adds an in-memory grant — discarded when the
// process exits. Used by the prompt dialog for users who don't
// want to persist the decision. Returns the canonical path.
func (s *Store) TrustForSession(dir string) (string, error) {
	abs, err := canonicalise(dir)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.sessionTrusted {
		if t == abs {
			return abs, nil
		}
	}
	s.sessionTrusted = append(s.sessionTrusted, abs)
	return abs, nil
}

// Untrust removes a previously-trusted directory. No error when it
// wasn't trusted — keeps the API idempotent for "make sure this
// isn't trusted" callers.
func (s *Store) Untrust(dir string) error {
	abs, err := canonicalise(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.trusted {
		if t == abs {
			s.trusted = append(s.trusted[:i], s.trusted[i+1:]...)
			return s.saveLocked()
		}
	}
	return nil
}

// List returns the persistent trusted-directory set (sorted). Does
// NOT include session grants — those are visible only via the
// IsTrusted check.
func (s *Store) List() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.trusted...)
}

// SessionList returns just the in-memory grants. Useful for the
// REPL `/trust` listing so users can see "I trusted this for the
// session" entries separately from persistent ones.
func (s *Store) SessionList() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.sessionTrusted...)
}

// saveLocked persists s.trusted to disk. Caller must hold s.mu in
// write mode. No-op when path is empty (in-memory-only stores).
//
// Atomic write: temp-file + rename so a crash mid-write doesn't
// leave a corrupt JSON document.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("trust: mkdir %s: %w", filepath.Dir(s.path), err)
	}
	doc := fileShape{Trusted: s.trusted}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("trust: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: rename %s: %w", tmp, err)
	}
	return nil
}

// canonicalise resolves a user-supplied path to an absolute, clean
// form. Empty / unresolvable paths return an error so callers can
// surface "/trust /no/such/dir" failures clearly.
func canonicalise(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("trust: empty path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("trust: %w", err)
	}
	return filepath.Clean(abs), nil
}

// isAncestor returns true when `target` is `ancestor` itself or a
// descendant of it. Both inputs must be absolute + clean.
func isAncestor(ancestor, target string) bool {
	if ancestor == target {
		return true
	}
	prefix := ancestor
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(target, prefix)
}

// normaliseList canonicalises every entry in `in` and drops dupes.
// Used by Load so a hand-edited trust.json with mixed cases / trailing
// slashes still resolves consistently.
func normaliseList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		abs, err := canonicalise(s)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

// envBypassed reports whether $BIU_TRUST is set to a truthy value.
// Restricted to "1" / "true" (case-insensitive) so a stray export
// of an empty / "0" value doesn't accidentally globally-trust.
func envBypassed() bool {
	v := strings.TrimSpace(os.Getenv(EnvBypass))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	}
	return false
}
