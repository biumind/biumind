// Async OAuth PKCE flow driver for the McpAuthTool pseudo-tool
// (P20.49b). Designed for the LLM round-trip pattern:
//
//   1. Model invokes mcp__<server>__authenticate.
//   2. Tool starts the flow, returns the authorize URL synchronously.
//   3. User opens the URL in a browser, completes consent.
//   4. Browser redirects to http://localhost:<port>/callback?code=…
//   5. Background goroutine catches the code, exchanges it at the
//      token endpoint, persists the tokens onto the HTTPClient, and
//      triggers Reconnect.
//   6. The next time the model calls a real tool, the request goes
//      through with the new Authorization: Bearer header and the
//      catalog refresh swaps in the server's actual tools.
//
// Why a self-contained driver: internal/oauth.Login encapsulates the
// flow as one synchronous Run() — perfect for `biu auth login` but
// the wrong shape for an async tool call. We reuse oauth.GeneratePKCE
// + oauth.RandomState (the cryptographic bits) and roll our own
// listener / token-exchange so the pseudo-tool can return the URL
// without blocking on user-paced browser activity.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
)

// flowResult is what the background goroutine reports back via the
// authState's done channel. Either Tokens is set (success) or Err is
// (anything that prevented us from getting a usable bearer token).
type flowResult struct {
	Tokens OAuthTokens
	Err    error
}

// startOAuthFlow kicks off a PKCE flow for `c` and returns the
// authorize URL the model should hand to the user. The flow then
// runs to completion (or context cancellation) inside a goroutine
// that:
//
//   - waits for the redirect_uri callback
//   - exchanges the code at TokenURL
//   - calls c.SetOAuthTokens + c.Reconnect
//
// If c.cfg.OAuth is nil or missing required fields, returns a
// (zero, error) pair so the caller can fall back to the static-token
// guidance message.
//
// reconnectCtx is a long-lived context owning the post-callback
// reconnect; typically context.Background() with a 10-minute cap so
// users have time to complete the consent screen.
func startOAuthFlow(reconnectCtx context.Context, c *HTTPClient) (string, error) {
	if c == nil || c.cfg.OAuth == nil {
		return "", fmt.Errorf("mcp[%s]: OAuth not configured for this server (cfg.oauth missing)", clientName(c))
	}

	// Resolve effective spec by merging cfg.oauth (user) with whatever
	// discovery (P20.49c-1, RFC 9728 + RFC 8414) can pull from the
	// 401 challenge's resource_metadata URL. cfg fields always win
	// against discovery — discovery only fills BLANKS. Discovery
	// failures are non-fatal: we fall back to whatever cfg has and
	// surface the validation error below.
	spec := c.cfg.OAuth
	if needsDiscovery(spec) {
		if ch := c.AuthChallenge(); ch != nil && ch.ResourceMetadata != "" {
			discoverCtx, discoverCancel := context.WithTimeout(reconnectCtx, 8*time.Second)
			discovered, derr := discoverOAuthEndpoints(discoverCtx, ch.ResourceMetadata)
			discoverCancel()
			if derr == nil && discovered != nil {
				spec = mergeOAuthSpec(spec, discovered)
			}
			// Discovery errors are intentionally swallowed — the
			// validation block below surfaces a unified error message
			// that names exactly which fields the caller still needs
			// to provide.
		}
	}

	if spec.AuthorizeURL == "" || spec.TokenURL == "" || spec.ClientID == "" {
		return "", fmt.Errorf("mcp[%s]: OAuth requires client_id + authorize_url + token_url "+
			"(discovery from challenge.resource_metadata didn't fill the gaps)", c.cfg.Name)
	}

	verifier, challenge, err := oauth.GeneratePKCE()
	if err != nil {
		return "", fmt.Errorf("mcp[%s]: pkce: %w", c.cfg.Name, err)
	}
	state, err := oauth.RandomState()
	if err != nil {
		return "", fmt.Errorf("mcp[%s]: state: %w", c.cfg.Name, err)
	}

	listener, port, err := openCallbackListener(spec.CallbackPort)
	if err != nil {
		return "", fmt.Errorf("mcp[%s]: listener: %w", c.cfg.Name, err)
	}

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	authURL := buildAuthorizeURL(spec, challenge, state, redirectURI)

	// Goroutine: catch the redirect, exchange the code, persist tokens.
	// Owns the listener — closes it whether the flow succeeds or fails.
	go func() {
		defer listener.Close()
		// Hard cap: 10 min for the user to complete the consent screen.
		ctx, cancel := context.WithTimeout(reconnectCtx, 10*time.Minute)
		defer cancel()

		code, err := waitForCallbackCode(ctx, listener, state)
		if err != nil {
			c.auth.recordFlowFailure(fmt.Errorf("waiting for callback: %w", err))
			return
		}

		tokens, err := exchangeAuthCode(ctx, spec, code, verifier, redirectURI)
		if err != nil {
			c.auth.recordFlowFailure(fmt.Errorf("token exchange: %w", err))
			return
		}

		c.SetOAuthTokens(tokens, spec.TokenURL)
		// Reconnect refreshes the session id and re-runs Initialize +
		// ListTools — the real tool catalog lands here, evicting the
		// authenticate pseudo-tool the next time RegisterEngineTools
		// runs (which the registry triggers on tools-changed events).
		if err := c.Reconnect(ctx); err != nil {
			c.auth.recordFlowFailure(fmt.Errorf("reconnect: %w", err))
			return
		}
		c.auth.recordFlowSuccess()
	}()

	return authURL, nil
}

// needsDiscovery reports whether the user-provided cfg.oauth is
// missing fields that discovery could fill. ClientID stays
// user-required (RFC 7591 dynamic client registration is out of
// P20.49 scope) — only AuthorizeURL / TokenURL / Scopes have a
// reasonable discovery path.
func needsDiscovery(s *OAuthSpec) bool {
	if s == nil {
		return false
	}
	return s.AuthorizeURL == "" || s.TokenURL == ""
}

// clientName returns a printable name for diagnostics; falls back to
// "<unknown>" when c is nil to avoid panicking inside error formatters.
func clientName(c *HTTPClient) string {
	if c == nil {
		return "<unknown>"
	}
	return c.cfg.Name
}

// openCallbackListener binds a TCP listener for the redirect_uri.
// Pass 0 to let the OS pick an ephemeral port; otherwise honour the
// pinned port (some OAuth providers require an exact match against
// the developer console).
func openCallbackListener(preferredPort int) (net.Listener, int, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", preferredPort)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return l, port, nil
}

// buildAuthorizeURL composes the authorization-endpoint URL per
// RFC 6749 §4.1.1 + RFC 7636 §4.3 (PKCE).
func buildAuthorizeURL(spec *OAuthSpec, challenge, state, redirectURI string) string {
	u, err := url.Parse(spec.AuthorizeURL)
	if err != nil {
		// Fall back to the raw string + naive query append; the
		// flow goroutine will surface any 4xx from the upstream.
		return spec.AuthorizeURL
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", spec.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if len(spec.Scopes) > 0 {
		q.Set("scope", strings.Join(spec.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// waitForCallbackCode serves a single GET /callback?code=…&state=…
// then returns. State mismatch → 400 + error to the goroutine.
// ctx cancellation → listener Close → Accept returns errClosed and
// we surface ctx.Err.
func waitForCallbackCode(ctx context.Context, l net.Listener, expectedState string) (string, error) {
	type capture struct {
		code string
		err  error
	}
	ch := make(chan capture, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		gotState := params.Get("state")
		gotCode := params.Get("code")
		gotErr := params.Get("error")

		if gotState != expectedState {
			http.Error(w, "state mismatch — open the URL biu printed, do not reuse a stale link", http.StatusBadRequest)
			ch <- capture{err: fmt.Errorf("state mismatch")}
			return
		}
		if gotErr != "" {
			http.Error(w, "authorization rejected: "+gotErr, http.StatusBadRequest)
			ch <- capture{err: fmt.Errorf("authorization error: %s", gotErr)}
			return
		}
		if gotCode == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			ch <- capture{err: fmt.Errorf("missing code")}
			return
		}
		// Success: render a tiny page so the browser doesn't sit on a
		// confusing blank tab. The user closes it manually.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w,
			`<!doctype html><meta charset=utf-8>`+
				`<title>biu — authorization complete</title>`+
				`<body style="font-family:system-ui;padding:2rem">`+
				`<h2>Authorization complete</h2>`+
				`<p>You can close this tab and return to the terminal — biu will reconnect the MCP server automatically.</p>`)
		ch <- capture{code: gotCode}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	select {
	case c := <-ch:
		return c.code, c.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// exchangeAuthCode POSTs the code to the token endpoint per
// RFC 6749 §4.1.3 (Authorization Code grant) + RFC 7636 §4.5 (PKCE).
func exchangeAuthCode(ctx context.Context, spec *OAuthSpec, code, verifier, redirectURI string) (OAuthTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", spec.ClientID)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthTokens{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OAuthTokens{}, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, truncate(body, 240))
	}

	// Most providers return JSON. GitHub historically returns
	// application/x-www-form-urlencoded unless `Accept: application/json`
	// is set (we did) — but be defensive and try form parsing as
	// fallback.
	tokens := OAuthTokens{TokenType: "Bearer"}
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var jr jsonTokenResponse
		if err := json.Unmarshal(body, &jr); err != nil {
			return OAuthTokens{}, fmt.Errorf("decode token JSON: %w", err)
		}
		tokens.AccessToken = jr.AccessToken
		tokens.RefreshToken = jr.RefreshToken
		if jr.TokenType != "" {
			tokens.TokenType = jr.TokenType
		}
		if jr.ExpiresIn > 0 {
			tokens.ExpiresAtUnix = time.Now().Unix() + int64(jr.ExpiresIn)
		}
	} else {
		// Form-encoded fallback.
		v, err := url.ParseQuery(string(body))
		if err != nil {
			return OAuthTokens{}, fmt.Errorf("decode token form: %w", err)
		}
		tokens.AccessToken = v.Get("access_token")
		tokens.RefreshToken = v.Get("refresh_token")
		if t := v.Get("token_type"); t != "" {
			tokens.TokenType = t
		}
		if expS := v.Get("expires_in"); expS != "" {
			if exp, err := strconv.ParseInt(expS, 10, 64); err == nil && exp > 0 {
				tokens.ExpiresAtUnix = time.Now().Unix() + exp
			}
		}
	}
	if tokens.AccessToken == "" {
		return OAuthTokens{}, fmt.Errorf("token response had no access_token: %s", truncate(body, 240))
	}
	return tokens, nil
}

type jsonTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// exchangeRefreshToken POSTs a refresh_token grant to the token
// endpoint per RFC 6749 §6. Returns the new token set; the server
// MAY rotate the refresh_token (and most do — single-use refresh
// tokens are common defence-in-depth). When the server doesn't
// return a new refresh_token, we fall back to the old one.
//
// Used by the pre-flight refresh path in HTTPClient.call(): when
// the current access token is within `refreshSlack` of expiry AND
// we have refresh_token + token URL + client_id, fire a refresh
// to keep the request going without bouncing through 401 →
// re-authenticate.
func exchangeRefreshToken(ctx context.Context, tokenURL, clientID, refreshToken string) (OAuthTokens, error) {
	if tokenURL == "" || clientID == "" || refreshToken == "" {
		return OAuthTokens{}, fmt.Errorf("refresh: missing token_url / client_id / refresh_token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthTokens{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OAuthTokens{}, fmt.Errorf("refresh endpoint %d: %s",
			resp.StatusCode, truncate(body, 240))
	}

	out := OAuthTokens{TokenType: "Bearer", RefreshToken: refreshToken}
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var jr jsonTokenResponse
		if err := json.Unmarshal(body, &jr); err != nil {
			return OAuthTokens{}, fmt.Errorf("decode refresh JSON: %w", err)
		}
		out.AccessToken = jr.AccessToken
		if jr.RefreshToken != "" {
			out.RefreshToken = jr.RefreshToken // server rotated it
		}
		if jr.TokenType != "" {
			out.TokenType = jr.TokenType
		}
		if jr.ExpiresIn > 0 {
			out.ExpiresAtUnix = time.Now().Unix() + jr.ExpiresIn
		}
	} else {
		// Form-encoded fallback (rare for refresh, but cheap to support).
		v, err := url.ParseQuery(string(body))
		if err != nil {
			return OAuthTokens{}, fmt.Errorf("decode refresh form: %w", err)
		}
		out.AccessToken = v.Get("access_token")
		if rt := v.Get("refresh_token"); rt != "" {
			out.RefreshToken = rt
		}
		if t := v.Get("token_type"); t != "" {
			out.TokenType = t
		}
		if expS := v.Get("expires_in"); expS != "" {
			if exp, err := strconv.ParseInt(expS, 10, 64); err == nil && exp > 0 {
				out.ExpiresAtUnix = time.Now().Unix() + exp
			}
		}
	}
	if out.AccessToken == "" {
		return OAuthTokens{}, fmt.Errorf("refresh response had no access_token: %s", truncate(body, 240))
	}
	return out, nil
}

// refreshSlack is the safety margin before token expiry that
// triggers a pre-flight refresh. 60 s tolerates clock drift across
// the user's machine vs the OAuth server while keeping the refresh
// chatter low.
const refreshSlack = 60 * time.Second

// refreshIfNeeded runs a refresh_token grant when the current
// tokens are about to expire. Best-effort: failures don't block
// the request; the request goes through with the existing token
// and falls back to the regular 401 → needs-auth path on rejection.
//
// Concurrency: serialised via auth.refreshMu so 4 simultaneous
// tool calls don't fire 4 refresh requests. The first holder
// refreshes; the rest see fresh tokens by the time they take the
// lock.
func (c *HTTPClient) refreshIfNeeded(ctx context.Context) {
	if c == nil {
		return
	}
	tokens := c.auth.Tokens()
	if tokens == nil || tokens.RefreshToken == "" || !tokens.expiringSoon(refreshSlack) {
		return
	}
	tokenURL := c.ResolvedTokenURL()
	if tokenURL == "" {
		return
	}
	clientID := ""
	if c.cfg.OAuth != nil {
		clientID = c.cfg.OAuth.ClientID
	}
	if clientID == "" {
		return
	}

	c.auth.refreshMu.Lock()
	defer c.auth.refreshMu.Unlock()

	// Re-check after acquiring the lock — a concurrent goroutine
	// may already have refreshed.
	tokens = c.auth.Tokens()
	if tokens == nil || !tokens.expiringSoon(refreshSlack) {
		return
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fresh, err := exchangeRefreshToken(refreshCtx, tokenURL, clientID, tokens.RefreshToken)
	if err != nil {
		// Refresh failed. Don't crash the in-flight request; let it
		// proceed with the stale token and surface 401 if the
		// server rejects. Record so the pseudo-tool's diagnostic
		// can show the user why re-auth was needed.
		c.auth.recordFlowFailure(fmt.Errorf("refresh: %w", err))
		return
	}
	// Persist + cache via SetOAuthTokens. Pass empty string for the
	// resolvedTokenURL parameter — we already have it cached and
	// don't want to overwrite (refresh doesn't change the endpoint).
	c.SetOAuthTokens(fresh, "")
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// authState helpers for flow result reporting. Pseudo-tool's next
// invocation can read FlowError to surface a useful diagnostic instead
// of just "still needs auth".
//
// Concurrency: a single in-flight flow per client. recordFlowSuccess
// clears the error; a second consent attempt overwrites both.
func (a *authState) recordFlowSuccess() {
	if a == nil {
		return
	}
	a.flowMu.Lock()
	a.flowErr = nil
	a.flowMu.Unlock()
}

func (a *authState) recordFlowFailure(err error) {
	if a == nil {
		return
	}
	a.flowMu.Lock()
	a.flowErr = err
	a.flowMu.Unlock()
}

func (a *authState) FlowError() error {
	if a == nil {
		return nil
	}
	a.flowMu.Lock()
	defer a.flowMu.Unlock()
	return a.flowErr
}

// flowResult is the structured outcome a future synchronous-status
// API can return; today the helpers above only need (success, err).
// Kept exported-shape so tests can construct mock results.
var _ = flowResult{}
