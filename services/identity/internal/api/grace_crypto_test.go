package api

import (
	"bytes"
	"testing"
)

func TestGraceCrypto_Roundtrip(t *testing.T) {
	key := DeriveGraceKey("test-material")
	pt := []byte("rt-live-abcdefghijklmnopqrstuvwxyz")

	ct, err := encryptGrace(key, pt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, pt) {
		t.Error("ciphertext contains plaintext")
	}
	got, err := decryptGrace(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Errorf("roundtrip mismatch: got %q want %q", got, pt)
	}
}

func TestGraceCrypto_WrongKeyFails(t *testing.T) {
	ct, err := encryptGrace(DeriveGraceKey("key-a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptGrace(DeriveGraceKey("key-b"), ct); err == nil {
		t.Error("decrypt with wrong key should fail")
	}
}

func TestGraceCrypto_TamperedFails(t *testing.T) {
	key := DeriveGraceKey("m")
	ct, err := encryptGrace(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ct[len(ct)-1] ^= 0xff
	if _, err := decryptGrace(key, ct); err == nil {
		t.Error("decrypt of tampered ciphertext should fail")
	}
	if _, err := decryptGrace(key, ct[:5]); err == nil {
		t.Error("decrypt of truncated ciphertext should fail")
	}
}

func TestDeriveGraceKey(t *testing.T) {
	k1 := DeriveGraceKey("x")
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32", len(k1))
	}
	if !bytes.Equal(k1, DeriveGraceKey("x")) {
		t.Error("derivation not deterministic")
	}
	if bytes.Equal(k1, DeriveGraceKey("y")) {
		t.Error("different material should derive different key")
	}
}
