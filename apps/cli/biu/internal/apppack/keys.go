// Package apppack handles the developer-side concerns for App Center
// distribution: keypairs, .biuapp pack/verify, scaffolding new apps.
//
// This is the CLI counterpart to packages/go-sdk/biu/biuapp — the
// SDK validates manifests at runtime; this package produces them at
// dev time and signs / unpacks the distributable bundle.
package apppack

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultKeyDir is where keygen drops new keypairs. Mirrors how
// gnupg / ssh keep keys under the user's HOME — predictable + the
// only path biu app pack looks at by default.
func DefaultKeyDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "keys"), nil
}

// KeyPair carries an ed25519 keypair + the disk paths it was loaded
// from / written to. Public keys are short enough we just hold the
// bytes; private keys live encrypted only on disk for v2.0 (see
// issue tracker — for v2.0.0 they're plain PEM with 0600 perms,
// matching how the existing skill keystore behaves).
type KeyPair struct {
	Pub         ed25519.PublicKey
	Priv        ed25519.PrivateKey
	PrivPath    string
	PubPath     string
	PublisherID string // "ed25519:<base64-pub>" — used in manifest.author.public_key
}

// Generate creates a fresh keypair under name (e.g. "publisher").
// Files land at <keyDir>/<name>.ed25519 (priv) and .ed25519.pub.
// Refuses to overwrite existing files — accidental keygen on the
// publishing key would invalidate every prior signature.
func Generate(keyDir, name string) (*KeyPair, error) {
	if name == "" {
		name = "publisher"
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("apppack: mkdir keys: %w", err)
	}
	privPath := filepath.Join(keyDir, name+".ed25519")
	pubPath := privPath + ".pub"
	for _, p := range []string{privPath, pubPath} {
		if _, err := os.Stat(p); err == nil {
			return nil, fmt.Errorf("apppack: %s already exists — refusing to overwrite", p)
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("apppack: gen key: %w", err)
	}
	if err := os.WriteFile(privPath, pemEncode("ED25519 PRIVATE KEY", priv), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pubPath, pemEncode("ED25519 PUBLIC KEY", pub), 0o644); err != nil {
		return nil, err
	}
	return &KeyPair{
		Pub: pub, Priv: priv,
		PrivPath: privPath, PubPath: pubPath,
		PublisherID: "ed25519:" + base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// LoadKeyPair reads a keypair previously written by Generate.
// privPath empty → the matching .pub-only entry (for verification);
// most callers want both for signing.
func LoadKeyPair(privPath string) (*KeyPair, error) {
	if privPath == "" {
		return nil, errors.New("apppack: priv path required")
	}
	priv, err := loadPEM(privPath, "ED25519 PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("apppack: bad priv key length %d (want %d)",
			len(priv), ed25519.PrivateKeySize)
	}
	pubPath := privPath + ".pub"
	var pub ed25519.PublicKey
	if pubBytes, err := loadPEM(pubPath, "ED25519 PUBLIC KEY"); err == nil {
		pub = ed25519.PublicKey(pubBytes)
	} else {
		// Derive from private — every ed25519 priv contains the pub
		// in its second half, so even if the .pub got lost we can
		// still sign + show the matching publisher id.
		pub = ed25519.PrivateKey(priv).Public().(ed25519.PublicKey)
	}
	return &KeyPair{
		Pub: pub, Priv: ed25519.PrivateKey(priv),
		PrivPath: privPath, PubPath: pubPath,
		PublisherID: "ed25519:" + base64.StdEncoding.EncodeToString(pub),
	}, nil
}

func pemEncode(blockType string, bytes []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes})
}

func loadPEM(path, wantType string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("apppack: read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("apppack: %s not PEM-encoded", path)
	}
	if block.Type != wantType {
		return nil, fmt.Errorf("apppack: %s wrong block type %q (want %q)",
			path, block.Type, wantType)
	}
	return block.Bytes, nil
}
