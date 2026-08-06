package keys

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func newKek(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	kek := newKek(t)
	env, err := NewEnvelope(kek)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("sk-ant-api03-very-secret-key-1234567890")
	ek, err := env.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(ek.Ciphertext, plain) {
		t.Fatal("ciphertext == plaintext (encryption broken)")
	}
	out, err := env.Decrypt(ek)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("got %q want %q", out, plain)
	}
}

func TestRotateKEK(t *testing.T) {
	oldKek := newKek(t)
	newKek := newKek(t)
	envOld, _ := NewEnvelope(oldKek)
	envNew, _ := NewEnvelope(newKek)

	plain := []byte("rotate-me")
	ek, _ := envOld.Encrypt(plain)

	// Decrypt with old, re-wrap with new
	dek, err := gcmOpen(oldKek, ek.WrapIV, ek.WrappedDEK)
	if err != nil {
		t.Fatal(err)
	}
	newWrapIV := make([]byte, ivSize)
	io.ReadFull(rand.Reader, newWrapIV)
	rewrapped, err := gcmSeal(newKek, newWrapIV, dek)
	if err != nil {
		t.Fatal(err)
	}
	ek.WrappedDEK = rewrapped
	ek.WrapIV = newWrapIV

	// Now decrypt with new envelope
	out, err := envNew.Decrypt(ek)
	if err != nil {
		t.Fatalf("decrypt with rotated KEK: %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("rotation mismatch")
	}
}

func TestHintLast4(t *testing.T) {
	if HintLast4("abcdefgh") != "efgh" {
		t.Fail()
	}
	if HintLast4("ab") != "ab" {
		t.Fail()
	}
}

func TestBadKEKSize(t *testing.T) {
	if _, err := NewEnvelope(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte KEK")
	}
}
