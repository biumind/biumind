package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDiscoverOAuthEndpoints_TwoHop covers the standard RFC 9728 →
// RFC 8414 chain: resource_metadata.authorization_servers[0] points
// at an issuer URL whose .well-known/oauth-authorization-server
// returns the actual endpoint set.
func TestDiscoverOAuthEndpoints_TwoHop(t *testing.T) {
	asSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           "ISSUER",
			"authorization_endpoint":           "https://auth.example/authz",
			"token_endpoint":                   "https://auth.example/token",
			"code_challenge_methods_supported": []string{"S256"},
			"scopes_supported":                 []string{"read", "write"},
		})
	}))
	defer asSrv.Close()

	rsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "https://api.example/mcp",
			"authorization_servers": []string{asSrv.URL},
			"scopes_supported":      []string{"mcp:read"},
		})
	}))
	defer rsSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := discoverOAuthEndpoints(ctx, rsSrv.URL+"/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got == nil {
		t.Fatal("got nil spec")
	}
	if got.AuthorizeURL != "https://auth.example/authz" {
		t.Errorf("AuthorizeURL = %q", got.AuthorizeURL)
	}
	if got.TokenURL != "https://auth.example/token" {
		t.Errorf("TokenURL = %q", got.TokenURL)
	}
	// Resource-metadata scopes win over AS-metadata scopes (more
	// specific to the resource the user cares about).
	if len(got.Scopes) != 1 || got.Scopes[0] != "mcp:read" {
		t.Errorf("Scopes = %v, want [mcp:read] from resource metadata", got.Scopes)
	}
}

// TestDiscoverOAuthEndpoints_UnifiedDoc covers servers that conflate
// RFC 9728 + 8414 into a single document at the resource_metadata
// URL — biu should detect authorization_endpoint inline and skip
// the second hop.
func TestDiscoverOAuthEndpoints_UnifiedDoc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":               "https://api.example/mcp",
			"authorization_servers":  []string{"https://this-is-ignored.example"},
			"authorization_endpoint": "https://auth.example/authz",
			"token_endpoint":         "https://auth.example/token",
			"scopes_supported":       []string{"mcp:read"},
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := discoverOAuthEndpoints(ctx, srv.URL+"/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got.AuthorizeURL != "https://auth.example/authz" {
		t.Errorf("AuthorizeURL = %q (skipped second hop?)", got.AuthorizeURL)
	}
}

// TestDiscoverOAuthEndpoints_NoAuthServers — bare ResourceMetadata
// with no AuthorizationServers and no inline AS endpoints. Returns
// nil spec without error so the caller falls back to cfg.
func TestDiscoverOAuthEndpoints_NoAuthServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": "https://api.example/mcp",
		})
	}))
	defer srv.Close()

	got, err := discoverOAuthEndpoints(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("expected nil error on incomplete metadata, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil spec, got %+v", got)
	}
}

// TestDiscoverOAuthEndpoints_HTTPError — the resource_metadata URL
// 500s. We surface this as an error (caller decides whether to
// fall back to cfg or fail loudly).
func TestDiscoverOAuthEndpoints_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := discoverOAuthEndpoints(context.Background(), srv.URL+"/x")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

// TestDiscoverOAuthEndpoints_BadJSON — the URL returns 200 but the
// body isn't JSON. Surfaces as a decode error.
func TestDiscoverOAuthEndpoints_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json at all")
	}))
	defer srv.Close()
	_, err := discoverOAuthEndpoints(context.Background(), srv.URL+"/x")
	if err == nil {
		t.Fatal("expected decode error on non-JSON body")
	}
}

// TestDiscoverOAuthEndpoints_EmptyURL — defensive: nil/empty input
// short-circuits without error so callers can pass challenge.Re-
// sourceMetadata directly without nil-checking it themselves.
func TestDiscoverOAuthEndpoints_EmptyURL(t *testing.T) {
	got, err := discoverOAuthEndpoints(context.Background(), "")
	if err != nil || got != nil {
		t.Errorf("empty URL: got=%v err=%v, want nil/nil", got, err)
	}
}

// TestMergeOAuthSpec confirms user-provided fields always beat
// discovery-provided fields, and missing user fields get filled in
// from discovery without mutating either input.
func TestMergeOAuthSpec(t *testing.T) {
	user := &OAuthSpec{
		ClientID:     "my-client",
		AuthorizeURL: "https://user-supplied.example/authz",
		// Empty TokenURL + Scopes — discovery should fill these in.
	}
	discovered := &OAuthSpec{
		AuthorizeURL: "https://discovered.example/authz",
		TokenURL:     "https://discovered.example/token",
		Scopes:       []string{"discovered:read"},
	}
	got := mergeOAuthSpec(user, discovered)
	if got.AuthorizeURL != "https://user-supplied.example/authz" {
		t.Errorf("user AuthorizeURL didn't win: %q", got.AuthorizeURL)
	}
	if got.TokenURL != "https://discovered.example/token" {
		t.Errorf("discovered TokenURL didn't fill blank: %q", got.TokenURL)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "discovered:read" {
		t.Errorf("discovered Scopes didn't fill blank: %v", got.Scopes)
	}
	// Inputs must remain unchanged (defensive copy).
	if user.TokenURL != "" {
		t.Error("merge mutated user input")
	}
	if discovered.AuthorizeURL != "https://discovered.example/authz" {
		t.Error("merge mutated discovered input")
	}
}

// TestMergeOAuthSpec_NilHandling — both, either, and neither.
func TestMergeOAuthSpec_NilHandling(t *testing.T) {
	if got := mergeOAuthSpec(nil, nil); got != nil {
		t.Errorf("both-nil should give nil, got %+v", got)
	}
	d := &OAuthSpec{TokenURL: "x"}
	if got := mergeOAuthSpec(nil, d); got == nil || got.TokenURL != "x" {
		t.Errorf("nil-user should clone discovered: got %+v", got)
	}
	u := &OAuthSpec{TokenURL: "y"}
	if got := mergeOAuthSpec(u, nil); got == nil || got.TokenURL != "y" {
		t.Errorf("nil-discovered should clone user: got %+v", got)
	}
}

// TestNeedsDiscovery — only AuthorizeURL/TokenURL trigger discovery.
// ClientID staying blank is handled separately (RFC 7591 territory,
// out of P20.49 scope).
func TestNeedsDiscovery(t *testing.T) {
	cases := []struct {
		name string
		spec *OAuthSpec
		want bool
	}{
		{"nil", nil, false},
		{"complete", &OAuthSpec{
			ClientID: "x", AuthorizeURL: "y", TokenURL: "z",
		}, false},
		{"missing-authz", &OAuthSpec{ClientID: "x", TokenURL: "z"}, true},
		{"missing-token", &OAuthSpec{ClientID: "x", AuthorizeURL: "y"}, true},
		{"only-client-id", &OAuthSpec{ClientID: "x"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsDiscovery(tt.spec); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

// TestValidateDiscoveryURL — sanity check on URLs we got from
// upstream JSON. Need to allow http://localhost for tests, reject
// non-http(s) schemes / missing host.
func TestValidateDiscoveryURL(t *testing.T) {
	cases := []struct {
		url    string
		wantOK bool
	}{
		{"https://auth.example/authz", true},
		{"http://localhost:8080/authz", true},
		{"http://127.0.0.1/authz", true},
		{"", false},
		{"file:///etc/passwd", false},
		{"ftp://example.com", false},
		{"://no-scheme", false},
		{"https://", false},
	}
	for _, tt := range cases {
		t.Run(tt.url, func(t *testing.T) {
			err := validateDiscoveryURL(tt.url)
			if tt.wantOK && err != nil {
				t.Errorf("expected ok, got err: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}
