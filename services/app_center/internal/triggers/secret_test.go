package triggers

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerate_ProducesDistinctSecrets(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != SecretBytes {
		t.Errorf("len = %d, want %d", len(a), SecretBytes)
	}
	b, _ := Generate()
	if string(a) == string(b) {
		t.Error("two calls returned the same secret — RNG broken?")
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	secret, _ := Generate()
	body := []byte(`{"hello":"world"}`)
	sig := Sign(secret, body)
	if err := Verify(secret, body, sig); err != nil {
		t.Errorf("Verify(valid sig): %v", err)
	}
}

func TestVerify_AcceptsSha256Prefix(t *testing.T) {
	secret, _ := Generate()
	body := []byte(`x`)
	sig := "sha256=" + Sign(secret, body)
	if err := Verify(secret, body, sig); err != nil {
		t.Errorf("Verify with sha256= prefix: %v", err)
	}
}

func TestVerify_RejectsBadSig(t *testing.T) {
	secret, _ := Generate()
	body := []byte(`x`)
	cases := []string{
		"",
		"not_hex_at_all",
		"00",
		strings.Repeat("a", 64), // valid-looking hex, wrong value
	}
	for _, sig := range cases {
		if err := Verify(secret, body, sig); !errors.Is(err, ErrSignatureInvalid) {
			t.Errorf("Verify(%q) = %v, want ErrSignatureInvalid", sig, err)
		}
	}
}

func TestVerify_RejectsBodyTamper(t *testing.T) {
	secret, _ := Generate()
	sig := Sign(secret, []byte(`original`))
	if err := Verify(secret, []byte(`tampered`), sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("tampered body should fail, got %v", err)
	}
}

func TestVerify_RejectsWrongSecret(t *testing.T) {
	a, _ := Generate()
	b, _ := Generate()
	body := []byte(`x`)
	sig := Sign(a, body)
	if err := Verify(b, body, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("wrong secret should fail, got %v", err)
	}
}

func TestVerify_RejectsEmptySecret(t *testing.T) {
	if err := Verify(nil, []byte(`x`), "deadbeef"); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("empty secret should fail, got %v", err)
	}
}
