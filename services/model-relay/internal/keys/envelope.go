// Package keys implements BYOK envelope encryption per §5.3 of Technical Architecture.
//
//	Plaintext  --[AES-GCM with DEK]-->  Ciphertext (stored)
//	DEK        --[AES-GCM with KEK]-->  Wrapped DEK (stored)
//	KEK        --[from KMS / env]-----> never stored
//
// To rotate KEK: re-wrap all DEKs; ciphertext untouched.
package keys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	dekSize = 32 // AES-256
	ivSize  = 12 // GCM standard
)

// EncryptedKey is what gets stored in DB.
type EncryptedKey struct {
	Ciphertext []byte // AES-GCM(plaintext, dek, iv)
	WrappedDEK []byte // AES-GCM(dek, kek, wrapIV)
	IV         []byte // for ciphertext
	WrapIV     []byte // for wrapped DEK
}

// Envelope handles encrypt / decrypt with a master KEK.
type Envelope struct {
	kek []byte // 32 bytes
}

// NewEnvelope from raw bytes.
func NewEnvelope(kek []byte) (*Envelope, error) {
	if len(kek) != 32 {
		return nil, fmt.Errorf("envelope: KEK must be 32 bytes, got %d", len(kek))
	}
	return &Envelope{kek: kek}, nil
}

// NewEnvelopeFromBase64 parses base64-encoded KEK (matches BIUMIND_MASTER_KEY env).
func NewEnvelopeFromBase64(s string) (*Envelope, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("envelope: decode KEK: %w", err)
	}
	return NewEnvelope(raw)
}

// Encrypt wraps plaintext into an EncryptedKey.
func (e *Envelope) Encrypt(plaintext []byte) (*EncryptedKey, error) {
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("envelope: gen dek: %w", err)
	}
	iv := make([]byte, ivSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	ciphertext, err := gcmSeal(dek, iv, plaintext)
	if err != nil {
		return nil, fmt.Errorf("envelope: seal plaintext: %w", err)
	}
	wrapIV := make([]byte, ivSize)
	if _, err := io.ReadFull(rand.Reader, wrapIV); err != nil {
		return nil, err
	}
	wrappedDEK, err := gcmSeal(e.kek, wrapIV, dek)
	if err != nil {
		return nil, fmt.Errorf("envelope: seal dek: %w", err)
	}
	return &EncryptedKey{
		Ciphertext: ciphertext,
		WrappedDEK: wrappedDEK,
		IV:         iv,
		WrapIV:     wrapIV,
	}, nil
}

// Decrypt unwraps an EncryptedKey back to plaintext.
func (e *Envelope) Decrypt(ek *EncryptedKey) ([]byte, error) {
	dek, err := gcmOpen(e.kek, ek.WrapIV, ek.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap dek: %w", err)
	}
	plaintext, err := gcmOpen(dek, ek.IV, ek.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("envelope: open ciphertext: %w", err)
	}
	// Best-effort wipe DEK
	for i := range dek {
		dek[i] = 0
	}
	return plaintext, nil
}

// HintLast4 returns last 4 chars of plaintext for UI display.
// (We never store this — caller computes once at create-time.)
func HintLast4(plaintext string) string {
	if len(plaintext) <= 4 {
		return plaintext
	}
	return plaintext[len(plaintext)-4:]
}

// ─── helpers ────────────────────────────────────────────

func gcmSeal(key, iv, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, iv, plaintext, nil), nil
}

func gcmOpen(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, iv, ciphertext, nil)
}
