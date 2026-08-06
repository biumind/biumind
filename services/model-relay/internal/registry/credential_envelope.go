// CredentialVault is the high-level facade combining envelope encryption
// with CredentialRepo. Admin handlers use it to Save plaintext keys and
// to retrieve credentials for display (Safe form). The runtime path
// (resolver) uses Reveal to get the plaintext just before handing it
// to the provider adaptor.
//
// Why this lives in registry/ and not keys/: the wrapper is repo-aware
// (it knows the table layout of credentials) but envelope-agnostic — it
// shouldn't be inside the keys package because that's a pure crypto
// primitive. Putting it in registry/ keeps a clear "if you need to
// encrypt a credential, here's the one place to do it" boundary.
//
// Plaintext lifetime is intentionally short: it lives only in stack
// frames inside Vault methods. We never log it, never JSON-serialise
// it, and the admin DTO (CredentialSafe) doesn't have a field for it.

package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/biumind/biumind/services/model-relay/internal/keys"
)

// CredentialVault wires CredentialRepo + Envelope together.
type CredentialVault struct {
	repo     *CredentialRepo
	envelope *keys.Envelope
}

// NewCredentialVault constructs the facade. envelope must be non-nil;
// running model-relay without a master key is a config error caught
// at startup, not here.
func NewCredentialVault(repo *CredentialRepo, env *keys.Envelope) *CredentialVault {
	if env == nil {
		// Fail loud — a nil envelope means we'd silently store plaintext
		// or panic deep in a request handler. Better to crash on construct.
		panic("registry: NewCredentialVault requires non-nil Envelope")
	}
	return &CredentialVault{repo: repo, envelope: env}
}

// SaveInput is the writable shape for "create a credential". The
// plaintext key is what the admin pasted into the form; everything
// else is admin-supplied metadata.
type SaveInput struct {
	ProviderID     uuid.UUID
	Label          string
	Plaintext      string // the upstream API key — encrypted before insert
	BaseURL        string
	HeaderOverride map[string]string
	Status         EntityStatus
}

// Save encrypts plaintext via envelope and inserts a credentials row.
// Returns the scrubbed admin view — plaintext is dropped after this
// returns (only the key_preview survives in the row).
func (v *CredentialVault) Save(ctx context.Context, in SaveInput) (*CredentialSafe, error) {
	if strings.TrimSpace(in.Plaintext) == "" {
		return nil, fmt.Errorf("vault.save: plaintext required")
	}

	ek, err := v.envelope.Encrypt([]byte(in.Plaintext))
	if err != nil {
		return nil, fmt.Errorf("vault.save: encrypt: %w", err)
	}

	preview := buildKeyPreview(in.Plaintext)

	cred, err := v.repo.Insert(ctx, CredentialInput{
		ProviderID:     in.ProviderID,
		Label:          in.Label,
		Ciphertext:     ek.Ciphertext,
		WrappedDEK:     ek.WrappedDEK,
		IV:             ek.IV,
		WrapIV:         ek.WrapIV,
		KeyPreview:     preview,
		BaseURL:        in.BaseURL,
		HeaderOverride: in.HeaderOverride,
		Status:         in.Status,
	})
	if err != nil {
		return nil, err
	}
	safe := NewCredentialSafe(cred)
	return &safe, nil
}

// UpdateMetadata edits the non-secret fields. The encrypted key is
// untouched. Use Rotate to re-encrypt with a new plaintext.
func (v *CredentialVault) UpdateMetadata(
	ctx context.Context, id uuid.UUID,
	label, baseURL string,
	headerOverride map[string]string,
	status EntityStatus,
) (*CredentialSafe, error) {
	cred, err := v.repo.Update(ctx, id, CredentialUpdate{
		Label:          label,
		BaseURL:        baseURL,
		HeaderOverride: headerOverride,
		Status:         status,
	})
	if err != nil {
		return nil, err
	}
	safe := NewCredentialSafe(cred)
	return &safe, nil
}

// Rotate replaces the encrypted material with a new plaintext. The
// rest of the row (label / base_url / header_override / status) is
// preserved. Used by admin "粘贴新 key" flow.
func (v *CredentialVault) Rotate(ctx context.Context, id uuid.UUID, plaintext string) (*CredentialSafe, error) {
	if strings.TrimSpace(plaintext) == "" {
		return nil, fmt.Errorf("vault.rotate: plaintext required")
	}

	existing, err := v.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	ek, err := v.envelope.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("vault.rotate: encrypt: %w", err)
	}
	preview := buildKeyPreview(plaintext)

	updated, err := v.repo.Update(ctx, id, CredentialUpdate{
		Label:          existing.Label,
		BaseURL:        existing.BaseURL,
		HeaderOverride: existing.HeaderOverride,
		Status:         StatusActive, // a fresh key is presumed valid; probe will downgrade if it isn't
		Ciphertext:     ek.Ciphertext,
		WrappedDEK:     ek.WrappedDEK,
		IV:             ek.IV,
		WrapIV:         ek.WrapIV,
		KeyPreview:     preview,
	})
	if err != nil {
		return nil, err
	}
	safe := NewCredentialSafe(updated)
	return &safe, nil
}

// Reveal returns the plaintext key for runtime use. This is the ONLY
// path that decrypts; callers (resolver, health probe) hold the result
// briefly and never log/store/serialise it. Admin paths must never
// call Reveal — they only see CredentialSafe.
//
// The returned bytes are a fresh copy; the caller can wipe them after
// use without affecting cached state (cache stores ciphertext only).
func (v *CredentialVault) Reveal(ctx context.Context, id uuid.UUID) (plaintext []byte, base string, header map[string]string, err error) {
	cred, err := v.repo.Get(ctx, id)
	if err != nil {
		return nil, "", nil, err
	}
	if cred.Status != StatusActive {
		return nil, "", nil, fmt.Errorf("vault.reveal: credential %s is %s", id, cred.Status)
	}
	pt, err := v.envelope.Decrypt(&keys.EncryptedKey{
		Ciphertext: cred.Ciphertext,
		WrappedDEK: cred.WrappedDEK,
		IV:         cred.IV,
		WrapIV:     cred.WrapIV,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("vault.reveal: decrypt: %w", err)
	}
	return pt, cred.BaseURL, cred.HeaderOverride, nil
}

// RevealFromCached takes a cache-returned Credential (cipher bytes
// already in memory) and decrypts it without a DB round-trip. The
// resolver hot path uses this — Get/GetCredential in cache already
// gave us the bytes; we don't want a second SELECT just to decrypt.
func (v *CredentialVault) RevealFromCached(cred *Credential) ([]byte, error) {
	if cred == nil {
		return nil, fmt.Errorf("vault.reveal_from_cached: nil credential")
	}
	if cred.Status != StatusActive {
		return nil, fmt.Errorf("vault.reveal_from_cached: credential is %s", cred.Status)
	}
	return v.envelope.Decrypt(&keys.EncryptedKey{
		Ciphertext: cred.Ciphertext,
		WrappedDEK: cred.WrappedDEK,
		IV:         cred.IV,
		WrapIV:     cred.WrapIV,
	})
}

// RevealForProbe decrypts a credential regardless of status. ONLY the
// health probe should use this — admin "test credential" must work
// even when status='invalid' so an admin can re-test after rotating
// the upstream key. Resolver / runtime paths must continue to use
// Reveal / RevealFromCached, which fail closed.
//
// Returns the plaintext, the underlying Credential (so the caller has
// base_url / header_override), or an error. Caller must zero the
// plaintext after use.
func (v *CredentialVault) RevealForProbe(ctx context.Context, id uuid.UUID) ([]byte, *Credential, error) {
	cred, err := v.repo.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	pt, err := v.envelope.Decrypt(&keys.EncryptedKey{
		Ciphertext: cred.Ciphertext,
		WrappedDEK: cred.WrappedDEK,
		IV:         cred.IV,
		WrapIV:     cred.WrapIV,
	})
	if err != nil {
		return nil, cred, fmt.Errorf("vault.reveal_for_probe: decrypt: %w", err)
	}
	return pt, cred, nil
}

// ─── preview ──────────────────────────────────────────────────────

// buildKeyPreview produces a human-recognisable hint without exposing
// the full key. Format chosen to match common upstream conventions:
//
//   sk-1234567890abcdef    →  "sk-12...cdef"   (12 chars, 4+ellipsis+4)
//   sk-ant-very-short      →  "sk-ant...hort"
//   abc                    →  "abc"            (too short — return as-is, not sensitive)
//
// We never want to store the plain key here, but we DO want enough to
// help an admin disambiguate "OpenAI account A vs B" at a glance.
func buildKeyPreview(plaintext string) string {
	t := strings.TrimSpace(plaintext)
	if len(t) <= 8 {
		return t // not enough to obscure meaningfully
	}
	prefix := t[:5]
	last4 := keys.HintLast4(t)
	return prefix + "..." + last4
}
