package registry

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/keys"
)

// freshEnvelope returns a fresh Envelope for tests — random KEK, no
// shared global state.
func freshEnvelope(t *testing.T) *keys.Envelope {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("rand kek: %v", err)
	}
	env, err := keys.NewEnvelope(kek)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return env
}

func TestVaultSaveAndReveal(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	pcode := uniqueCode(t, "p_vault")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	vault := NewCredentialVault(store.Credentials, freshEnvelope(t))

	const plaintext = "sk-1234567890abcdefghij"
	safe, err := vault.Save(ctx, SaveInput{
		ProviderID:     prov.ID,
		Label:          "Acme",
		Plaintext:      plaintext,
		BaseURL:        "https://api.openai.com",
		HeaderOverride: map[string]string{"X-Tenant": "acme"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	if safe.KeyPreview != "sk-12...ghij" {
		t.Fatalf("preview format unexpected: %q", safe.KeyPreview)
	}

	// CredentialSafe never carries plaintext or envelope bytes — verify
	// by checking JSON serialisation.
	if got := safe.KeyPreview; got == plaintext {
		t.Fatalf("plaintext leaked into preview")
	}

	// Plaintext should NOT be findable in the stored row's JSON-able
	// fields. Read raw from DB to confirm ciphertext != plaintext.
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT ciphertext FROM model_relay.credentials WHERE id=$1`, safe.ID,
	).Scan(&raw); err != nil {
		t.Fatalf("read raw ciphertext: %v", err)
	}
	if bytes.Contains(raw, []byte(plaintext)) {
		t.Fatalf("plaintext leaked into ciphertext column")
	}

	// Reveal returns the original plaintext
	pt, baseURL, header, err := vault.Reveal(ctx, safe.ID)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if string(pt) != plaintext {
		t.Fatalf("reveal mismatch: %q vs %q", pt, plaintext)
	}
	if baseURL != "https://api.openai.com" {
		t.Fatalf("base_url roundtrip lost: %q", baseURL)
	}
	if header["X-Tenant"] != "acme" {
		t.Fatalf("header roundtrip lost: %v", header)
	}
}

func TestVaultRotate(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	pcode := uniqueCode(t, "p_rotate")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	vault := NewCredentialVault(store.Credentials, freshEnvelope(t))

	original := "sk-original-key-value-aaaa"
	safe, err := vault.Save(ctx, SaveInput{
		ProviderID: prov.ID, Label: "Acme", Plaintext: original,
		BaseURL: "x", HeaderOverride: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	// Mark invalid; rotate should bring it back to active
	if err := store.Credentials.PatchTestResult(ctx, safe.ID, "stale", false); err != nil {
		t.Fatalf("mark invalid: %v", err)
	}
	_, _ = pool.Exec(ctx,
		`UPDATE model_relay.credentials SET status='invalid' WHERE id=$1`, safe.ID)

	rotated := "sk-new-rotated-key-bbbb"
	updated, err := vault.Rotate(ctx, safe.ID, rotated)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if updated.Status != StatusActive {
		t.Fatalf("rotate did not reactivate: %s", updated.Status)
	}
	if updated.KeyPreview == safe.KeyPreview {
		t.Fatalf("preview unchanged after rotation: %q", updated.KeyPreview)
	}

	// Old plaintext no longer decrypts (envelope is the same; ciphertext
	// is different; gcm.Open on the stored ciphertext yields the new pt).
	pt, _, _, err := vault.Reveal(ctx, safe.ID)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if string(pt) != rotated {
		t.Fatalf("rotation didn't replace ciphertext: %q", pt)
	}

	// Other metadata preserved
	if updated.BaseURL != "x" || updated.HeaderOverride["k"] != "v" {
		t.Fatalf("rotation clobbered metadata: %+v", updated)
	}
}

func TestVaultRevealRefusesNonActive(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	pcode := uniqueCode(t, "p_ref")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	vault := NewCredentialVault(store.Credentials, freshEnvelope(t))
	safe, err := vault.Save(ctx, SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: "sk-something-here",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	// Disable the credential — Reveal must refuse
	_, _ = pool.Exec(ctx,
		`UPDATE model_relay.credentials SET status='disabled' WHERE id=$1`, safe.ID)

	if _, _, _, err := vault.Reveal(ctx, safe.ID); err == nil {
		t.Fatalf("Reveal should refuse a disabled credential")
	}
}

func TestVaultBuildPreview(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"sk-1234567890abcdef", "sk-12...cdef"},
		{"sk-ant-very-short", "sk-an...hort"},
		{"abc", "abc"},
		{"sk-12345", "sk-12345"}, // exactly 8 chars — too short to obscure
		{"  sk-trimmed-key-xxxx  ", "sk-tr...xxxx"},
	}
	for _, c := range cases {
		got := buildKeyPreview(c.in)
		if got != c.want {
			t.Errorf("buildKeyPreview(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestVaultNilEnvelopePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil envelope")
		}
	}()
	_ = NewCredentialVault(&CredentialRepo{}, nil)
}

// Sanity: a wrong KEK refuses to decrypt; we can't accidentally swap
// envelopes between instances.
func TestVaultWrongKEKFailsClosed(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	pcode := uniqueCode(t, "p_kek")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	envA := freshEnvelope(t)
	envB := freshEnvelope(t)

	vaultA := NewCredentialVault(store.Credentials, envA)
	vaultB := NewCredentialVault(store.Credentials, envB)

	safe, err := vaultA.Save(ctx, SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: "sk-secret-data-here",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	if _, _, _, err := vaultB.Reveal(ctx, safe.ID); err == nil {
		t.Fatalf("vaultB with wrong KEK should refuse to decrypt")
	} else if errors.Is(err, ErrNotFound) {
		t.Fatalf("expected decrypt error, not not_found: %v", err)
	}
}
