package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBuildAuthorizeURL locks the query parameter set + ordering
// (alphabetical via Encode). RFC 6749 §4.1.1 + RFC 7636 §4.3 require
// every parameter we set; missing one would silently break the flow
// against any conformant authorization server.
func TestBuildAuthorizeURL(t *testing.T) {
	spec := &OAuthSpec{
		ClientID:     "my-client",
		AuthorizeURL: "https://auth.example.com/oauth/authorize",
		TokenURL:     "https://auth.example.com/oauth/token",
		Scopes:       []string{"read", "write"},
	}
	got := buildAuthorizeURL(spec, "CHALLENGE", "STATE", "http://127.0.0.1:5555/callback")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != spec.AuthorizeURL {
		t.Errorf("base URL got %q want %q", u.Scheme+"://"+u.Host+u.Path, spec.AuthorizeURL)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "my-client",
		"redirect_uri":          "http://127.0.0.1:5555/callback",
		"state":                 "STATE",
		"code_challenge":        "CHALLENGE",
		"code_challenge_method": "S256",
		"scope":                 "read write",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query[%q] = %q, want %q", k, got, v)
		}
	}
}

// TestBuildAuthorizeURL_PreservesExistingQuery — some providers
// require their own canonical query parameters on the authorize URL
// (e.g. `?prompt=consent`). buildAuthorizeURL should merge ours into
// theirs, not stomp.
func TestBuildAuthorizeURL_PreservesExistingQuery(t *testing.T) {
	spec := &OAuthSpec{
		ClientID:     "x",
		AuthorizeURL: "https://auth.example.com/oauth/authorize?prompt=consent",
		TokenURL:     "https://auth.example.com/oauth/token",
	}
	got := buildAuthorizeURL(spec, "c", "s", "http://127.0.0.1:0/callback")
	u, _ := url.Parse(got)
	if u.Query().Get("prompt") != "consent" {
		t.Errorf("provider's prompt= got dropped: %q", got)
	}
	if u.Query().Get("client_id") != "x" {
		t.Errorf("our client_id missing: %q", got)
	}
}

// TestBuildAuthorizeURL_NoScopes — Scopes is optional. If empty, we
// must NOT emit `scope=` (some servers reject empty scopes).
func TestBuildAuthorizeURL_NoScopes(t *testing.T) {
	spec := &OAuthSpec{
		ClientID:     "x",
		AuthorizeURL: "https://auth.example.com/oauth/authorize",
		TokenURL:     "https://auth.example.com/oauth/token",
	}
	got := buildAuthorizeURL(spec, "c", "s", "http://127.0.0.1:0/callback")
	if strings.Contains(got, "scope=") {
		t.Errorf("scope= emitted with no Scopes set: %q", got)
	}
}

// TestOAuthFlow_EndToEnd is the meaningful integration test: it
// drives the full PKCE handshake against an in-process httptest
// server playing the part of a Streamable HTTP MCP service that
// gates everything behind OAuth.
//
// Flow exercised:
//
//   1. Client.Initialize → fixture returns 401 + Bearer challenge
//      with resource_metadata. HTTPClient parses it, transitions to
//      needs-auth, returns ErrNeedsAuth.
//   2. Test calls startOAuthFlow → gets back authorize URL with
//      code_challenge / state / redirect_uri / scope.
//   3. Test simulates the user clicking the URL via http.Client
//      (default redirect-following). Fixture's /authorize responds
//      302 to redirect_uri + code+state.
//   4. The redirect lands in biu's local callback listener; the
//      goroutine captures the code, POSTs to /token with the
//      verifier, fixture issues access_token.
//   5. Goroutine calls client.SetOAuthTokens + client.Reconnect.
//      Reconnect's Initialize now goes through with Authorization:
//      Bearer set; fixture returns the real serverInfo.
//   6. Test calls ListTools → fixture returns a single `echo` tool.
//
// What this test does NOT do: drive the engine.SimpleRegistry tool
// swap. The engine_adapter's pseudo-tool eviction is independent of
// the OAuth mechanics and is exercised by the unit test that locks
// `replaceServerTools` behaviour.
func TestOAuthFlow_EndToEnd(t *testing.T) {
	const issuedAccessToken = "test-access-token-zx9k"

	var (
		mu          sync.Mutex
		gotVerifier string
		issuedCode  = "code-abc-123"
	)

	var fixture *httptest.Server
	fixture = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			q := r.URL.Query()
			for _, k := range []string{
				"client_id", "redirect_uri", "state",
				"code_challenge", "code_challenge_method",
			} {
				if q.Get(k) == "" {
					http.Error(w, "missing "+k, http.StatusBadRequest)
					return
				}
			}
			if q.Get("code_challenge_method") != "S256" {
				http.Error(w, "expected S256", http.StatusBadRequest)
				return
			}
			redirectURL := fmt.Sprintf("%s?code=%s&state=%s",
				q.Get("redirect_uri"), issuedCode, url.QueryEscape(q.Get("state")))
			http.Redirect(w, r, redirectURL, http.StatusFound)

		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if r.Form.Get("code") != issuedCode {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") != "authorization_code" {
				http.Error(w, "bad grant_type", http.StatusBadRequest)
				return
			}
			mu.Lock()
			gotVerifier = r.Form.Get("code_verifier")
			mu.Unlock()
			if gotVerifier == "" {
				http.Error(w, "missing code_verifier", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": issuedAccessToken,
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/mcp":
			authz := r.Header.Get("Authorization")
			if authz != "Bearer "+issuedAccessToken {
				w.Header().Set("WWW-Authenticate",
					fmt.Sprintf(`Bearer realm="test", resource_metadata="%s/.well-known/oauth-protected-resource"`,
						fixture.URL))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			method, _ := req["method"].(string)
			id := req["id"]
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			switch method {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"serverInfo": map[string]any{
							"name": "fake-oauth", "version": "0.1",
						},
						"capabilities": map[string]any{"tools": map[string]any{}},
					},
				})
			case "tools/list":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "echo",
							"description": "echoes its input",
							"inputSchema": map[string]any{"type": "object"},
						}},
					},
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": id, "result": map[string]any{},
				})
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()

	cfg := HTTPConfig{
		Name: "fake",
		URL:  fixture.URL + "/mcp",
		OAuth: &OAuthSpec{
			ClientID:     "test-client",
			AuthorizeURL: fixture.URL + "/authorize",
			TokenURL:     fixture.URL + "/token",
			Scopes:       []string{"read"},
		},
	}
	client := NewHTTP(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: first Initialize hits 401 + Bearer challenge.
	if _, err := client.Initialize(ctx); err == nil || !errors.Is(err, ErrNeedsAuth) {
		t.Fatalf("expected ErrNeedsAuth from Initialize, got %v", err)
	}
	if !client.NeedsAuth() {
		t.Fatal("client should be in needs-auth state after 401")
	}
	if ch := client.AuthChallenge(); ch == nil || !strings.HasPrefix(ch.ResourceMetadata, fixture.URL) {
		t.Fatalf("AuthChallenge missing or wrong resource_metadata: %+v", ch)
	}

	// Step 2: drive the OAuth flow programmatically. Returns the
	// authorize URL to pass to the user; the goroutine handles the
	// callback + token exchange + reconnect in the background.
	authURL, err := startOAuthFlow(context.Background(), client)
	if err != nil {
		t.Fatalf("startOAuthFlow: %v", err)
	}

	// Step 3: simulate the user opening the URL. Default http.Client
	// follows the 302 redirect into biu's callback listener — both
	// the GET response succeeds AND biu's goroutine captures the code.
	httpClient := http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(authURL)
	if err != nil {
		t.Fatalf("simulating user open(authorize): %v", err)
	}
	resp.Body.Close()

	// Step 4: wait for the goroutine to land tokens. Polling is
	// cheap; the typical critical path is sub-50ms (token exchange
	// + reconnect against an in-process httptest).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !client.NeedsAuth() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if client.NeedsAuth() {
		t.Fatalf("OAuth flow did not complete: flow err = %v", client.auth.FlowError())
	}

	// Step 5: verify the tokens we expected landed.
	tokens := client.auth.Tokens()
	if tokens == nil {
		t.Fatal("no tokens stored after flow completed")
	}
	if tokens.AccessToken != issuedAccessToken {
		t.Errorf("access_token = %q, want %q", tokens.AccessToken, issuedAccessToken)
	}
	if tokens.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want %q", tokens.TokenType, "Bearer")
	}
	if tokens.ExpiresAtUnix == 0 {
		t.Error("ExpiresAtUnix should be set when expires_in is provided")
	}
	mu.Lock()
	verifier := gotVerifier
	mu.Unlock()
	if verifier == "" {
		t.Fatal("token endpoint never received code_verifier (PKCE pairing missing)")
	}

	// Step 6: real tools/list call works now — the fixture sees the
	// Authorization header on the post-flow request.
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools after OAuth completed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("tools = %+v, want [echo]", tools)
	}
}

// TestRefreshIfNeeded_ExchangesAndPersists drives the pre-flight
// refresh path end-to-end:
//
//   1. HTTPClient is seeded with an access token that's about to
//      expire + a refresh_token + a resolved token URL pointing at
//      a test server that accepts refresh_token grants.
//   2. refreshIfNeeded fires.
//   3. The test server returns a fresh access_token + (rotated)
//      refresh_token + new expires_in.
//   4. HTTPClient's auth state now holds the new tokens AND they
//      are persisted to the store (so a fresh NewHTTP picks them up).
//
// The seam under test is the integration of exchangeRefreshToken +
// SetOAuthTokens + token-store persistence + the pre-flight
// expiringSoon check.
func TestRefreshIfNeeded_ExchangesAndPersists(t *testing.T) {
	withTempStore(t)

	const (
		oldAccess  = "old-access"
		oldRefresh = "old-refresh"
		newAccess  = "new-access-zx9k"
		newRefresh = "new-refresh-zx9k"
		clientID   = "my-client"
	)

	var refreshHits int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "wrong grant_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("refresh_token") != oldRefresh {
			http.Error(w, "wrong refresh_token", http.StatusBadRequest)
			return
		}
		if r.Form.Get("client_id") != clientID {
			http.Error(w, "wrong client_id", http.StatusBadRequest)
			return
		}
		refreshHits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	c := NewHTTP(HTTPConfig{
		Name: "fake",
		URL:  "https://api.example/mcp",
		OAuth: &OAuthSpec{
			ClientID: clientID,
			TokenURL: tokenSrv.URL,
		},
	})
	// Seed expiring tokens manually (simulate a previous run that's
	// near token expiry).
	expiring := OAuthTokens{
		AccessToken:   oldAccess,
		RefreshToken:  oldRefresh,
		TokenType:     "Bearer",
		ExpiresAtUnix: time.Now().Add(20 * time.Second).Unix(), // within refreshSlack
	}
	c.SetOAuthTokens(expiring, tokenSrv.URL)

	c.refreshIfNeeded(context.Background())

	if refreshHits != 1 {
		t.Errorf("expected 1 refresh hit, got %d", refreshHits)
	}
	got := c.auth.Tokens()
	if got == nil || got.AccessToken != newAccess {
		t.Errorf("access_token = %v, want %s", got, newAccess)
	}
	if got.RefreshToken != newRefresh {
		t.Errorf("refresh_token rotation lost: %q want %q", got.RefreshToken, newRefresh)
	}
	if got.ExpiresAtUnix == 0 {
		t.Error("ExpiresAtUnix should be set from expires_in")
	}

	// Persisted? Fresh client picks up the new tokens.
	c2 := NewHTTP(HTTPConfig{
		Name:  "fake",
		URL:   "https://api.example/mcp",
		OAuth: &OAuthSpec{ClientID: clientID, TokenURL: tokenSrv.URL},
	})
	if got2 := c2.auth.Tokens(); got2 == nil || got2.AccessToken != newAccess {
		t.Errorf("fresh client didn't see persisted refresh: %+v", got2)
	}
}

// TestRefreshIfNeeded_NoOpWhenNotExpiring confirms the pre-flight
// is silent on tokens that are still safely live — no chatter
// against the upstream token endpoint, no state mutation.
func TestRefreshIfNeeded_NoOpWhenNotExpiring(t *testing.T) {
	withTempStore(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	c := NewHTTP(HTTPConfig{
		Name:  "fake",
		URL:   "https://api.example/mcp",
		OAuth: &OAuthSpec{ClientID: "x", TokenURL: srv.URL},
	})
	c.SetOAuthTokens(OAuthTokens{
		AccessToken:   "still-fresh",
		RefreshToken:  "rt",
		TokenType:     "Bearer",
		ExpiresAtUnix: time.Now().Add(2 * time.Hour).Unix(),
	}, srv.URL)

	c.refreshIfNeeded(context.Background())
	if hits != 0 {
		t.Errorf("expected 0 refresh hits, got %d", hits)
	}
	if c.auth.Tokens().AccessToken != "still-fresh" {
		t.Errorf("token mutated unexpectedly")
	}
}

// TestRefreshIfNeeded_FailureDoesntCrash — pre-flight is best-effort.
// A 4xx from the token endpoint records the error onto authState
// for diagnostics but DOESN'T clear the existing tokens (the request
// proceeds with the stale token; if upstream MCP rejects it, the
// regular needs-auth path takes over).
func TestRefreshIfNeeded_FailureDoesntCrash(t *testing.T) {
	withTempStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewHTTP(HTTPConfig{
		Name:  "fake",
		URL:   "https://api.example/mcp",
		OAuth: &OAuthSpec{ClientID: "x", TokenURL: srv.URL},
	})
	c.SetOAuthTokens(OAuthTokens{
		AccessToken:   "stale",
		RefreshToken:  "rt",
		TokenType:     "Bearer",
		ExpiresAtUnix: time.Now().Add(20 * time.Second).Unix(),
	}, srv.URL)

	c.refreshIfNeeded(context.Background())

	// Stale tokens still in place.
	if got := c.auth.Tokens(); got == nil || got.AccessToken != "stale" {
		t.Errorf("refresh failure shouldn't clear tokens: %+v", got)
	}
	// Flow error captured for the pseudo-tool diagnostic.
	if c.auth.FlowError() == nil {
		t.Error("flow error should be recorded after refresh failure")
	}
}

// TestRefreshIfNeeded_NoTokenURL — without a resolved token URL the
// refresh path can't run; it should silently skip rather than panic.
func TestRefreshIfNeeded_NoTokenURL(t *testing.T) {
	withTempStore(t)
	c := NewHTTP(HTTPConfig{
		Name:  "fake",
		URL:   "https://api.example/mcp",
		OAuth: &OAuthSpec{ClientID: "x"}, // no token URL
	})
	c.SetOAuthTokens(OAuthTokens{
		AccessToken:   "stale",
		RefreshToken:  "rt",
		ExpiresAtUnix: time.Now().Add(10 * time.Second).Unix(),
	}, "")
	c.refreshIfNeeded(context.Background()) // must not panic
	if got := c.auth.Tokens(); got.AccessToken != "stale" {
		t.Errorf("tokens shouldn't have changed without token URL: %+v", got)
	}
}

// TestStartOAuthFlow_BadConfig fails fast on incomplete OAuth specs
// (missing client_id / authorize_url / token_url) without launching
// any goroutines or listeners.
func TestStartOAuthFlow_BadConfig(t *testing.T) {
	cases := []struct {
		name string
		spec *OAuthSpec
	}{
		{"nil", nil},
		{"no-client-id", &OAuthSpec{AuthorizeURL: "x", TokenURL: "y"}},
		{"no-authorize-url", &OAuthSpec{ClientID: "x", TokenURL: "y"}},
		{"no-token-url", &OAuthSpec{ClientID: "x", AuthorizeURL: "y"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := &HTTPClient{cfg: HTTPConfig{Name: "fake", OAuth: tt.spec}}
			c.auth = &authState{}
			_, err := startOAuthFlow(t.Context(), c)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}
