// RFC 9728 (Protected Resource Metadata) + RFC 8414 (Authorization
// Server Metadata) discovery. Drives the cfg.oauth → discovered
// merge so users only need to write `client_id` (and even that goes
// away once dynamic client registration / RFC 7591 ships).
//
// Two-hop chain:
//
//   1. Resource server emits 401 + WWW-Authenticate carrying a
//      `resource_metadata="<URL>"` parameter (already parsed into
//      OAuthChallenge.ResourceMetadata by parseAuthChallenge).
//   2. discoverProtectedResource fetches that URL → ResourceMetadata
//      JSON → reads `authorization_servers[0]`.
//   3. discoverAuthorizationServer appends the IETF well-known
//      suffix `/.well-known/oauth-authorization-server` to the AS
//      base URL → ASMetadata JSON → grabs authorization_endpoint +
//      token_endpoint + supported PKCE methods.
//
// Why both: some MCP servers self-host the AS metadata at the
// resource URL (skipping step 2) and some serve the AS metadata
// only at the standard well-known. We try (1) → (2) and fall back
// to assuming the resource_metadata URL IS the AS metadata if step
// 1's response carries authorization_endpoint directly.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ResourceMetadata is RFC 9728 §3 — the JSON document a protected
// resource server publishes at its `.well-known/oauth-protected-
// resource` (or wherever WWW-Authenticate.resource_metadata points).
//
// We pick out the fields biu actually consumes; unknown keys flow
// through json.Unmarshal silently.
type ResourceMetadata struct {
	// Resource is the canonical URL of the protected resource. SHOULD
	// match the `aud` claim of issued tokens.
	Resource string `json:"resource"`

	// AuthorizationServers is the list of issuer URIs whose tokens
	// this resource accepts. We pick index 0 and follow it.
	AuthorizationServers []string `json:"authorization_servers"`

	// ScopesSupported is the union of scopes any AS in
	// AuthorizationServers may grant. Optional; biu uses it as a
	// hint when cfg didn't list scopes explicitly.
	ScopesSupported []string `json:"scopes_supported,omitempty"`

	// BearerMethodsSupported is one of "header" / "body" / "query".
	// biu only emits "header" today, but keeping the field for future
	// validation.
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// ASMetadata is RFC 8414 §2 — the authorization server's
// self-description. Picked-out fields match what startOAuthFlow
// consumes directly.
type ASMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
}

// discoverOAuthEndpoints walks the RFC 9728 → RFC 8414 chain starting
// from the resource_metadata URL captured in WWW-Authenticate. Returns
// a partial OAuthSpec (URLs + scopes) that the caller MERGES with
// user-provided cfg.oauth — explicit cfg fields always win because
// the user might be on a non-conformant server that lies in metadata.
//
// nil return + nil error means "no metadata available, fall back to
// pure cfg". An error is only returned when the network /
// JSON-decode hit something the caller should surface (e.g. unparseable
// JSON at a URL the server explicitly pointed us at — that's almost
// certainly a bug worth flagging).
func discoverOAuthEndpoints(ctx context.Context, resourceMetadataURL string) (*OAuthSpec, error) {
	if resourceMetadataURL == "" {
		return nil, nil
	}

	rm, asMetadataInResource, err := fetchResourceMetadata(ctx, resourceMetadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetch resource metadata %s: %w", resourceMetadataURL, err)
	}

	// Some compact servers serve AS metadata at the same URL as
	// resource metadata. If we already saw authorization_endpoint
	// in step 1, skip step 2.
	var asm *ASMetadata
	switch {
	case asMetadataInResource != nil:
		asm = asMetadataInResource
	case rm != nil && len(rm.AuthorizationServers) > 0:
		asURL := joinWellKnown(rm.AuthorizationServers[0],
			"/.well-known/oauth-authorization-server")
		got, err := fetchASMetadata(ctx, asURL)
		if err != nil {
			return nil, fmt.Errorf("fetch authorization-server metadata %s: %w", asURL, err)
		}
		asm = got
	default:
		return nil, nil
	}

	if asm == nil || asm.AuthorizationEndpoint == "" || asm.TokenEndpoint == "" {
		return nil, nil
	}

	out := &OAuthSpec{
		AuthorizeURL: asm.AuthorizationEndpoint,
		TokenURL:     asm.TokenEndpoint,
	}
	// Prefer the ResourceMetadata's scope list when present (it's
	// scoped to the *resource* the user is trying to talk to, not
	// the AS's full universe of scopes).
	switch {
	case rm != nil && len(rm.ScopesSupported) > 0:
		out.Scopes = append([]string(nil), rm.ScopesSupported...)
	case len(asm.ScopesSupported) > 0:
		out.Scopes = append([]string(nil), asm.ScopesSupported...)
	}
	return out, nil
}

// fetchResourceMetadata GETs the resource_metadata URL and decodes
// it. Some servers conflate the two RFCs and serve AS metadata at
// the same URL — when we see authorization_endpoint in the response,
// also decode it as ASMetadata so the caller skips the second hop.
func fetchResourceMetadata(ctx context.Context, u string) (*ResourceMetadata, *ASMetadata, error) {
	body, err := fetchWellKnown(ctx, u)
	if err != nil {
		return nil, nil, err
	}

	var rm ResourceMetadata
	if err := json.Unmarshal(body, &rm); err != nil {
		return nil, nil, fmt.Errorf("decode resource metadata: %w", err)
	}

	// Probe the same body for AS-style fields. Avoids a second HTTP
	// hit when the server publishes a unified document.
	var asm ASMetadata
	if err := json.Unmarshal(body, &asm); err == nil &&
		asm.AuthorizationEndpoint != "" && asm.TokenEndpoint != "" {
		return &rm, &asm, nil
	}
	return &rm, nil, nil
}

// fetchASMetadata GETs the AS's well-known and decodes it.
func fetchASMetadata(ctx context.Context, u string) (*ASMetadata, error) {
	body, err := fetchWellKnown(ctx, u)
	if err != nil {
		return nil, err
	}
	var asm ASMetadata
	if err := json.Unmarshal(body, &asm); err != nil {
		return nil, fmt.Errorf("decode AS metadata: %w", err)
	}
	return &asm, nil
}

// fetchWellKnown is the shared HTTP GET → bytes path. 5 s timeout
// (well-known docs are tiny static JSON; longer waits indicate the
// server is misbehaving and we want to fall back fast).
func fetchWellKnown(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

// joinWellKnown appends the well-known suffix to an issuer URL.
// RFC 8414 §3.1 says the path is inserted BEFORE any existing path
// (issuer/.well-known/oauth-authorization-server, even if issuer has
// /tenants/foo trailing) — but the simpler "append after issuer's
// path" works for almost every real-world AS today, so we go with
// that. Strip a trailing slash to avoid the double-slash that some
// servers reject.
func joinWellKnown(issuer, suffix string) string {
	issuer = strings.TrimRight(issuer, "/")
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	return issuer + suffix
}

// mergeOAuthSpec returns a new spec where `user` fields override
// `discovered` fields. nil-safe both ways.
func mergeOAuthSpec(user, discovered *OAuthSpec) *OAuthSpec {
	switch {
	case user == nil && discovered == nil:
		return nil
	case user == nil:
		cp := *discovered
		return &cp
	case discovered == nil:
		cp := *user
		return &cp
	}
	out := *user // copy
	if out.AuthorizeURL == "" {
		out.AuthorizeURL = discovered.AuthorizeURL
	}
	if out.TokenURL == "" {
		out.TokenURL = discovered.TokenURL
	}
	if len(out.Scopes) == 0 {
		out.Scopes = append([]string(nil), discovered.Scopes...)
	}
	return &out
}

// validateDiscoveryURL is a tiny sanity check on URLs we got from
// untrusted upstream JSON. Rejects empty / non-http(s) / bad-host
// strings to keep the OAuth flow from triggering on a poisoned
// metadata document. http://localhost / 127.0.0.1 are explicitly
// allowed for tests + sandboxed dev environments.
func validateDiscoveryURL(s string) error {
	if s == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("parse %q: %w", s, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host in %q", s)
	}
	return nil
}
