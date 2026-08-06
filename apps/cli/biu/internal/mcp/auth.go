// MCP HTTP OAuth state — captures the "401 with auth challenge"
// metadata so a downstream pseudo-tool can pick up the flow.
//
// Per RFC 6750 §3 a server MUST emit `WWW-Authenticate: Bearer …` on
// the 401, and per RFC 9728 / OAuth 2.0 Protected Resource Metadata
// the same header MAY carry a `resource_metadata="<URL>"` parameter
// pointing at the discovery doc. We capture both: the raw header
// (logged into /mcp diagnostics) AND a parsed view (used by the
// authenticate pseudo-tool to drive the PKCE handshake).
//
// State lifecycle on a single HTTPClient:
//
//   ✓ initial      → first call() succeeds, normal tools register
//   ↺ needs-auth   → first call() (typically Initialize) returns 401 with
//                    a Bearer challenge. We capture OAuthChallenge,
//                    DON'T flip healthy=false (would kill reconnect),
//                    and surface a single pseudo-tool
//                    `mcp__<server>__authenticate` that the model
//                    invokes to start the PKCE flow.
//   ✓ authenticated → After the user completes OAuth in their browser
//                    we save Tokens, set the Authorization header, and
//                    Reconnect — the real tools list arrives on the
//                    next handshake and the pseudo-tool gets evicted
//                    by replaceServerTools.
//
// Other 401 / 403 (no Bearer challenge, or unparseable challenge) keep
// the legacy behaviour: sticky unhealthy. We only branch to needs-auth
// when there's enough metadata to actually drive a flow.

package mcp

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// OAuthChallenge captures what the server told us in WWW-Authenticate.
// Empty fields just mean the server didn't advertise that hint —
// callers fall back to discovery via the well-known metadata path.
type OAuthChallenge struct {
	// Realm is the optional `realm="…"` parameter. Mostly cosmetic;
	// servers use it as a human-readable scope label.
	Realm string

	// ResourceMetadata is the `resource_metadata="…"` URL pointing at
	// the OAuth Protected Resource Metadata document (RFC 9728). The
	// pseudo-tool fetches this to discover the authorization server.
	ResourceMetadata string

	// Scope is the optional `scope="…"` parameter — space-separated
	// scopes the resource server expects.
	Scope string

	// Raw is the full WWW-Authenticate header for diagnostics + as a
	// last-resort fallback when parsing the structured fields fails.
	Raw string
}

// HasFlow reports whether we have enough information to attempt an
// OAuth flow. Without ResourceMetadata we'd have to guess the
// authorize URL, which we don't (yet — server-provided metadata is
// the only path biu speaks).
func (c OAuthChallenge) HasFlow() bool {
	return c.ResourceMetadata != ""
}

// parseAuthChallenge extracts an OAuthChallenge from a 401 response's
// WWW-Authenticate header. Per RFC 7235 §4.1 there can be multiple
// challenges in the header; we keep only the first `Bearer` one.
//
// The grammar is:
//
//   challenge = auth-scheme [ 1*SP token68 / [ auth-param *( OWS "," OWS auth-param ) ] ]
//
// We don't implement the full grammar — just enough to pull
// realm / resource_metadata / scope out of the typical shape:
//
//   Bearer realm="example", resource_metadata="https://...", scope="mcp"
//
// Quoted values stay intact; unquoted token68 form is also tolerated.
func parseAuthChallenge(resp *http.Response) (OAuthChallenge, bool) {
	hdr := resp.Header.Get("WWW-Authenticate")
	if hdr == "" {
		return OAuthChallenge{}, false
	}
	out := OAuthChallenge{Raw: hdr}

	// Strip the leading "Bearer " (case-insensitive).
	rest := hdr
	if i := strings.IndexByte(hdr, ' '); i > 0 {
		scheme := strings.ToLower(strings.TrimSpace(hdr[:i]))
		if scheme != "bearer" {
			// Not a Bearer challenge — biu doesn't drive Basic / Digest
			// flows for MCP. Caller falls back to legacy 401 handling.
			return OAuthChallenge{}, false
		}
		rest = strings.TrimSpace(hdr[i+1:])
	}

	for _, part := range splitAuthParams(rest) {
		k, v, ok := splitAuthKV(part)
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "realm":
			out.Realm = v
		case "resource_metadata":
			out.ResourceMetadata = v
		case "scope":
			out.Scope = v
		}
	}
	return out, true
}

// splitAuthParams splits "a=1, b=\"x,y\", c=3" honouring quoted
// commas. Tiny hand-rolled parser; the WWW-Authenticate grammar is
// CSV-with-quotes and Go's stdlib doesn't expose a parser.
func splitAuthParams(s string) []string {
	var (
		out      []string
		buf      strings.Builder
		inQuotes bool
	)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			inQuotes = !inQuotes
			buf.WriteByte(ch)
		case ch == ',' && !inQuotes:
			out = append(out, strings.TrimSpace(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(ch)
		}
	}
	if buf.Len() > 0 {
		out = append(out, strings.TrimSpace(buf.String()))
	}
	return out
}

// splitAuthKV pulls "k=v" or `k="v"` apart. Returns false if the
// fragment doesn't contain '=' (shouldn't happen for valid headers).
func splitAuthKV(s string) (string, string, bool) {
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(s[:eq])
	v := strings.TrimSpace(s[eq+1:])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return k, v, true
}

// authState holds the per-client OAuth state. atomic.Pointer keeps
// reads lock-free for the hot path (ListTools / Call pre-flight check).
//
// flowMu/flowErr (P20.49b) record the most recent OAuth flow result
// so the pseudo-tool's next invocation can surface a useful
// diagnostic when the user closed the browser tab without consenting,
// the token exchange 4xx'd, etc. Mutex (not atomic) because errors
// carry stack traces / wrapped chains that don't atomic-load cleanly.
type authState struct {
	challenge atomic.Pointer[OAuthChallenge]
	tokens    atomic.Pointer[OAuthTokens] // set after a successful flow

	flowMu  sync.Mutex
	flowErr error

	// refreshMu serialises refresh-grant attempts so concurrent
	// tool calls don't spawn N parallel refreshes against the same
	// token endpoint (P20.49c-#4). The first goroutine in does the
	// refresh; the rest see fresh tokens once they take the lock.
	refreshMu sync.Mutex
}

// OAuthTokens is what the authenticate pseudo-tool stores after the
// PKCE exchange completes. The HTTPClient consults TokenForRequest to
// decorate subsequent requests with `Authorization: Bearer <access>`.
type OAuthTokens struct {
	AccessToken  string
	RefreshToken string
	TokenType    string // typically "Bearer"
	ExpiresAtUnix int64 // 0 ⇒ no expiry advertised
}

// NeedsAuth reports whether this client is currently in the
// needs-auth state — set after a 401 with a parseable Bearer
// challenge, cleared once Tokens are stored.
func (a *authState) NeedsAuth() bool {
	if a == nil {
		return false
	}
	if a.tokens.Load() != nil {
		return false
	}
	return a.challenge.Load() != nil
}

// Challenge returns the captured OAuth challenge, or nil when the
// client isn't in needs-auth.
func (a *authState) Challenge() *OAuthChallenge {
	if a == nil {
		return nil
	}
	return a.challenge.Load()
}

// Tokens returns the stored OAuth tokens, or nil when the client
// hasn't been authenticated yet.
func (a *authState) Tokens() *OAuthTokens {
	if a == nil {
		return nil
	}
	return a.tokens.Load()
}

// SetChallenge stores an OAuth challenge captured from a 401. Idempotent.
func (a *authState) SetChallenge(c OAuthChallenge) {
	if a == nil {
		return
	}
	cp := c
	a.challenge.Store(&cp)
}

// SetTokens stores the result of a completed OAuth flow. After this
// the client transitions out of needs-auth; the pseudo-tool gets
// evicted on the next tools/list refresh.
func (a *authState) SetTokens(t OAuthTokens) {
	if a == nil {
		return
	}
	cp := t
	a.tokens.Store(&cp)
}
