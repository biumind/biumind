package installer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/skillsign"
)

// keypair returns a fresh keypair + the same key parsed back through
// skillsign for use as a "trusted publisher" entry.
func keypair(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()
	priv, pub, err := skillsign.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func sigOver(t *testing.T, archive, privPEM []byte) string {
	t.Helper()
	priv, err := skillsign.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := skillsign.Sign(archive, priv)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestEmptyTrustStore_IsPermissive(t *testing.T) {
	store := &TrustStore{}
	if !store.IsEmpty() {
		t.Fatal("zero-value TrustStore should be empty")
	}
	// VerifyZipInstall with empty store + no sig → ok (permissive).
	pub, err := VerifyZipInstall(store, []byte("anything"), "")
	if err != nil {
		t.Errorf("empty store + no sig should pass; got %v", err)
	}
	if pub != "" {
		t.Errorf("empty store should report empty publisher; got %q", pub)
	}
	// Empty store also accepts a (presumably bogus) signature — we
	// have no way to check it. This is intentional permissive
	// behaviour; flipping to strict requires populating the store.
	_, err = VerifyZipInstall(store, []byte("a"), "AAA=")
	if err != nil {
		t.Errorf("empty store should ignore signatures; got %v", err)
	}
}

func TestStrictStore_RequiresSignature(t *testing.T) {
	priv, pub := keypair(t)
	pk, _ := skillsign.ParsePublicKey(pub)
	store := &TrustStore{keys: []trustedKey{{id: "alice", key: pk}}}

	archive := []byte("PK\x03\x04 fake archive bytes")

	// No signature in strict mode → ErrSignatureRequired.
	_, err := VerifyZipInstall(store, archive, "")
	if !errors.Is(err, ErrSignatureRequired) {
		t.Errorf("want ErrSignatureRequired; got %v", err)
	}

	// Valid signature → returns publisher id.
	sig := sigOver(t, archive, priv)
	id, err := VerifyZipInstall(store, archive, sig)
	if err != nil {
		t.Fatalf("strict + valid sig should pass; got %v", err)
	}
	if id != "alice" {
		t.Errorf("publisher = %q, want alice", id)
	}

	// Tampered archive → ErrUntrusted (no key matches the bytes).
	tampered := append([]byte{}, archive...)
	tampered[0] = 'X'
	_, err = VerifyZipInstall(store, tampered, sig)
	if !errors.Is(err, ErrUntrusted) {
		t.Errorf("tampered → want ErrUntrusted; got %v", err)
	}

	// Bogus base64 → structural error (not ErrUntrusted).
	_, err = VerifyZipInstall(store, archive, "not-base64-!!!")
	if errors.Is(err, ErrUntrusted) {
		t.Errorf("structural decode error should not be reported as untrusted")
	}
	if err == nil {
		t.Errorf("bad base64 must error; got nil")
	}
}

func TestMultipleTrustedKeys_AnyMatchSucceeds(t *testing.T) {
	// Trust store with two publishers; sig from the second key still
	// verifies (loop continues past the first mismatch).
	_, pub1 := keypair(t)
	priv2, pub2 := keypair(t)
	pk1, _ := skillsign.ParsePublicKey(pub1)
	pk2, _ := skillsign.ParsePublicKey(pub2)
	store := &TrustStore{keys: []trustedKey{
		{id: "alice", key: pk1},
		{id: "bob", key: pk2},
	}}

	archive := []byte("contents")
	sig := sigOver(t, archive, priv2)

	id, err := VerifyZipInstall(store, archive, sig)
	if err != nil {
		t.Fatal(err)
	}
	if id != "bob" {
		t.Errorf("expected bob's signature to match; got %q", id)
	}
}

func TestLoadTrustStoreFromEnv_DirPrecedence(t *testing.T) {
	dir := t.TempDir()
	_, pubA := keypair(t)
	_, pubB := keypair(t)
	if err := os.WriteFile(filepath.Join(dir, "alice.pub"), pubA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bob.pub"), pubB, 0o644); err != nil {
		t.Fatal(err)
	}
	// Throw an extra non-.pub file in to confirm we ignore it.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_DIR", dir)
	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_PEM", "should be ignored when dir set")

	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if store.IsEmpty() {
		t.Fatal("two .pub files should populate the store")
	}
	if got := len(store.keys); got != 2 {
		t.Errorf("store size = %d, want 2", got)
	}
	ids := []string{store.keys[0].id, store.keys[1].id}
	hasAlice, hasBob := false, false
	for _, id := range ids {
		if id == "alice" {
			hasAlice = true
		}
		if id == "bob" {
			hasBob = true
		}
	}
	if !hasAlice || !hasBob {
		t.Errorf("expected alice + bob; got %v", ids)
	}
}

func TestLoadTrustStoreFromEnv_InlineFallback(t *testing.T) {
	_, pub := keypair(t)
	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_DIR", "")
	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_PEM", string(pub))

	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if store.IsEmpty() {
		t.Fatal("inline PEM should populate the store")
	}
	if store.keys[0].id != "inline" {
		t.Errorf("inline key id = %q, want inline", store.keys[0].id)
	}
}

func TestLoadTrustStoreFromEnv_BothEmptyYieldsEmptyStore(t *testing.T) {
	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_DIR", "")
	t.Setenv("BIUMIND_SKILL_TRUSTED_PUBKEY_PEM", "")
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !store.IsEmpty() {
		t.Errorf("empty env should produce empty store")
	}
}
