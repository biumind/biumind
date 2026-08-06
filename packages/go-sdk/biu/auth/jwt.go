// Package auth provides JWT signing/verification + claims extraction.
// Used by Identity (issuer) and all other services (verifier).
package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the standard BiuMind JWT payload.
//
// Plan 是终端用户的订阅档位 (free/pro/team). 跟 Roles 是正交的:
//   - Roles  → 后台 RBAC, 决定能调哪些 admin 端点
//   - Plan   → 业务限额 + metrics 切片 (model-relay /v1/messages 用)
// 内部用户 (admin/ops 等) Plan 通常空, 由消费方自行兜底 (planFromClaims).
type Claims struct {
	UserID    string   `json:"sub"`
	OrgID     string   `json:"org_id,omitempty"`
	TeamIDs   []string `json:"team_ids,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Plan      string   `json:"plan,omitempty"`
	DeviceID  string   `json:"device_id,omitempty"`
	Scope     []string `json:"scope,omitempty"` // for virtual keys
	IsService bool     `json:"is_service,omitempty"`
	jwt.RegisteredClaims
}

// Verifier verifies JWTs.
//
// Two modes selected by which constructor builds it:
//
//   - NewVerifier(secret, …)  — HS256 with shared secret. Used by tests
//     and dev bootstrap. Convenient but every service shares the secret.
//
//   - NewJWKSVerifier(url, …) — RS256 via Identity's JWKS endpoint.
//     Identity holds the private key; everyone else fetches the public
//     keys, caches by `kid`, refreshes on cache miss + every TTL.
//     Production path — leaks of one service's config don't compromise
//     token signing.
//
// Verify(token) is identical between the two modes; the dispatch
// happens inside on the token's `alg` header.
type Verifier struct {
	// HS256 path
	Secret []byte

	// RS256 / JWKS path
	jwks *jwksFetcher

	Issuer    string
	Audience  string
	LeewaySec int64
}

func NewVerifier(secret, issuer, audience string) *Verifier {
	return &Verifier{
		Secret:    []byte(secret),
		Issuer:    issuer,
		Audience:  audience,
		LeewaySec: 30,
	}
}

// NewJWKSVerifier wires the verifier to Identity's JWKS endpoint.
// Pass an empty `jwksURL` to fall back to the HS256 path (useful when
// services degrade from production to dev mode at runtime).
//
// `refreshTTL` controls how often we proactively re-fetch the JWKS.
// Cache misses (unknown kid) ALWAYS trigger an immediate fetch on top
// of the periodic refresh, so key rotation is observed within one
// request rather than waiting for the next refresh tick.
func NewJWKSVerifier(jwksURL, issuer, audience string, refreshTTL time.Duration) *Verifier {
	v := &Verifier{
		Issuer:    issuer,
		Audience:  audience,
		LeewaySec: 30,
	}
	if jwksURL != "" {
		v.jwks = newJWKSFetcher(jwksURL, refreshTTL)
	}
	return v
}

// StripPATFraming removes the `bm_<prefix>_` framing from a PAT
// secret, leaving the bare JWT for verification. Idempotent: tokens
// that aren't PATs (i.e. plain JWTs) pass through unchanged. Exported
// because handlers / middleware sometimes want the cleaned token to
// e.g. inspect claims via a separate parser.
func StripPATFraming(tok string) string {
	if !strings.HasPrefix(tok, "bm_") {
		return tok
	}
	rest := tok[3:]
	idx := strings.IndexByte(rest, '_')
	if idx < 0 {
		return tok
	}
	return rest[idx+1:]
}

// Verify parses and validates a JWT. Returns claims on success.
//
// Tokens may be wrapped in `bm_<prefix>_<jwt>` PAT framing — the
// prefix is purely for visual identification in UI listings (it has
// no auth role), and gets stripped before signature verification. Other
// services don't need to know about the framing; they just hand the
// raw bearer string to Verify.
func (v *Verifier) Verify(tokenStr string) (*Claims, error) {
	tokenStr = StripPATFraming(tokenStr)
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		switch t.Method.Alg() {
		case "HS256":
			if len(v.Secret) == 0 {
				return nil, fmt.Errorf("HS256 token but no shared secret configured")
			}
			return v.Secret, nil
		case "RS256":
			if v.jwks == nil {
				return nil, fmt.Errorf("RS256 token but no JWKS configured")
			}
			kid, _ := t.Header["kid"].(string)
			return v.jwks.publicKey(kid)
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
	}, jwt.WithLeeway(time.Duration(v.LeewaySec)*time.Second))
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	if v.Issuer != "" && c.Issuer != v.Issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q want %q", c.Issuer, v.Issuer)
	}
	if v.Audience != "" {
		matched := false
		for _, a := range c.Audience {
			if a == v.Audience {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("audience mismatch")
		}
	}
	return c, nil
}

// Signer signs JWTs. Used by Identity.
//
// Two modes (mirrors Verifier):
//   - NewSigner(secret, …)         — HS256, shared secret. Tests + dev.
//   - NewRSASigner(privKey, kid, …) — RS256, asymmetric. Production.
//
// Sign(c) is identical between the two modes; only the signing method
// + secret differ.
type Signer struct {
	// HS256 path
	Secret []byte

	// RS256 path
	rsaKey *rsa.PrivateKey
	kid    string

	Issuer   string
	Audience string
	TTL      time.Duration
}

func NewSigner(secret, issuer, audience string, ttl time.Duration) *Signer {
	return &Signer{
		Secret:   []byte(secret),
		Issuer:   issuer,
		Audience: audience,
		TTL:      ttl,
	}
}

// NewRSASigner builds a production-mode signer. `kid` MUST match the
// public key that JWKS publishes — verifiers use it to pick the right
// public key. Use [DeriveKid] for a stable deterministic id.
func NewRSASigner(key *rsa.PrivateKey, kid, issuer, audience string, ttl time.Duration) *Signer {
	return &Signer{
		rsaKey:   key,
		kid:      kid,
		Issuer:   issuer,
		Audience: audience,
		TTL:      ttl,
	}
}

func (s *Signer) Sign(c *Claims) (string, error) {
	now := time.Now()
	c.Issuer = s.Issuer
	c.Audience = jwt.ClaimStrings{s.Audience}
	c.IssuedAt = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(now.Add(s.TTL))
	if s.rsaKey != nil {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = s.kid
		return tok.SignedString(s.rsaKey)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(s.Secret)
}

// SigningKid returns the `kid` this signer stamps on RS256 tokens.
// Empty when running in HS256 mode.
func (s *Signer) SigningKid() string { return s.kid }

// PublicJWKs returns the JWK set this signer can publish. HS256 mode
// returns nil — there's no public material to expose.
func (s *Signer) PublicJWKs() *JWKS {
	if s.rsaKey == nil {
		return nil
	}
	return &JWKS{Keys: []JWK{PublicJWK(&s.rsaKey.PublicKey, s.kid)}}
}

// ─── Context plumbing ─────────────────────────────────

type ctxKey struct{}

func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

// rawTokenKey + WithRawToken/RawTokenFrom stamp the original bearer
// string on the request context so handlers that need to call
// downstream services (model-relay etc.) on behalf of the caller can
// pass through the same token. Middleware sets this alongside the
// parsed Claims; missing means "this was a stateless / untrusted
// path — don't trust the absence as proof of an unauth request".
type rawTokenKey struct{}

func WithRawToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, rawTokenKey{}, token)
}

func RawTokenFrom(ctx context.Context) string {
	if v, ok := ctx.Value(rawTokenKey{}).(string); ok {
		return v
	}
	return ""
}

// MustClaims panics if no claims; use only in handlers behind auth middleware.
func MustClaims(ctx context.Context) *Claims {
	c, ok := ClaimsFrom(ctx)
	if !ok {
		panic("auth.MustClaims: no claims in context — middleware not installed?")
	}
	return c
}
