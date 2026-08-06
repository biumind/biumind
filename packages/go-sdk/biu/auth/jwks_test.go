package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRSARoundtripWithJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := DeriveKid(&priv.PublicKey)
	signer := NewRSASigner(priv, kid, "iss", "aud", time.Minute)

	// Identity's JWKS endpoint.
	srv := httptest.NewServer(JWKSHandler(signer))
	defer srv.Close()

	// Verifier on a different process / service.
	v := NewJWKSVerifier(srv.URL, "iss", "aud", time.Hour)

	tok, err := signer.Sign(&Claims{UserID: "u-1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.UserID != "u-1" || c.Issuer != "iss" {
		t.Errorf("claims: %+v", c)
	}
}

func TestVerifierRejectsHS256TokenWhenJWKSOnly(t *testing.T) {
	v := NewJWKSVerifier("http://nowhere.invalid", "iss", "aud", time.Hour)
	hs := NewSigner("hmac-secret-32-chars-aaaaaaaaaaaa", "iss", "aud", time.Minute)
	tok, _ := hs.Sign(&Claims{UserID: "u"})
	if _, err := v.Verify(tok); err == nil {
		t.Errorf("HS256 token must not validate against JWKS-only verifier")
	}
}

func TestVerifierRejectsRS256TokenWhenHS256Only(t *testing.T) {
	v := NewVerifier("hmac-secret-32-chars-aaaaaaaaaaaa", "iss", "aud")
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	rs := NewRSASigner(priv, DeriveKid(&priv.PublicKey), "iss", "aud", time.Minute)
	tok, _ := rs.Sign(&Claims{UserID: "u"})
	if _, err := v.Verify(tok); err == nil {
		t.Errorf("RS256 token must not validate against HS256-only verifier")
	}
}

func TestUnknownKidTriggersRefresh(t *testing.T) {
	// Two private keys, simulating a key rotation.
	keyA, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyB, _ := rsa.GenerateKey(rand.Reader, 2048)
	kidA := DeriveKid(&keyA.PublicKey)
	kidB := DeriveKid(&keyB.PublicKey)

	// Server initially serves only kidA. After we flip the toggle it
	// serves both. We expect the verifier to refresh on cache-miss
	// when it sees a kidB token.
	var serveBoth atomic.Bool
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		set := JWKS{Keys: []JWK{PublicJWK(&keyA.PublicKey, kidA)}}
		if serveBoth.Load() {
			set.Keys = append(set.Keys, PublicJWK(&keyB.PublicKey, kidB))
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	defer srv.Close()

	signerA := NewRSASigner(keyA, kidA, "iss", "aud", time.Minute)
	signerB := NewRSASigner(keyB, kidB, "iss", "aud", time.Minute)
	v := NewJWKSVerifier(srv.URL, "iss", "aud", time.Hour)

	tokA, _ := signerA.Sign(&Claims{UserID: "u-A"})
	if _, err := v.Verify(tokA); err != nil {
		t.Fatalf("A: %v", err)
	}
	first := fetches.Load()

	// Now sign with kidB and verify — should fail until the server
	// rolls forward, then succeed without restarting the verifier.
	tokB, _ := signerB.Sign(&Claims{UserID: "u-B"})
	if _, err := v.Verify(tokB); err == nil {
		t.Errorf("expected unknown-kid failure before rotation")
	}
	serveBoth.Store(true)
	if _, err := v.Verify(tokB); err != nil {
		t.Errorf("post-rotation B: %v", err)
	}
	if fetches.Load() <= first {
		t.Errorf("verifier should have re-fetched after seeing unknown kid")
	}
}

func TestJWKSHandlerNotFoundOnHS256Mode(t *testing.T) {
	hs := NewSigner("hmac-secret-32-chars-aaaaaaaaaaaa", "iss", "aud", time.Minute)
	rr := httptest.NewRecorder()
	JWKSHandler(hs)(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("HS256 mode should serve 404, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "HS256") {
		t.Errorf("body should mention HS256: %s", rr.Body.String())
	}
}

func TestLoadOrCreateRSAKeyPersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/key.pem"
	a, err := LoadOrCreateRSAKey(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b, err := LoadOrCreateRSAKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if a.N.Cmp(b.N) != 0 {
		t.Errorf("modulus changed across reload — key not persisted")
	}
}
