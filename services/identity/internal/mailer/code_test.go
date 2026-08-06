package mailer

import (
	"strings"
	"testing"
)

func TestGenerateCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, h, err := GenerateCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(c) != 6 {
			t.Errorf("code len: got %d, want 6 (%q)", len(c), c)
		}
		if strings.TrimLeft(c, "0123456789") != "" {
			t.Errorf("non-digit in code: %q", c)
		}
		if len(h) != 32 {
			t.Errorf("hash len: got %d, want 32", len(h))
		}
	}
}

func TestVerifyCodeRoundtrip(t *testing.T) {
	c, h, _ := GenerateCode()
	if !VerifyCode(c, h) {
		t.Fatal("legit code rejected")
	}
	if VerifyCode("000000", h) && c != "000000" {
		t.Fatal("wrong code accepted")
	}
	// 改一个字节 — 不能匹配
	bad := append([]byte{}, h...)
	bad[0] ^= 0xff
	if VerifyCode(c, bad) {
		t.Fatal("tampered hash accepted")
	}
}

func TestHashCodeDeterministic(t *testing.T) {
	a := HashCode("123456")
	b := HashCode("123456")
	if string(a) != string(b) {
		t.Fatal("HashCode not deterministic")
	}
	c := HashCode("123457")
	if string(a) == string(c) {
		t.Fatal("collision (impossible)")
	}
}
