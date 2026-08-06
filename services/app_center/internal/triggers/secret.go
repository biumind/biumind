// Webhook secret generation + HMAC verification.
//
// Each installation that declares a webhook trigger gets a unique
// 32-byte random secret stored in app_center.installations.webhook_secret.
// Inbound webhook requests carry an HMAC-SHA256 of the body in the
// X-BiuMind-App-Signature header; the receiver re-computes and
// constant-time-compares.
//
// We expose only Generate + Verify so callers can't accidentally
// leak the comparison primitive (which would open a timing-side-
// channel for secret recovery).

package triggers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// SecretBytes is the length of a webhook secret. 32 bytes = 256 bits
// — the same width as the HMAC-SHA256 output, which is the recommended
// minimum for HMAC keying material.
const SecretBytes = 32

// Generate produces a fresh random secret suitable for storing in
// installations.webhook_secret. Uses crypto/rand so a compromised
// PRNG can't predict future secrets from past ones.
func Generate() ([]byte, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// ErrSignatureInvalid is returned by Verify when the supplied
// signature doesn't match. We don't distinguish "missing", "malformed",
// "wrong length", "wrong value" in the error — surface to attackers
// would help them probe.
var ErrSignatureInvalid = errors.New("webhook: signature invalid")

// Verify checks that signatureHex equals HMAC-SHA256(secret, body)
// using a constant-time compare. signatureHex MAY have an "sha256="
// prefix (some upstream services include it); we strip and proceed.
//
// Returns nil iff the signature is valid; otherwise ErrSignatureInvalid.
// All non-ok paths return the same sentinel so error inspection
// can't narrow the search space.
func Verify(secret, body []byte, signatureHex string) error {
	signatureHex = strings.TrimSpace(signatureHex)
	signatureHex = strings.TrimPrefix(signatureHex, "sha256=")
	signatureHex = strings.TrimPrefix(signatureHex, "SHA256=")
	if len(signatureHex) == 0 || len(secret) == 0 {
		return ErrSignatureInvalid
	}
	got, err := hex.DecodeString(signatureHex)
	if err != nil {
		return ErrSignatureInvalid
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)

	if !hmac.Equal(want, got) {
		return ErrSignatureInvalid
	}
	return nil
}

// Sign returns hex(HMAC-SHA256(secret, body)). Exposed primarily for
// tests and for upstream-aware webhook clients that prefer to send
// what we expect.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
