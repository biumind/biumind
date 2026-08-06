// Persisted state for "marketplaces the user has added".
//
// Lives at ~/.biumind/plugins/marketplaces.json so it sits next to
// the marketplace cache directory. Format:
//
//	{
//	  "marketplaces": [
//	    { "name": "biumind-official",
//	      "source": "git+https://github.com/biumind/marketplace",
//	      "pinnedKey": "ed25519:base64-spki..." },
//	    { "name": "local-dev",
//	      "source": "/Users/me/work/my-marketplace" }
//	  ]
//	}
//
// PinnedKey is optional; when set, every fetch from this source
// will require the marketplace.json's .sig to verify against it.
package plugins

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/biumind/biumind/apps/cli/biu/internal/skillpack"
)

// MarketplaceEntry is one line in the known-marketplaces store. The
// `name` field is what users type after `@` in
// `biu plugin install <plugin>@<name>`.
type MarketplaceEntry struct {
	Name      string `json:"name"`
	Source    string `json:"source"`              // file path / https URL / git+... URL
	PinnedKey string `json:"pinnedKey,omitempty"` // ed25519:<base64-spki> or empty
}

// MarketplaceStore is the JSON file that tracks known marketplaces.
// Manipulated only via Load + Save (or the Add/Remove helpers below)
// so the file shape stays consistent across writes.
type MarketplaceStore struct {
	Marketplaces []MarketplaceEntry `json:"marketplaces"`
}

// ErrMarketplaceUnknown — caller asked for a marketplace by name
// that the store doesn't have.
var ErrMarketplaceUnknown = errors.New("marketplace not in known set; add it with `biu plugin marketplace add`")

// MarketplaceStorePath returns the canonical store location.
// Creates the parent directory on demand so first-time users
// don't need to mkdir.
func MarketplaceStorePath() (string, error) {
	cacheDir, err := MarketplaceCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "marketplaces.json"), nil
}

// LoadMarketplaceStore reads the store. Missing file → empty store
// (not an error — first-time users have nothing).
func LoadMarketplaceStore() (*MarketplaceStore, error) {
	path, err := MarketplaceStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MarketplaceStore{}, nil
		}
		return nil, err
	}
	var s MarketplaceStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// Save atomically writes the store back. Same temp+rename pattern
// SetPluginDisabled uses; consistent crash semantics across all
// settings-style writes in biu.
func (s *MarketplaceStore) Save() error {
	path, err := MarketplaceStorePath()
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Add registers a marketplace under a given local handle. Returns an
// error when the name already exists — callers should remove first
// then re-add to update a source URL, so accidental overwrite of a
// trusted pinned key surfaces loud.
func (s *MarketplaceStore) Add(entry MarketplaceEntry) error {
	if entry.Name == "" {
		return fmt.Errorf("marketplace name required")
	}
	if !marketplaceNameRE.MatchString(entry.Name) {
		return fmt.Errorf("invalid marketplace name %q", entry.Name)
	}
	if entry.Source == "" {
		return fmt.Errorf("marketplace source required (URL or path)")
	}
	for _, m := range s.Marketplaces {
		if m.Name == entry.Name {
			return fmt.Errorf("marketplace %q already registered (remove it first)", entry.Name)
		}
	}
	s.Marketplaces = append(s.Marketplaces, entry)
	return nil
}

// Remove drops a marketplace by name. Returns ErrMarketplaceUnknown
// when not present so the CLI can give a typo-friendly error.
func (s *MarketplaceStore) Remove(name string) error {
	out := s.Marketplaces[:0]
	found := false
	for _, m := range s.Marketplaces {
		if m.Name == name {
			found = true
			continue
		}
		out = append(out, m)
	}
	if !found {
		return ErrMarketplaceUnknown
	}
	s.Marketplaces = out
	return nil
}

// Lookup returns an entry by name + the parsed pinned key (or nil
// when unsigned). Pulled apart so callers don't have to re-parse
// the key on every fetch.
func (s *MarketplaceStore) Lookup(name string) (*MarketplaceEntry, ed25519.PublicKey, error) {
	for i := range s.Marketplaces {
		if s.Marketplaces[i].Name == name {
			pub, err := parsePinnedKey(s.Marketplaces[i].PinnedKey)
			if err != nil {
				return &s.Marketplaces[i], nil, err
			}
			return &s.Marketplaces[i], pub, nil
		}
	}
	return nil, nil, ErrMarketplaceUnknown
}

// ParsePinnedKey decodes a "ed25519:<base64-spki>" string. Empty
// string → nil key (unsigned). Malformed → error.
//
// The format mirrors GPG / SSH key-fingerprint conventions: a
// scheme prefix so future formats (e.g. "ed25519-ph:...") can
// coexist without breaking existing pins.
func ParsePinnedKey(s string) (ed25519.PublicKey, error) { return parsePinnedKey(s) }

func parsePinnedKey(s string) (ed25519.PublicKey, error) {
	if s == "" {
		return nil, nil
	}
	const prefix = "ed25519:"
	if len(s) <= len(prefix) || s[:len(prefix)] != prefix {
		return nil, fmt.Errorf("pinned key must start with %q", prefix)
	}
	b64 := s[len(prefix):]
	pem, err := pemFromBase64(b64)
	if err != nil {
		return nil, fmt.Errorf("decode pinned key: %w", err)
	}
	return skillpack.ParsePublicKey(pem)
}

// pemFromBase64 wraps a base64 SPKI blob in a PUBLIC KEY PEM block
// so we can reuse skillpack.ParsePublicKey (which expects PEM)
// without exposing a base64-direct entry point. The 64-char line
// wrapping matches `openssl pkey -pubout` output so a user pasting
// in their own key works either via "ed25519:<one-line-b64>" or
// the multi-line PEM equivalent if we add it later.
func pemFromBase64(b64 string) ([]byte, error) {
	const header = "-----BEGIN PUBLIC KEY-----\n"
	const footer = "-----END PUBLIC KEY-----\n"
	var buf []byte
	buf = append(buf, header...)
	for i := 0; i < len(b64); i += 64 {
		end := i + 64
		if end > len(b64) {
			end = len(b64)
		}
		buf = append(buf, b64[i:end]...)
		buf = append(buf, '\n')
	}
	buf = append(buf, footer...)
	return buf, nil
}
