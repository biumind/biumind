// Package skillsign provides ed25519 signing + verification for
// .biuskill archives. Shared between the CLI (apps/cli/biu) which
// signs at build time and the Runtime install handler which verifies
// before persisting the archive.
//
// Wire shape:
//
//	<pack>.biuskill        — deterministic archive (PS4.2)
//	<pack>.biuskill.sig    — exactly one line: base64(ed25519_signature)
//	keys/<id>.key          — PEM-encoded ed25519 private key (PKCS#8)
//	keys/<id>.key.pub      — PEM-encoded ed25519 public key (SPKI)
//
// The signature is over the literal archive bytes — NOT over a
// canonical-form derived from them. This is only safe because PS4.2
// guarantees Pack(dir) is deterministic; if that contract weakens,
// the whole verification chain breaks. Tests for this package MUST
// re-pack on the same input and confirm bytes are identical, so the
// failure surfaces here rather than as a mysterious sig mismatch.
package skillsign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
)

// Block types — PEM standard names. We use PKCS#8 / SPKI so the same
// keyfiles open with `openssl pkey -in foo.key -text` for human
// inspection, which is invaluable when debugging key trust issues.
const (
	pemPrivateBlock = "PRIVATE KEY"
	pemPublicBlock  = "PUBLIC KEY"
)

// ErrBadSignature — verify rejected the archive. Caller should
// surface as "this skill failed verification — do not install"
// rather than "I/O error".
var ErrBadSignature = errors.New("ed25519 signature verification failed")

// GenerateKeypair returns a fresh ed25519 keypair PEM-encoded, ready
// to write to disk. Two artifacts: the private key (keep secret) and
// the public key (publish alongside the marketplace listing).
func GenerateKeypair() (privPEM, pubPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	privPEM = pem.EncodeToMemory(&pem.Block{Type: pemPrivateBlock, Bytes: privDER})
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: pemPublicBlock, Bytes: pubDER})
	return privPEM, pubPEM, nil
}

// ParsePrivateKey decodes a PEM-encoded PKCS#8 ed25519 private key.
// Friendly errors so a typo'd keyfile produces a clear message
// instead of a generic "asn1: structure error".
func ParsePrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("private key: not PEM-encoded")
	}
	if block.Type != pemPrivateBlock {
		return nil, fmt.Errorf("private key: expected %q PEM block, got %q",
			pemPrivateBlock, block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key: not ed25519 (got %T)", key)
	}
	return priv, nil
}

// ParsePublicKey decodes a PEM-encoded SPKI ed25519 public key.
func ParsePublicKey(pemBytes []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("public key: not PEM-encoded")
	}
	if block.Type != pemPublicBlock {
		return nil, fmt.Errorf("public key: expected %q PEM block, got %q",
			pemPublicBlock, block.Type)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse SPKI: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key: not ed25519 (got %T)", key)
	}
	return pub, nil
}

// Sign produces a base64-encoded ed25519 signature over archiveBytes.
// Output is single-line — no trailing newline — so a sidecar .sig
// file written verbatim round-trips through Verify cleanly.
func Sign(archiveBytes []byte, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("private key wrong size %d (want %d)",
			len(priv), ed25519.PrivateKeySize)
	}
	sig := ed25519.Sign(priv, archiveBytes)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks the base64-encoded signature against archiveBytes
// using pub. Returns ErrBadSignature on mismatch (or transport-shape
// errors with their own wrapped messages).
func Verify(archiveBytes []byte, sigB64 string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key wrong size %d (want %d)",
			len(pub), ed25519.PublicKeySize)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signature: bad base64: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature wrong size %d (want %d)",
			len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, archiveBytes, sig) {
		return ErrBadSignature
	}
	return nil
}
