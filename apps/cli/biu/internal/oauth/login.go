// Login flow: open browser → wait for callback → exchange code →
// persist tokens.
//
// Two redirect modes:
//
//   * automatic — biu runs an HTTP listener on
//     http://localhost:<port>/callback. The provider redirects there
//     with `?code=...&state=...`. Browser closes via a tiny success
//     page.
//   * manual — for SSH / CI / sandboxed environments. The user copies
//     the URL printed by biu, opens it on a different machine, then
//     pastes the redirected URL back into the terminal so we can
//     extract code+state.

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginResult is what Login returns on success.
type LoginResult struct {
	Tokens     Tokens
	AuthorizeURL string // URL the user opened (returned for telemetry / display)
}

// Login executes the full PKCE flow.
//
//   * UrlOpener is invoked with the authorize URL the moment we have
//     it; it should open the user's default browser. CLI callers can
//     just print the URL.
//   * When ctx is cancelled before the callback arrives, the listener
//     is torn down and an error is returned.
//   * When ManualCode is non-empty, the listener is skipped entirely
//     — the caller has already extracted `code` from a manual-paste
//     flow (e.g. via prompt) and we go straight to token exchange.
type Login struct {
	Config Config

	// UrlOpener defaults to a no-op when nil; CLI callers usually
	// supply a function that runs `open` / `xdg-open` and ALSO prints
	// the URL so SSH sessions still work.
	UrlOpener func(authorizeURL string)

	// HTTP overrides the http.Client used for the token exchange.
	// Optional.
	HTTP *http.Client

	// ManualCode short-circuits the listener: if non-empty we skip the
	// HTTP server and POST directly to the token endpoint. Useful for
	// the manual paste flow.
	ManualCode  string
	ManualState string

	// ManualVerifier is the PKCE code_verifier paired with the
	// challenge that was used to mint ManualCode. When set Login.Run
	// uses it instead of generating a fresh one (otherwise the
	// provider rejects the exchange because verifier mismatches the
	// challenge it cached). Required when ManualCode is set.
	ManualVerifier string
}

// Run executes the flow and returns the resulting tokens. Caller is
// responsible for persisting them via Store.Save.
func (l Login) Run(ctx context.Context) (*LoginResult, error) {
	if l.Config.AuthorizeURL == "" || l.Config.TokenURL == "" {
		return nil, errors.New("oauth: AuthorizeURL + TokenURL required")
	}
	if l.Config.ClientID == "" {
		return nil, errors.New("oauth: ClientID required")
	}

	// ── Manual-paste fast path ───────────────────────────
	// Caller supplied code + verifier already; skip the listener and
	// the verifier regeneration that would otherwise break the PKCE
	// pairing.
	if l.ManualCode != "" {
		if l.ManualVerifier == "" {
			return nil, errors.New("oauth: ManualCode requires ManualVerifier (the PKCE verifier paired with the challenge)")
		}
		tokens, err := l.exchangeCode(ctx, l.ManualCode, l.ManualVerifier, l.manualRedirect(), 0)
		if err != nil {
			return nil, err
		}
		tokens.Provider = l.Config.AuthorizeURL
		return &LoginResult{Tokens: tokens}, nil
	}

	// ── Automatic-flow PKCE generation ───────────────────
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("pkce: %w", err)
	}
	state, err := RandomState()
	if err != nil {
		return nil, err
	}

	// ── Automatic flow ───────────────────────────────────
	listener, port, err := openListener(l.Config.CallbackPort)
	if err != nil {
		return nil, fmt.Errorf("listener: %w", err)
	}
	defer listener.Close()

	authURL := buildAuthURL(l.Config, challenge, state, port, false)
	if l.UrlOpener != nil {
		l.UrlOpener(authURL)
	}

	code, err := waitForCode(ctx, listener, state)
	if err != nil {
		return nil, err
	}
	tokens, err := l.exchangeCode(ctx, code, verifier,
		fmt.Sprintf("http://localhost:%d/callback", port), 0)
	if err != nil {
		return nil, err
	}
	tokens.Provider = l.Config.AuthorizeURL
	return &LoginResult{Tokens: tokens, AuthorizeURL: authURL}, nil
}

// AuthorizeURL builds the URL the user should open. Exposed so the
// CLI can print + open it before we start the listener.
func (l Login) AuthorizeURL(challenge, state string, port int) string {
	return buildAuthURL(l.Config, challenge, state, port, port == 0)
}

func (l Login) manualRedirect() string {
	if l.Config.ManualRedirectURL != "" {
		return l.Config.ManualRedirectURL
	}
	return "urn:ietf:wg:oauth:2.0:oob"
}

func (l Login) httpClient() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// buildAuthURL assembles the /authorize URL with all PKCE + state
// parameters. `manual=true` swaps the redirect for the provider's
// out-of-band URL.
func buildAuthURL(cfg Config, challenge, state string, port int, manual bool) string {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return cfg.AuthorizeURL
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	redirect := fmt.Sprintf("http://localhost:%d/callback", port)
	if manual {
		if cfg.ManualRedirectURL != "" {
			redirect = cfg.ManualRedirectURL
		} else {
			redirect = "urn:ietf:wg:oauth:2.0:oob"
		}
	}
	q.Set("redirect_uri", redirect)
	u.RawQuery = q.Encode()
	return u.String()
}

func openListener(port int) (net.Listener, int, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, err
	}
	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		l.Close()
		return nil, 0, errors.New("not a tcp listener")
	}
	return l, tcpAddr.Port, nil
}

// waitForCode runs a one-shot HTTP server on the provided listener.
// Closes once it gets a callback (or ctx cancels).
func waitForCode(ctx context.Context, l net.Listener, expectedState string) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				done <- result{err: fmt.Errorf("oauth callback error: %s — %s", e, q.Get("error_description"))}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "OAuth login failed. You can close this tab.")
				return
			}
			if expectedState != "" && q.Get("state") != expectedState {
				done <- result{err: errors.New("oauth: state mismatch (CSRF guard)")}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "Login failed: state mismatch.")
				return
			}
			code := q.Get("code")
			if code == "" {
				done <- result{err: errors.New("oauth: missing code in callback")}
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("content-type", "text/html")
			_, _ = io.WriteString(w, callbackPage)
			done <- result{code: code}
		}),
	}
	go func() { _ = server.Serve(l) }()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return r.code, r.err
	}
}

// exchangeCode POSTs the authorization_code grant to the provider's
// token endpoint and returns the resulting tokens.
func (l Login) exchangeCode(ctx context.Context, code, verifier, redirectURI string, expiresIn int) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", l.Config.ClientID)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.Config.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	resp, err := l.httpClient().Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return Tokens{}, fmt.Errorf("oauth token exchange %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return decodeTokens(resp.Body)
}

// Refresh exchanges a refresh_token for a new access_token. Returns
// the updated Tokens struct (preserves refresh_token when the server
// doesn't rotate it).
func (l Login) Refresh(ctx context.Context, current Tokens) (Tokens, error) {
	if current.RefreshToken == "" {
		return Tokens{}, errors.New("oauth: no refresh_token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", current.RefreshToken)
	form.Set("client_id", l.Config.ClientID)
	if len(l.Config.Scopes) > 0 {
		form.Set("scope", strings.Join(l.Config.Scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.Config.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	resp, err := l.httpClient().Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return Tokens{}, fmt.Errorf("oauth refresh %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	next, err := decodeTokens(resp.Body)
	if err != nil {
		return Tokens{}, err
	}
	if next.RefreshToken == "" {
		// Provider didn't rotate; keep the existing one.
		next.RefreshToken = current.RefreshToken
	}
	next.Provider = current.Provider
	return next, nil
}

// decodeTokens parses an OAuth token response. expires_in (seconds)
// is converted to absolute ExpiresAt for easier comparisons.
func decodeTokens(body io.Reader) (Tokens, error) {
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return Tokens{}, err
	}
	out := Tokens{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		TokenType:    raw.TokenType,
		Scope:        raw.Scope,
	}
	if raw.ExpiresIn > 0 {
		out.ExpiresAt = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second).UTC()
	}
	return out, nil
}

// callbackPage is shown to the user once the redirect lands. Tiny on
// purpose — the CLI is the source of truth.
const callbackPage = `<!doctype html>
<title>biu — login</title>
<style>body{font-family:system-ui;text-align:center;padding:4em;color:#333}</style>
<h2>✓ Logged in</h2>
<p>You can close this tab and return to the terminal.</p>
`

