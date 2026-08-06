// Package-internal helpers for ed25519 signature verification of
// .biuskill installs (P2-#10). The CLI side signs (apps/cli/biu/
// internal/skillpack/sign.go); the runtime install handler calls
// VerifyArchive before persisting the row.
//
// Trust model:
//   - Empty trust store → permissive: signatures ignored, any zip
//     installs (legacy / dev behaviour). Backward compatible.
//   - Non-empty trust store → strict: every zip install MUST carry a
//     signature_b64 that verifies against at least one trusted pubkey.
//
// Trust store loading:
//   - Inline PEM in env BIUMIND_SKILL_TRUSTED_PUBKEY_PEM (single key).
//   - Directory of *.pub files via BIUMIND_SKILL_TRUSTED_PUBKEY_DIR
//     (one file = one trusted publisher, filename = stable id used in
//     audit logs).
//
// URL / inline install paths bypass verification — those don't
// produce a signed archive shape today. When marketplace adapters
// land they'll need extending.

package installer

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/skillsign"
)

// TrustStore — collection of trusted publisher keys. Each entry has
// a stable id (filename or "inline") for audit logs.
type TrustStore struct {
	keys []trustedKey
}

type trustedKey struct {
	id  string
	key ed25519.PublicKey
}

// IsEmpty reports whether the store has zero trusted keys. Callers
// switch behaviour: empty = permissive, non-empty = strict.
func (s *TrustStore) IsEmpty() bool {
	return s == nil || len(s.keys) == 0
}

// Verify runs signature against every trusted key. Returns the
// matching publisher id on success, or ErrUntrusted when no key
// matched. Bad-base64 / wrong-size signature errors propagate from
// skillsign.Verify so the caller sees the specific reason.
func (s *TrustStore) Verify(archive []byte, sigB64 string) (string, error) {
	if s.IsEmpty() {
		return "", errors.New("trust store empty (caller should not have called Verify)")
	}
	for _, k := range s.keys {
		err := skillsign.Verify(archive, sigB64, k.key)
		if err == nil {
			return k.id, nil
		}
		// Distinguish "this key didn't match" from "signature is
		// structurally broken" — bail early on the latter.
		if !errors.Is(err, skillsign.ErrBadSignature) {
			return "", err
		}
	}
	return "", ErrUntrusted
}

// ErrUntrusted — no trusted publisher key matched the signature.
// Caller should reject the install with 403 and tell the user to
// either (a) verify the upstream source, or (b) ask an admin to
// add the publisher key to the trust store.
var ErrUntrusted = errors.New("no trusted publisher key matched the signature")

// LoadTrustStoreFromEnv reads trust store config from process env.
// Returns an empty store when no env var is set — the caller decides
// whether to treat that as permissive or fatal.
//
// Precedence:
//  1. BIUMIND_SKILL_TRUSTED_PUBKEY_DIR — directory of *.pub files
//  2. BIUMIND_SKILL_TRUSTED_PUBKEY_PEM — single inline PEM block
//
// When both are set, the directory wins (production); inline is for
// dev convenience only.
func LoadTrustStoreFromEnv() (*TrustStore, error) {
	if dir := os.Getenv("BIUMIND_SKILL_TRUSTED_PUBKEY_DIR"); dir != "" {
		return loadDir(dir)
	}
	if pemStr := os.Getenv("BIUMIND_SKILL_TRUSTED_PUBKEY_PEM"); pemStr != "" {
		return loadInline(pemStr)
	}
	return &TrustStore{}, nil
}

func loadDir(dir string) (*TrustStore, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("trust store dir %q: %w", dir, err)
	}
	var keys []trustedKey
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".pub") {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		pub, err := skillsign.ParsePublicKey(body)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		// id = filename without .pub — stable, human-friendly for
		// audit logs ("install verified by publisher: alice").
		keys = append(keys, trustedKey{
			id:  strings.TrimSuffix(name, ".pub"),
			key: pub,
		})
	}
	return &TrustStore{keys: keys}, nil
}

func loadInline(pemStr string) (*TrustStore, error) {
	pub, err := skillsign.ParsePublicKey([]byte(pemStr))
	if err != nil {
		return nil, fmt.Errorf("inline trust store key: %w", err)
	}
	return &TrustStore{
		keys: []trustedKey{{id: "inline", key: pub}},
	}, nil
}

// VerifyZipInstall is the install-handler-facing entry point. Encapsulates
// the trust-store-aware policy:
//
//   - empty store + no signature  → ok (permissive)
//   - empty store + signature     → ok (signature ignored; we have no way
//                                    to check it, but we don't reject
//                                    benign-looking input)
//   - non-empty store + no sig    → ErrSignatureRequired
//   - non-empty store + sig OK    → ok, returns publisher id
//   - non-empty store + sig fail  → ErrUntrusted (or structural err)
//
// The caller logs the publisher id alongside skill creation for audit.
func VerifyZipInstall(store *TrustStore, archive []byte, sigB64 string) (publisher string, err error) {
	if store.IsEmpty() {
		return "", nil
	}
	if strings.TrimSpace(sigB64) == "" {
		return "", ErrSignatureRequired
	}
	return store.Verify(archive, sigB64)
}

// ErrSignatureRequired — server is in strict mode and the install
// request didn't carry a signature_b64.
var ErrSignatureRequired = errors.New("signature_b64 required (server runs in strict mode)")
