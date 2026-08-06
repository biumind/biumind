// Streamable HTTP transport for MCP. Implements the 2025-03-26 spec
// variant: a single endpoint accepts JSON-RPC POSTs and replies
// either inline JSON or as Server-Sent Events (text/event-stream)
// when the server wants to stream notifications back. The single-
// endpoint shape supersedes the older "HTTP+SSE" two-endpoint
// design and matches what hosted MCP services (Atlassian, Linear,
// remote claude.ai connectors) actually deploy today.
//
// Spec: https://modelcontextprotocol.io/specification/2025-03-26/basic/transports
//
// Wire framing:
//   - Every client→server message is a POST to URL with
//     Content-Type: application/json and Accept: application/json,
//     text/event-stream (the dual Accept is required by the spec —
//     servers reject 406 otherwise).
//   - The server returns either:
//       (a) Content-Type: application/json — a single JSON-RPC
//           response body. Common case for simple request/reply.
//       (b) Content-Type: text/event-stream — an SSE stream of
//           "data:" lines; one of those carries the JSON-RPC
//           response and the rest may be notifications.
//   - Sessions are tracked via Mcp-Session-Id header — server
//     issues one in the initialize response, client echoes it on
//     subsequent requests so server can correlate state.
//
// Notifications (e.g. resources/list_changed) on the SSE channel
// are read but currently dropped — biu's REPL doesn't subscribe to
// invalidations yet. Hooking them into a future "MCP server
// reloaded its catalog" event is straightforward; the readLoop
// scaffolding is here.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// HTTPConfig is the launch spec for an HTTP MCP server. URL is
// required; Headers is optional (typical use: Authorization bearer
// tokens, custom auth schemes, or vendor-specific routing keys).
type HTTPConfig struct {
	Name    string
	URL     string
	Headers map[string]string

	// OAuth (P20.49b), when non-nil, drives the PKCE flow on 401
	// instead of returning the static-token guidance message. Caller
	// (cmd/biu/wiring) populates this from cfg.MCPServers[].OAuth.
	OAuth *OAuthSpec
}

// OAuthSpec carries the PKCE flow parameters the authenticate
// pseudo-tool needs. All four URL/ID fields are typically required —
// RFC 9728 metadata auto-discovery from the WWW-Authenticate header
// is a future refinement; today the user copies these from the
// provider's OAuth-app developer console (e.g. github.com/settings/developers).
type OAuthSpec struct {
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	Scopes       []string
	CallbackPort int // 0 = ephemeral
}

// streamableAcceptHeader is required on every POST per the
// 2025-03-26 spec — strict servers return 406 without it.
const streamableAcceptHeader = "application/json, text/event-stream"

// sessionHeaderName is the HTTP header used to round-trip the
// MCP session id between client and server.
const sessionHeaderName = "Mcp-Session-Id"

// HTTPClient is the Streamable HTTP MCP transport. Concurrency:
// every method is safe for concurrent calls — request bookkeeping
// is per-call (no shared pending map; HTTP gives us request/reply
// correlation for free).
type HTTPClient struct {
	cfg HTTPConfig

	httpClient *http.Client
	sessionID  atomic.Pointer[string]
	nextID     atomic.Int64
	healthy    atomic.Bool

	// auth holds OAuth challenge / tokens captured after a 401 with a
	// parseable Bearer challenge. Lock-free reads on the hot path
	// (NeedsAuth() check before tools/list registration). Set in
	// call() when the server signals needs-auth, cleared by
	// AuthenticatedReconnect once the pseudo-tool's PKCE flow lands.
	auth *authState

	// resolvedTokenURL caches the token-endpoint URL that issued the
	// current OAuthTokens — populated either from cfg.OAuth.TokenURL,
	// from RFC 8414 discovery during startOAuthFlow, or from the
	// persisted token store on NewHTTP. Refresh grants (P20.49c-#4)
	// POST to this URL; without it the only recovery from expiry is
	// re-running the full PKCE dance. atomic.Pointer keeps reads
	// lock-free on the hot pre-request path.
	resolvedTokenURL atomic.Pointer[string]

	// Tracks outstanding background streams (the long-lived GET
	// channel servers may open for spontaneous notifications).
	// We don't ship an active GET today, but the close-on-shutdown
	// hook is wired so adding one is a one-liner.
	mu      sync.Mutex
	streams []io.Closer
	closed  atomic.Bool
}

// ErrNeedsAuth is returned by call() when a 401 carried a parseable
// `Bearer` challenge with enough metadata to drive an OAuth flow.
// connectClient treats this specially: the server gets registered
// with a single `mcp__<name>__authenticate` pseudo-tool instead of
// failing bootstrap.
//
// Callers that don't care about the auth distinction can compare
// errors.Is(err, ErrNeedsAuth) — the sentinel doesn't carry detail
// beyond its type.
var ErrNeedsAuth = errors.New("mcp: server requires OAuth (needs-auth)")

// NewHTTP builds an HTTPClient. The connection isn't opened until
// Start is called; this lets tests construct a client + assert on
// its config without spawning live HTTP traffic.
//
// If a previous biu session persisted OAuth tokens for this server's
// URL (P20.49c-#2), they're loaded into the auth state immediately
// so the first Initialize goes through with Authorization: Bearer
// already set — no needs-auth detour, no second browser dance.
// Load failures are silent: a corrupt or missing token store just
// means the user runs the OAuth flow again, no worse than today.
func NewHTTP(cfg HTTPConfig) *HTTPClient {
	c := &HTTPClient{
		cfg:        cfg,
		httpClient: http.DefaultClient,
		auth:       &authState{},
	}
	c.healthy.Store(true)
	if cfg.URL != "" {
		if entry, err := loadTokenEntry(cfg.URL); err == nil && entry != nil && entry.AccessToken != "" {
			tt := entry.TokenType
			if tt == "" {
				tt = "Bearer"
			}
			c.auth.SetTokens(OAuthTokens{
				AccessToken:   entry.AccessToken,
				RefreshToken:  entry.RefreshToken,
				TokenType:     tt,
				ExpiresAtUnix: entry.ExpiresAtUnix,
			})
			// Cache the persisted TokenURL so refresh grants survive
			// across biu restarts even when the original challenge /
			// discovery context is gone.
			if entry.TokenURL != "" {
				tu := entry.TokenURL
				c.resolvedTokenURL.Store(&tu)
			}
		}
	}
	// Fall back to cfg.OAuth.TokenURL when we didn't have a persisted
	// store entry but the user pinned a token URL explicitly.
	if c.resolvedTokenURL.Load() == nil && cfg.OAuth != nil && cfg.OAuth.TokenURL != "" {
		tu := cfg.OAuth.TokenURL
		c.resolvedTokenURL.Store(&tu)
	}
	return c
}

// ResolvedTokenURL returns the token endpoint URL biu uses for this
// client (post-discovery / cfg / persisted-store merge). Empty when
// the OAuth flow hasn't been completed yet AND nothing is configured;
// refresh grants short-circuit in that case.
func (c *HTTPClient) ResolvedTokenURL() string {
	if c == nil {
		return ""
	}
	if p := c.resolvedTokenURL.Load(); p != nil {
		return *p
	}
	return ""
}

// NeedsAuth reports whether the server has signalled a needs-auth
// state via a 401 + Bearer challenge that hasn't yet been resolved
// by an OAuth flow. Used by the engine_adapter to decide whether to
// expose the real tools or a single `mcp__<name>__authenticate`
// pseudo-tool.
func (c *HTTPClient) NeedsAuth() bool { return c.auth.NeedsAuth() }

// AuthChallenge returns the OAuth challenge captured from the 401, or
// nil when the client isn't in needs-auth.
func (c *HTTPClient) AuthChallenge() *OAuthChallenge { return c.auth.Challenge() }

// SetOAuthTokens stores the tokens produced by a completed PKCE flow
// (or refresh grant) and clears the needs-auth state. The
// HTTPClient subsequently decorates every request with
// `Authorization: Bearer <access>`. Caller is responsible for
// triggering Reconnect after this; the real tools won't appear
// until the next Initialize handshake.
//
// resolvedTokenURL is the token endpoint that issued these tokens —
// pass cfg.OAuth.TokenURL for static configs, or the post-discovery
// resolved URL for auto-discovery flows. Empty string is honoured
// (a refresh grant just won't run later) but discouraged.
//
// Tokens are also persisted to ~/.biu/mcp-tokens.json (P20.49c-#2)
// so the next biu start finds them already in place — no second
// browser dance per session. Save failures are non-fatal: the
// in-memory tokens still drive the current session; the user just
// re-auths next time. Failures are silent because the alternative
// (returning an error) would force every Reconnect callsite to
// handle "couldn't write disk" specially when in-memory state is
// already correct.
func (c *HTTPClient) SetOAuthTokens(t OAuthTokens, resolvedTokenURL string) {
	c.auth.SetTokens(t)
	c.healthy.Store(true)
	// Cache the resolved token URL for future refresh grants. Only
	// overwrite when the caller actually has a value — preserve any
	// previously-resolved URL so callers that don't track the spec
	// (e.g. the refresh path itself) don't accidentally clear it.
	if resolvedTokenURL != "" {
		tu := resolvedTokenURL
		c.resolvedTokenURL.Store(&tu)
	}
	if c.cfg.URL != "" {
		authorizeURL := ""
		if c.cfg.OAuth != nil {
			authorizeURL = c.cfg.OAuth.AuthorizeURL
		}
		_ = SaveTokens(c.cfg.Name, c.cfg.URL, authorizeURL, c.ResolvedTokenURL(), t)
	}
}

func (c *HTTPClient) Name() string { return c.cfg.Name }

func (c *HTTPClient) Spec() ClientSpec {
	return ClientSpec{
		Transport: TransportHTTP,
		Command:   c.cfg.URL,
		Args:      nil,
	}
}

// Start is a no-op placeholder for HTTP — the spec doesn't require
// a separate "open the connection" step before the initialize POST.
// Kept on the interface for parity with stdio (which spawns the
// subprocess here) and to give future websocket / SSE-only
// transports a hook.
func (c *HTTPClient) Start(_ context.Context) error { return nil }

// IsHealthy returns false once the underlying transport has
// reported a fatal error (closed by server, repeated 5xx, etc.).
// Per-request errors don't flip this — we want a soft-error per
// call, not whole-registry takedown.
func (c *HTTPClient) IsHealthy() bool { return c.healthy.Load() && !c.closed.Load() }

// Ping issues the MCP `ping` method against the HTTP endpoint.
// Lightweight liveness probe — Streamable HTTP servers respond
// with an empty `result` field. The call goes through the same
// session-aware path() helper so a stale session id flips the
// caller's healthy=false on its own (no need for Ping to unwrap
// the auth-failure semantics).
func (c *HTTPClient) Ping(ctx context.Context) error {
	_, err := c.call(ctx, MethodPing, map[string]any{})
	return err
}

// Reconnect drops the cached session id, flips healthy back to
// true, and re-issues Initialize so the server hands us a fresh
// session. The HTTP transport doesn't have a "subprocess" to
// respawn; the only thing that needs to roll forward is the
// session-id state, since any in-flight request that's failing
// most likely sees a 401 / 410 because the upstream session
// expired or got rotated.
func (c *HTTPClient) Reconnect(ctx context.Context) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: client closed", c.cfg.Name)
	}
	// Drop session id + mark healthy so the next call is treated
	// as a clean handshake rather than a retry on a known-bad
	// session.
	empty := ""
	c.sessionID.Store(&empty)
	c.healthy.Store(true)
	if _, err := c.Initialize(ctx); err != nil {
		c.healthy.Store(false)
		return fmt.Errorf("mcp[%s]: re-initialize: %w", c.cfg.Name, err)
	}
	return nil
}

// Close releases pending streams and marks the client as shut
// down. Subsequent calls fast-fail rather than hanging on the
// already-cancelled context.
func (c *HTTPClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.streams {
		_ = s.Close()
	}
	c.streams = nil
	c.healthy.Store(false)
	return nil
}

// call is the shared POST → JSON or SSE response helper used by
// every JSON-RPC method. Returns the raw JSON-RPC `result` field;
// errors are unwrapped from the JSON-RPC envelope.
func (c *HTTPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: client closed", c.cfg.Name)
	}
	// P20.49c-#4: pre-flight refresh. When the access token is within
	// `refreshSlack` of expiry AND we have a refresh_token + token URL
	// + client_id, fire a refresh grant before sending the request.
	// Best-effort: failures don't block the request; we proceed with
	// the stale token and let the regular 401 → needs-auth flow
	// recover if the server rejects.
	c.refreshIfNeeded(ctx)
	id := c.nextID.Add(1)
	body := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: marshal params: %w", c.cfg.Name, err)
		}
		body.Params = raw
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal request: %w", c.cfg.Name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: build request: %w", c.cfg.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", streamableAcceptHeader)
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	if sid := c.sessionID.Load(); sid != nil && *sid != "" {
		req.Header.Set(sessionHeaderName, *sid)
	}
	// Decorate with the OAuth bearer token once a PKCE flow has
	// completed via the authenticate pseudo-tool. Static
	// cfg.Headers["Authorization"] still wins if the user pinned
	// one explicitly — this branch only fires when the user did NOT
	// pre-configure auth.
	if tokens := c.auth.Tokens(); tokens != nil && req.Header.Get("Authorization") == "" {
		typ := tokens.TokenType
		if typ == "" {
			typ = "Bearer"
		}
		req.Header.Set("Authorization", typ+" "+tokens.AccessToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Transport-level failure — keep the client healthy in case
		// the next call works (transient DNS / TCP), but report.
		return nil, fmt.Errorf("mcp[%s]: %s: %w", c.cfg.Name, method, err)
	}
	defer resp.Body.Close()

	// Capture session id from the server's response if present.
	// Initialize is the typical place this fires; subsequent
	// requests may re-issue if the server rotates sessions.
	if sid := resp.Header.Get(sessionHeaderName); sid != "" {
		v := sid
		c.sessionID.Store(&v)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// 401 with a parseable Bearer challenge → transition to
		// needs-auth instead of sticky-unhealthy. The pseudo-tool
		// surfaced by RegisterEngineTools then drives the OAuth
		// flow and SetOAuthTokens flips us back to healthy.
		//
		// 403 / 401 without a Bearer challenge keep the legacy
		// sticky-unhealthy behaviour — those are typically static
		// API-key misconfigurations the user fixes by editing
		// cfg.Headers["Authorization"], not via a runtime OAuth flow.
		if resp.StatusCode == http.StatusUnauthorized {
			if challenge, ok := parseAuthChallenge(resp); ok {
				c.auth.SetChallenge(challenge)
				return nil, fmt.Errorf("mcp[%s]: %s: %w (%s)",
					c.cfg.Name, method, ErrNeedsAuth, challenge.Raw)
			}
		}
		c.healthy.Store(false)
		return nil, fmt.Errorf("mcp[%s]: %s: %d %s", c.cfg.Name, method, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if resp.StatusCode >= 500 {
		// 5xx is also sticky — most likely the server crashed or is
		// rolling. Caller sees the per-call error; healthy=false
		// surfaces in /mcp and bootstrap diagnostics.
		c.healthy.Store(false)
		return nil, fmt.Errorf("mcp[%s]: %s: %d %s", c.cfg.Name, method, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp[%s]: %s: %d %s", c.cfg.Name, method, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		// Single JSON-RPC envelope — read the body and decode.
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: read body: %w", c.cfg.Name, err)
		}
		var env JSONRPCResponse
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode response: %w", c.cfg.Name, err)
		}
		if env.Error != nil {
			return nil, fmt.Errorf("mcp[%s]: %s: %w", c.cfg.Name, method, env.Error)
		}
		return env.Result, nil
	case strings.HasPrefix(ct, "text/event-stream"):
		// SSE stream: each "data:" line is a JSON-RPC frame. We
		// scan until we find one whose id matches our request,
		// returning its result. Unrelated frames (notifications)
		// are dropped; future work will route them to the engine
		// event channel.
		return c.readSSEResponse(resp.Body, id, method)
	default:
		return nil, fmt.Errorf("mcp[%s]: %s: unexpected content-type %q", c.cfg.Name, method, ct)
	}
}

// readSSEResponse scans an SSE stream for the JSON-RPC response
// matching `wantID`. The MCP spec allows the server to interleave
// notifications (id-less frames) before the response — we drop
// them but keep reading. The first frame matching the request id
// wins; the body is closed by the caller's deferred Body.Close().
func (c *HTTPClient) readSSEResponse(body io.Reader, wantID int64, method string) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// SSE frame format: each event is one or more lines
		// "key: value", terminated by a blank line. We only care
		// about "data:" lines; comments / id / event-name lines
		// are ignored.
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var env JSONRPCResponse
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			// Drop malformed frames — the next data: line might
			// still carry our response. A spec-compliant server
			// won't emit garbage but defensive servers behind
			// flaky proxies sometimes do.
			continue
		}
		// Match on id. Servers MAY send notifications (no id)
		// before the response; skip them.
		if env.ID == nil {
			continue
		}
		matched, err := matchJSONRPCID(env.ID, wantID)
		if err != nil {
			continue
		}
		if !matched {
			continue
		}
		if env.Error != nil {
			return nil, fmt.Errorf("mcp[%s]: %s: %w", c.cfg.Name, method, env.Error)
		}
		return env.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp[%s]: %s: read sse: %w", c.cfg.Name, method, err)
	}
	return nil, fmt.Errorf("mcp[%s]: %s: sse stream ended without response (id=%d)", c.cfg.Name, method, wantID)
}

// matchJSONRPCID compares an unmarshalled JSON-RPC id (which the
// spec allows as int / string / null) against our int64-typed
// outgoing id. Numbers in JSON come through as float64 in Go's
// generic decoder; strings come through as string.
func matchJSONRPCID(got any, want int64) (bool, error) {
	switch v := got.(type) {
	case float64:
		return int64(v) == want, nil
	case int64:
		return v == want, nil
	case string:
		// Some servers stringify numeric ids. Tolerate that — the
		// id round-trip semantics only require equality of the
		// representation server received from us, but defensive
		// is cheap here.
		return v == fmt.Sprintf("%d", want), nil
	default:
		return false, fmt.Errorf("unexpected id type %T", got)
	}
}

// Initialize completes the MCP handshake. Same wire shape as the
// stdio path (different in-flight transport), so the InitializeResult
// returned matches the structure callers see for stdio servers.
func (c *HTTPClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	raw, err := c.call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ClientInfo{Name: "biu", Version: "0.1"},
		Capabilities:    ClientCapabilities{},
	})
	if err != nil {
		return nil, err
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode initialize: %w", c.cfg.Name, err)
	}
	// The spec requires us to send `notifications/initialized`
	// immediately after a successful initialize. Fire-and-forget
	// (no response expected) — we send it as a POST with no id.
	if err := c.notify(ctx, MethodInitialized, nil); err != nil {
		// Non-fatal: some servers don't read the notification and
		// the protocol works anyway. Log via stderr would require
		// a sink we don't have here; tracking via healthy flag is
		// overkill for an advisory step.
		_ = err
	}
	return &res, nil
}

// notify sends a notification (no response expected). Per the
// Streamable HTTP spec, notifications go on the same POST endpoint
// as requests but with no id field; servers reply 202 Accepted.
func (c *HTTPClient) notify(ctx context.Context, method string, params any) error {
	body := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		body.Params = raw
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", streamableAcceptHeader)
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	if sid := c.sessionID.Load(); sid != nil && *sid != "" {
		req.Header.Set(sessionHeaderName, *sid)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// ListTools / CallTool / ListResources / ReadResource / ListPrompts
// / GetPrompt mirror the StdioClient methods one-for-one. The wire
// shape is identical because both transports share protocol.go;
// only the transport layer differs.

func (c *HTTPClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	out := []ToolDef{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodToolsList, params)
		if err != nil {
			return nil, err
		}
		var page ListToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode tools/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	raw, err := c.call(ctx, MethodToolsCall, CallToolParams{
		Name: name, Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	var res CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode tools/call: %w", c.cfg.Name, err)
	}
	return &res, nil
}

func (c *HTTPClient) ListResources(ctx context.Context) ([]Resource, error) {
	out := []Resource{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodResourcesList, params)
		if err != nil {
			return nil, err
		}
		var page ListResourcesResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode resources/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Resources...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *HTTPClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	raw, err := c.call(ctx, MethodResourcesRead, ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	var res ReadResourceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode resources/read: %w", c.cfg.Name, err)
	}
	return &res, nil
}

func (c *HTTPClient) ListPrompts(ctx context.Context) ([]Prompt, error) {
	out := []Prompt{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodPromptsList, params)
		if err != nil {
			return nil, err
		}
		var page ListPromptsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode prompts/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Prompts...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *HTTPClient) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	raw, err := c.call(ctx, MethodPromptsGet, GetPromptParams{
		Name: name, Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	var res GetPromptResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode prompts/get: %w", c.cfg.Name, err)
	}
	return &res, nil
}

// Compile-time assertion that HTTPClient satisfies the Client
// interface. Mirrors the same check on StdioClient.
var _ Client = (*HTTPClient)(nil)
