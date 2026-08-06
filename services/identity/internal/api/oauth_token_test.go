package api

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCE_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if !verifyPKCE(verifier, challenge, "S256") {
		t.Fatal("expected S256 verify pass")
	}
	if verifyPKCE(verifier+"x", challenge, "S256") {
		t.Fatal("modified verifier must fail")
	}
	if verifyPKCE(verifier, challenge, "plain") {
		t.Fatal("plain method must always fail (rejected at /authorize layer)")
	}
	if verifyPKCE(verifier, challenge[:len(challenge)-1], "S256") {
		t.Fatal("length-mismatched challenge must fail (no panic)")
	}
}
