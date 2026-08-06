package skillpack

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// keypair returns a parsed (priv, pub) pair freshly generated. The
// PEM round-trip is exercised by TestGenerateAndRoundTripSign; for
// other tests we cut straight to parsed keys to keep the body short.
func keypair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	privPEM, pubPEM, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(pubPEM)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func TestGenerateAndRoundTripSign(t *testing.T) {
	priv, pub := keypair(t)
	archive := []byte("PK\x03\x04 fake .biuskill bytes")
	sig, err := Sign(archive, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig == "" {
		t.Fatal("empty sig")
	}
	if err := Verify(archive, sig, pub); err != nil {
		t.Errorf("happy path verify failed: %v", err)
	}
}

func TestVerify_DetectsTampering(t *testing.T) {
	priv, pub := keypair(t)
	original := []byte("legit content")
	sig, _ := Sign(original, priv)

	tampered := []byte("legit conten!") // one-byte change
	err := Verify(tampered, sig, pub)
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("want ErrBadSignature for tampered bytes; got %v", err)
	}
}

func TestVerify_WrongKey(t *testing.T) {
	priv1, _ := keypair(t)
	_, pub2 := keypair(t)
	archive := []byte("body")
	sig, _ := Sign(archive, priv1)
	if err := Verify(archive, sig, pub2); !errors.Is(err, ErrBadSignature) {
		t.Errorf("want ErrBadSignature for wrong pubkey; got %v", err)
	}
}

func TestVerify_RejectsBadBase64(t *testing.T) {
	_, pub := keypair(t)
	if err := Verify([]byte("x"), "not-base64-!!!", pub); err == nil {
		t.Error("expected base64 decode error")
	}
}

func TestVerify_RejectsWrongLengthSig(t *testing.T) {
	_, pub := keypair(t)
	// Valid base64, wrong byte length.
	if err := Verify([]byte("x"), "AAA=", pub); err == nil {
		t.Error("expected wrong-length signature error")
	}
}

func TestParsePrivateKey_ErrorsOnNonPEM(t *testing.T) {
	if _, err := ParsePrivateKey([]byte("not a pem")); err == nil {
		t.Error("want error for non-PEM input")
	}
}

func TestParsePublicKey_RejectsPrivateBlock(t *testing.T) {
	privPEM, _, _ := GenerateKeypair()
	if _, err := ParsePublicKey(privPEM); err == nil {
		t.Error("expected mismatched-block-type error")
	}
}

// Determinism contract — two Pack(dir) calls produce identical bytes,
// so signing a tree once and verifying after re-pack still works.
// This is the SAFETY check that protects sig stability if the layout
// filter or zip header rules ever change.
func TestSign_StableAcrossRepacks(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "SKILL.md", `---
name: stable
description: stable
---
body`)
	writeFile(t, src, "scripts/x.sh", "echo")

	a, err := Pack(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Pack(src)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := keypair(t)
	sig, err := Sign(a.Bytes, priv)
	if err != nil {
		t.Fatal(err)
	}
	// Same source → same archive bytes → existing signature still
	// verifies. If this ever breaks, the determinism contract in
	// PS4.2 was silently weakened.
	if err := Verify(b.Bytes, sig, pub); err != nil {
		t.Fatalf("re-pack should preserve sig validity: %v", err)
	}
}
