package token

import (
	"strings"
	"testing"
)

func TestGenerateUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		full, _, err := Generate(RefreshTokenPrefix)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(full, RefreshTokenPrefix) {
			t.Fatalf("missing prefix: %s", full)
		}
		if seen[full] {
			t.Fatalf("collision: %s", full)
		}
		seen[full] = true
	}
}

func TestVerifyHash(t *testing.T) {
	full, hash, err := Generate(VirtualKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyHash(full, hash) {
		t.Fatal("VerifyHash returned false")
	}
	if VerifyHash("bk-live-different", hash) {
		t.Fatal("VerifyHash returned true for wrong token")
	}
}

func TestDisplayPrefix(t *testing.T) {
	full := "bk-live-abcdefghijklmnopqrstuvwxyz"
	if got := DisplayPrefix(full); got != "bk-live-abcdef" {
		t.Errorf("DisplayPrefix = %q", got)
	}
	if DisplayPrefix("short") != "" {
		t.Fail()
	}
}
