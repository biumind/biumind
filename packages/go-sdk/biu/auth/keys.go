// Key management helpers — creation / persistence / kid derivation /
// JWKS HTTP handler. Used exclusively by Identity (and tests).

package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

const defaultRSABits = 2048

// LoadOrCreateRSAKey reads an RSA private key from `path`, creating a
// fresh 2048-bit key + writing it (PKCS#1 PEM, mode 0600) if the file
// is missing. Empty `path` → in-memory ephemeral key (every restart
// rotates keys; fine for tests, not for prod).
func LoadOrCreateRSAKey(path string) (*rsa.PrivateKey, error) {
	if path == "" {
		return rsa.GenerateKey(rand.Reader, defaultRSABits)
	}
	if data, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(data)
		if blk == nil {
			return nil, fmt.Errorf("auth: %s: not a PEM file", path)
		}
		key, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
		if err != nil {
			// Try PKCS#8 as a fallback — `openssl genpkey` defaults to it.
			any, perr := x509.ParsePKCS8PrivateKey(blk.Bytes)
			if perr != nil {
				return nil, fmt.Errorf("auth: parse %s: %w", path, err)
			}
			rsaKey, ok := any.(*rsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("auth: %s: not an RSA key", path)
			}
			return rsaKey, nil
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("auth: read %s: %w", path, err)
	}

	// Generate + persist.
	key, err := rsa.GenerateKey(rand.Reader, defaultRSABits)
	if err != nil {
		return nil, fmt.Errorf("auth: generate: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("auth: mkdir: %w", err)
	}
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		return nil, fmt.Errorf("auth: write %s: %w", path, err)
	}
	return key, nil
}

// DeriveKid returns a stable 16-hex-char id derived from
// SHA-256(SubjectPublicKeyInfo). Same key → same kid across restarts.
func DeriveKid(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:8])
}

// JWKSHandler — http.Handler that serves the configured signer's
// public key set at any path it's mounted on. Cache headers default to
// 5 minutes.
func JWKSHandler(s *Signer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set := s.PublicJWKs()
		if set == nil {
			http.Error(w, "no JWKS — signer is in HS256 mode", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(set)
	}
}
