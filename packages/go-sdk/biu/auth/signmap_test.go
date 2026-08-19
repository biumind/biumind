package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SignMap must work in RS256 (production) mode too — the pre-SignMap
// identity helper was HS256-only, which broke OAuth token exchange in
// prod ("PAT minting requires HS256 signer"). Verify RS256 output
// resolves through JWKS with the kid header.
func TestSignMapRS256VerifiesViaJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := DeriveKid(&priv.PublicKey)
	signer := NewRSASigner(priv, kid, "iss", "aud", time.Minute)

	tok, err := signer.SignMap(jwt.MapClaims{
		"sub":  "u-1",
		"iss":  "iss",
		"aud":  []string{"aud"},
		"kind": "oauth",
		"exp":  time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SignMap: %v", err)
	}

	// kid header must be present so JWKS verifiers can pick the key.
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Header["kid"] != kid {
		t.Errorf("kid header = %v, want %q", parsed.Header["kid"], kid)
	}
	if parsed.Header["alg"] != "RS256" {
		t.Errorf("alg = %v, want RS256", parsed.Header["alg"])
	}
}

// HS256 mode keeps working (dev/tests), and a keyless signer errors
// instead of producing garbage.
func TestSignMapHS256AndKeyless(t *testing.T) {
	hs := NewSigner("hmac-secret-32-chars-aaaaaaaaaaaa", "iss", "aud", time.Minute)
	tok, err := hs.SignMap(jwt.MapClaims{"sub": "u-1"})
	if err != nil {
		t.Fatalf("HS256 SignMap: %v", err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Header["alg"] != "HS256" {
		t.Errorf("alg = %v, want HS256", parsed.Header["alg"])
	}

	keyless := &Signer{}
	if _, err := keyless.SignMap(jwt.MapClaims{"sub": "u"}); err == nil {
		t.Errorf("keyless signer should error")
	}
}
