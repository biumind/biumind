package auth

import (
	"context"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	signer := NewSigner("test-secret-very-long-string-for-hmac", "https://identity.test", "biumind-api", 5*time.Minute)
	verifier := NewVerifier("test-secret-very-long-string-for-hmac", "https://identity.test", "biumind-api")

	tok, err := signer.Sign(&Claims{
		UserID:  "u-123",
		OrgID:   "o-1",
		Roles:   []string{"member"},
		Plan:    "pro",
		TeamIDs: []string{"t-a", "t-b"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	c, err := verifier.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.UserID != "u-123" || c.OrgID != "o-1" {
		t.Errorf("claims wrong: %+v", c)
	}
	if len(c.Roles) != 1 || c.Roles[0] != "member" {
		t.Errorf("roles wrong: %v", c.Roles)
	}
	if c.Plan != "pro" {
		t.Errorf("plan wrong: %q", c.Plan)
	}
}

// 老 token 不含 plan 字段 — verify 后 c.Plan 应为空字符串 (兼容).
func TestSignVerifyEmptyPlan(t *testing.T) {
	signer := NewSigner("test-secret-very-long-string-for-hmac", "iss", "aud", time.Minute)
	verifier := NewVerifier("test-secret-very-long-string-for-hmac", "iss", "aud")
	tok, _ := signer.Sign(&Claims{UserID: "u-1"})
	c, err := verifier.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.Plan != "" {
		t.Errorf("expected empty plan, got %q", c.Plan)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	signer := NewSigner("aaaa-aaaa-aaaa-aaaa-aaaa-aaaa-aaaa", "iss", "aud", time.Minute)
	verifier := NewVerifier("bbbb-bbbb-bbbb-bbbb-bbbb-bbbb-bbbb", "iss", "aud")
	tok, _ := signer.Sign(&Claims{UserID: "u-1"})
	if _, err := verifier.Verify(tok); err == nil {
		t.Fatal("expected error on wrong secret")
	}
}

func TestVerifyExpired(t *testing.T) {
	signer := NewSigner("aaaa-aaaa-aaaa-aaaa-aaaa-aaaa-aaaa", "iss", "aud", -time.Minute) // 已过期
	verifier := NewVerifier("aaaa-aaaa-aaaa-aaaa-aaaa-aaaa-aaaa", "iss", "aud")
	verifier.LeewaySec = 0
	tok, _ := signer.Sign(&Claims{UserID: "u-1"})
	if _, err := verifier.Verify(tok); err == nil {
		t.Fatal("expected error on expired token")
	}
}

func TestContextRoundTrip(t *testing.T) {
	c := &Claims{UserID: "u-1"}
	ctx := WithClaims(context.Background(), c)
	got, ok := ClaimsFrom(ctx)
	if !ok || got.UserID != "u-1" {
		t.Fatal("context round-trip failed")
	}
}

func TestStripPATFraming(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"bm_a1b2c3d4_eyJhbGc.payload.sig", "eyJhbGc.payload.sig"},
		{"eyJhbGc.payload.sig", "eyJhbGc.payload.sig"}, // plain JWT untouched
		{"bm_only_no_real_jwt_marker", "no_real_jwt_marker"},
		{"bmprefix_no_leading", "bmprefix_no_leading"}, // missing bm_ prefix → unchanged
		{"bm_short", "bm_short"},                       // no second _ → unchanged
		{"", ""},
		{"bm_", "bm_"}, // truncated → unchanged
	}
	for _, c := range cases {
		got := StripPATFraming(c.in)
		if got != c.want {
			t.Errorf("StripPATFraming(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
