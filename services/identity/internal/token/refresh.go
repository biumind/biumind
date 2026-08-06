// Package token generates and verifies opaque refresh tokens + virtual keys.
//
// Format:
//
//	rt-live-<26 base32 chars>          refresh token
//	bk-live-<26 base32 chars>          virtual key
//
// We store only sha256 of the secret part; verification is constant-time compare.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"strings"
)

const (
	RefreshTokenPrefix = "rt-live-"
	VirtualKeyPrefix   = "bk-live-"
	secretBytes        = 16 // 128 bits, base32-encoded → 26 chars
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// Generate returns (full string to give caller, sha256 hash to store).
func Generate(prefix string) (full string, hash []byte, err error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	secret := strings.ToLower(b32.EncodeToString(raw))
	full = prefix + secret
	h := sha256.Sum256([]byte(full))
	return full, h[:], nil
}

// Hash returns sha256 of a token string (for lookup).
func Hash(full string) []byte {
	h := sha256.Sum256([]byte(full))
	return h[:]
}

// VerifyHash is constant-time compare.
func VerifyHash(full string, stored []byte) bool {
	got := Hash(full)
	return subtle.ConstantTimeCompare(got, stored) == 1
}

// Prefix returns the human-readable part for logging / UI display.
// e.g. "bk-live-abc...xyz" → "bk-live-abc..."
func DisplayPrefix(full string) string {
	if len(full) < 16 {
		return ""
	}
	return full[:14]
}

// HasPrefix is a small helper to identify token type.
func HasPrefix(full, prefix string) bool {
	return strings.HasPrefix(full, prefix)
}

var ErrBadFormat = errors.New("bad token format")
