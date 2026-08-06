// Tests for the Streamable HTTP transport. Three layers of
// coverage:
//
//   1. Pure-Go fixture (httptest.Server) — drives every HTTPClient
//      method against an in-process server we fully control. Fast,
//      deterministic, no language dependency.
//   2. Mixed-mode test — registry holds stdio + http simultaneously
//      and resource fan-out aggregates across both transports.
//   3. SSE branch — server replies with text/event-stream instead
//      of inline JSON; HTTPClient must scan for the matching id
//      while ignoring leading notification frames.
//
// The cross-language path (real Python aiohttp / Node http server
// driving the HTTP transport) lives in crosslang_http_test.go so
// this file stays runnable on machines without those interpreters.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// httpFixture is an httptest.Server that speaks Streamable HTTP
// MCP. Callers tweak its handlers via the SetXxx fields before
// starting; defaults handle initialize + tools/list + tools/call so
// most tests don't override anything.
type httpFixture struct {
	*httptest.Server
	requireSession bool // emit Mcp-Session-Id and require it on follow-up POSTs
	sessionID      string

	// requestCount counts every POST received — exposed for tests
	// that assert "we issued exactly N requests".
	requestCount atomic.Int64
}

// newJSONRPCFixture constructs the default in-process server. Each
// test customises behaviour by wrapping the returned http.Handler.
func newJSONRPCFixture(t *testing.T, handler http.HandlerFunc) *httpFixture {
	t.Helper()
	f := &httpFixture{
		sessionID: "session-abc",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.requestCount.Add(1)
		// Spec contract: every POST must Accept both JSON and SSE.
		// Reject 406 if the client misses the Accept header.
		if r.Method == http.MethodPost {
			accept := r.Header.Get("Accept")
			if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
				http.Error(w, "missing dual Accept header", http.StatusNotAcceptable)
				return
			}
		}
		handler(w, r)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// writeJSON is a helper that emits a JSON-RPC envelope as
// application/json (the inline branch). Optional sessionID is set
// on the response header when non-empty.
func writeJSON(w http.ResponseWriter, sessionID string, env JSONRPCResponse) {
	if sessionID != "" {
		w.Header().Set(sessionHeaderName, sessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

// writeSSE emits one or more JSON-RPC frames as an SSE stream. The
// last frame typically carries the response; earlier frames may be
// notifications (id-less) to verify the scanner's filter.
func writeSSE(w http.ResponseWriter, sessionID string, frames ...JSONRPCResponse) {
	if sessionID != "" {
		w.Header().Set(sessionHeaderName, sessionID)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, f := range frames {
		buf, _ := json.Marshal(f)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", buf)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// decodeRequest pulls the JSON-RPC request out of the POST body so
// the fixture can dispatch on method + id.
func decodeRequest(t *testing.T, r *http.Request) JSONRPCRequest {
	t.Helper()
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return req
}

// idOf returns the JSON-RPC id from a decoded request as int64.
// JSON numbers come through Go's decoder as float64.
func idOf(t *testing.T, req JSONRPCRequest) int64 {
	t.Helper()
	switch v := req.ID.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case nil:
		return 0 // notifications
	default:
		t.Fatalf("unexpected id type %T", req.ID)
		return 0
	}
}

// ─── Handshake + happy path ───────────────────────────

func TestHTTPInitializeRoundTrip(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "session-1", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "fixture", Version: "0.1"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			// Notifications return 202 with empty body. Some servers
			// return 200; either is fine per the spec.
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "unexpected: "+req.Method, http.StatusNotImplemented)
		}
	})

	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "fixture" {
		t.Errorf("server info wrong: %+v", res)
	}
	// Session id should have been captured for the next POST.
	if sid := c.sessionID.Load(); sid == nil || *sid != "session-1" {
		t.Errorf("session id not captured")
	}
}

func TestHTTPListAndCallToolsInline(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "fixture"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodToolsList:
			writeJSON(w, "", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, ListToolsResult{
					Tools: []ToolDef{{
						Name: "echo", Description: "echo input",
						InputSchema: map[string]any{"type": "object"},
					}},
				}),
			})
		case MethodToolsCall:
			var params CallToolParams
			_ = json.Unmarshal(req.Params, &params)
			writeJSON(w, "", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, CallToolResult{
					Content: []ContentBlock{{Type: "text",
						Text: "echoed: " + fmt.Sprint(params.Arguments["msg"])}},
				}),
			})
		default:
			http.Error(w, "unexpected", http.StatusNotImplemented)
		}
	})

	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools wrong: %+v", tools)
	}
	res, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FlattenText() != "echoed: hi" {
		t.Errorf("call result: %q", res.FlattenText())
	}
}

// ─── SSE response branch ──────────────────────────────

// SSE response stream where the response is preceded by a couple
// of notification frames (no id). HTTPClient must skip those and
// pick up the matching response.
func TestHTTPSSEResponseSkipsNotifications(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "fixture"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodToolsCall:
			// SSE payload: two notifications first (id-less), then
			// the actual response. Scanner must skip the noise.
			writeSSE(w, "",
				JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"hint":"warming up"}`)},
				JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"hint":"running"}`)},
				JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
					Result: mustMarshal(t, CallToolResult{
						Content: []ContentBlock{{Type: "text", Text: "sse-result"}},
					}),
				},
			)
		case MethodToolsList:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, ListToolsResult{Tools: []ToolDef{{
					Name: "tool", InputSchema: map[string]any{"type": "object"},
				}}}),
			})
		default:
			http.Error(w, "unexpected", http.StatusNotImplemented)
		}
	})

	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := c.CallTool(ctx, "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FlattenText() != "sse-result" {
		t.Errorf("sse result body: %q", res.FlattenText())
	}
}

// ─── Failure modes ────────────────────────────────────

// 401/403 mark the client unhealthy because the server is
// definitively rejecting us — not a transient network glitch.
// Subsequent calls should fast-fail instead of probing further.
func TestHTTPAuthFailureMarksUnhealthy(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no auth", http.StatusUnauthorized)
	})
	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	ctx := context.Background()
	if _, err := c.Initialize(ctx); err == nil {
		t.Fatal("expected auth error")
	}
	if c.IsHealthy() {
		t.Errorf("auth failure should mark client unhealthy")
	}
}

// JSON-RPC error envelope (well-formed protocol error, e.g. method
// not found) surfaces as a Go error but does NOT mark unhealthy —
// the next call might use a method the server does support.
func TestHTTPMethodNotFoundDoesNotKillHealth(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		writeJSON(w, "", JSONRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &JSONRPCError{Code: -32601, Message: "method not found"},
		})
	})
	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	if _, err := c.ListTools(context.Background()); err == nil {
		t.Error("expected error for method-not-found")
	}
	if !c.IsHealthy() {
		t.Errorf("protocol-level error should not kill health")
	}
}

// Spec compliance: every POST must include the dual Accept header.
// The fixture itself enforces 406, so a successful call here also
// proves HTTPClient sets the header. We test it explicitly so a
// future refactor that drops the header fails loudly.
func TestHTTPClientSetsDualAccept(t *testing.T) {
	gotAccept := ""
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		req := decodeRequest(t, r)
		writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: mustMarshal(t, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo:      ServerInfo{Name: "f"},
			}),
		})
	})
	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotAccept, "application/json") || !strings.Contains(gotAccept, "text/event-stream") {
		t.Errorf("Accept header missing dual: %q", gotAccept)
	}
}

// Custom Headers (Authorization etc.) flow through every request.
func TestHTTPHeadersPassedThrough(t *testing.T) {
	gotAuth := ""
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		req := decodeRequest(t, r)
		writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Result: mustMarshal(t, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo:      ServerInfo{Name: "f"},
			}),
		})
	})
	c := NewHTTP(HTTPConfig{
		Name: "fix", URL: f.URL + "/mcp",
		Headers: map[string]string{"Authorization": "Bearer token-abc"},
	})
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer token-abc" {
		t.Errorf("Authorization not forwarded: %q", gotAuth)
	}
}

// Session id captured during initialize MUST be echoed on every
// subsequent POST so a stateful server can correlate requests.
func TestHTTPSessionIDEchoedOnFollowups(t *testing.T) {
	gotSession := ""
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "session-xyz", JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "f"},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodToolsList:
			gotSession = r.Header.Get(sessionHeaderName)
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, ListToolsResult{}),
			})
		}
	})
	c := NewHTTP(HTTPConfig{Name: "fix", URL: f.URL + "/mcp"})
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotSession != "session-xyz" {
		t.Errorf("session id not echoed: %q", gotSession)
	}
}

// ─── Registry integration ─────────────────────────────

func TestRegistryConnectHTTP(t *testing.T) {
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "remote"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodToolsList:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, ListToolsResult{Tools: []ToolDef{{
					Name: "ping", InputSchema: map[string]any{"type": "object"},
				}}}),
			})
		case MethodToolsCall:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, CallToolResult{
					Content: []ContentBlock{{Type: "text", Text: "pong"}},
				}),
			})
		default:
			http.Error(w, "unexpected", http.StatusNotImplemented)
		}
	})
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.ConnectHTTP(ctx, HTTPConfig{
		Name: "remote", URL: f.URL + "/mcp",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Tool registered under namespaced name.
	res, err := r.Call(ctx, QualifyName("remote", "ping"), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.FlattenText() != "pong" {
		t.Errorf("call result: %q", res.FlattenText())
	}
	// Servers() reports transport.
	servers := r.Servers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Transport != TransportHTTP {
		t.Errorf("transport label wrong: %q", servers[0].Transport)
	}
	if servers[0].Command != f.URL+"/mcp" {
		t.Errorf("URL not in Command field: %q", servers[0].Command)
	}
}

// Mixed-mode test: a Registry with one stdio server and one HTTP
// server. Both must coexist; tool fan-out, listings, and calls
// must work for both transports through the same registry.
func TestRegistryMixedStdioAndHTTP(t *testing.T) {
	if !hasShellForFixture(t) {
		return
	}
	// Stdio side: minimal server (just initialize + tools/list +
	// tools/call) using the existing fakeServer fixture from
	// stdio_test.go. Tools live in the parent test; we just spawn
	// it.
	stdioPath := writeFakeServer(t)

	// HTTP side: the same minimal MCP behaviour over httptest.
	f := newJSONRPCFixture(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case MethodInitialize:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "http-fix"},
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				}),
			})
		case MethodInitialized:
			w.WriteHeader(http.StatusAccepted)
		case MethodToolsList:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, ListToolsResult{Tools: []ToolDef{{
					Name: "remote", InputSchema: map[string]any{"type": "object"},
				}}}),
			})
		case MethodToolsCall:
			writeJSON(w, "", JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: mustMarshal(t, CallToolResult{
					Content: []ContentBlock{{Type: "text", Text: "from-http"}},
				}),
			})
		default:
			http.Error(w, "unexpected", http.StatusNotImplemented)
		}
	})

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Connect(ctx, StdioConfig{
		Name: "local", Command: "/bin/sh", Args: []string{stdioPath},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.ConnectHTTP(ctx, HTTPConfig{
		Name: "remote", URL: f.URL + "/mcp",
	}); err != nil {
		t.Fatal(err)
	}

	// Both servers registered.
	servers := r.Servers()
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	transports := map[string]Transport{}
	for _, s := range servers {
		transports[s.Name] = s.Transport
	}
	if transports["local"] != TransportStdio {
		t.Errorf("local should be stdio: %v", transports)
	}
	if transports["remote"] != TransportHTTP {
		t.Errorf("remote should be http: %v", transports)
	}

	// Tools from BOTH servers reachable via the same registry.
	if _, err := r.Call(ctx, QualifyName("local", "echo"), map[string]any{"msg": "hello"}); err != nil {
		t.Errorf("stdio call: %v", err)
	}
	res, err := r.Call(ctx, QualifyName("remote", "remote"), nil)
	if err != nil {
		t.Errorf("http call: %v", err)
	}
	if res != nil && res.FlattenText() != "from-http" {
		t.Errorf("http call result: %q", res.FlattenText())
	}
}

// hasShellForFixture skips the test when /bin/sh isn't available.
// Mixed-mode tests that drive a stdio fixture need a POSIX shell;
// Windows runners short-circuit here.
func hasShellForFixture(t *testing.T) bool {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this platform")
		return false
	}
	return true
}

// mustMarshal is a small helper. JSON encoding errors mid-test
// would be a fixture bug, not a SUT bug — fail loudly.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}
