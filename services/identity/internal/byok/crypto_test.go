package byok

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c := newTestCipher(t)
	pt := []byte("sk-ant-api03-abcdefghijklmnop")
	ct, nonce, err := c.Encrypt(pt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := c.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("round-trip mismatch: %s != %s", got, pt)
	}
}

func TestEncrypt_DistinctNonces(t *testing.T) {
	c := newTestCipher(t)
	pt := []byte("same-plaintext")
	ct1, n1, _ := c.Encrypt(pt)
	ct2, n2, _ := c.Encrypt(pt)
	if bytes.Equal(n1, n2) {
		t.Fatal("nonces should be distinct")
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("ciphertexts should differ (AEAD nonce-driven)")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	c1 := newTestCipher(t)
	pt := []byte("secret")
	ct, nonce, _ := c1.Encrypt(pt)

	otherKey := make([]byte, KeySize)
	for i := range otherKey {
		otherKey[i] = byte(0xFF - i)
	}
	c2, _ := NewCipher(otherKey)
	if _, err := c2.Decrypt(ct, nonce); err != ErrDecryptFailed {
		t.Fatalf("want ErrDecryptFailed, got %v", err)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	c := newTestCipher(t)
	ct, nonce, _ := c.Encrypt([]byte("hi"))
	ct[0] ^= 0xFF // 翻转一位
	if _, err := c.Decrypt(ct, nonce); err != ErrDecryptFailed {
		t.Fatalf("want ErrDecryptFailed, got %v", err)
	}
}

func TestNewCipher_InvalidKeySize(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err != ErrInvalidMasterKey {
		t.Fatalf("want ErrInvalidMasterKey, got %v", err)
	}
}

func TestNewCipherFromBase64(t *testing.T) {
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = 0x42
	}
	b64 := base64.StdEncoding.EncodeToString(key)
	c, err := NewCipherFromBase64(b64)
	if err != nil {
		t.Fatalf("from b64: %v", err)
	}
	pt := []byte("ping")
	ct, nonce, _ := c.Encrypt(pt)
	got, _ := c.Decrypt(ct, nonce)
	if !bytes.Equal(got, pt) {
		t.Fatalf("decoded != original")
	}
}

func TestLast4(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-1234567890ABCD", "ABCD"},
		{"abc", "abc"},
		{"abcd", "abcd"},
		{"abcde", "bcde"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Last4(c.in); got != c.want {
			t.Errorf("Last4(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
