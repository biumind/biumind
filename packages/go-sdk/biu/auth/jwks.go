// JWKS — JSON Web Key Set fetcher + RSA signer.
//
// The platform's production auth path:
//   * Identity owns an RSA private key, signs tokens RS256 with `kid`.
//   * Identity exposes /.well-known/jwks.json — public keys keyed by kid.
//   * Every other service runs a [Verifier] with [NewJWKSVerifier] that
//     fetches that JWKS once at startup, caches by kid, and refreshes
//     either on cache-miss or every refreshTTL (whichever is sooner).
//
// We support multi-key responses so Identity can roll keys: emit the new
// `kid` for ~12h while signing with the old one, then cut over. Verifiers
// always pick by `kid` — both keys are valid simultaneously during the
// rollover window.

package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWK is one JSON Web Key (RSA-only for our purposes).
type JWK struct {
	Kty string `json:"kty"`           // "RSA"
	Kid string `json:"kid"`           // key id
	Alg string `json:"alg,omitempty"` // "RS256"
	Use string `json:"use,omitempty"` // "sig"
	N   string `json:"n"`             // base64url modulus
	E   string `json:"e"`             // base64url exponent
}

// JWKS is the wire format Identity serves at /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// ─── Fetcher (verifier side) ─────────────────────────────

type jwksFetcher struct {
	url        string
	refreshTTL time.Duration
	httpClient *http.Client

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey // kid → key
	fetchedAt time.Time
}

func newJWKSFetcher(url string, refreshTTL time.Duration) *jwksFetcher {
	if refreshTTL <= 0 {
		refreshTTL = 10 * time.Minute
	}
	return &jwksFetcher{
		url:        url,
		refreshTTL: refreshTTL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		keys:       map[string]*rsa.PublicKey{},
	}
}

// publicKey resolves a kid against the cache, refreshing the JWKS on
// miss or when the cache is stale.
func (f *jwksFetcher) publicKey(kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("jwks: token missing kid header")
	}
	f.mu.Lock()
	if k, ok := f.keys[kid]; ok && time.Since(f.fetchedAt) < f.refreshTTL {
		f.mu.Unlock()
		return k, nil
	}
	f.mu.Unlock()

	if err := f.refresh(); err != nil {
		return nil, fmt.Errorf("jwks: refresh: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.keys[kid]
	if !ok {
		return nil, fmt.Errorf("jwks: unknown kid %q after refresh", kid)
	}
	return k, nil
}

func (f *jwksFetcher) refresh() error {
	req, err := http.NewRequest(http.MethodGet, f.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("jwks: %s -> %d: %s", f.url, resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var set JWKS
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("jwks: parse: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pk, err := jwkToRSAPublic(k)
		if err != nil {
			continue // skip malformed; don't poison the whole set
		}
		keys[k.Kid] = pk
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks: no usable RSA keys in response")
	}
	f.mu.Lock()
	f.keys = keys
	f.fetchedAt = time.Now()
	f.mu.Unlock()
	return nil
}

func jwkToRSAPublic(k JWK) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("modulus: %w", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("exponent: %w", err)
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("zero exponent")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: e,
	}, nil
}

// ─── Helpers used by Identity to publish a JWKS ──────────

// PublicJWK encodes an RSA public key as a JWK. Identity uses this to
// build the JSON response served at /.well-known/jwks.json.
func PublicJWK(pub *rsa.PublicKey, kid string) JWK {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return JWK{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}
