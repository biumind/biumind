// Transport-agnostic client surface for the MCP Registry. Each
// transport (stdio, Streamable HTTP, in-process — when we add it)
// implements Client; the Registry stores a `map[string]Client` and
// dispatches every protocol method through this interface so adding
// a new transport doesn't ripple into engine_adapter / resource_tools
// / prompt_tools.
//
// Why we didn't just keep returning `*StdioClient`: the bootstrap
// path and the REPL `/mcp` slash both need to enumerate connected
// servers without knowing whether each one is a subprocess or an
// HTTP endpoint. Once HTTP transport landed, the registry's storage
// became the natural seam.

package mcp

import "context"

// Transport names the wire protocol a Client speaks. Surfaced on
// ServerStatus so REPL renderers can label rows (e.g. "✓ docs
// [http]"). Stable enum — adding a new transport requires a new
// constant, not a string-typed field with arbitrary values.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http" // MCP Streamable HTTP (2025-03-26 spec)
)

// ClientSpec is the transport-flavoured summary every Client emits
// for the REPL `/mcp` listing. The (Command, Args) fields keep the
// shape ServerStatus already exposed; HTTP clients fill Command
// with the endpoint URL and leave Args empty so existing renderers
// keep working.
type ClientSpec struct {
	Transport Transport
	Command   string   // stdio: executable path. http: endpoint URL.
	Args      []string // stdio: argv tail. http: nil.
}

// Client is the surface every MCP transport must implement. Methods
// match the JSON-RPC verbs biu uses today; new verbs land here as
// the spec evolves (resources/templates, prompts/list_changed
// subscriptions, …).
//
// All methods take a context.Context and respect cancellation. The
// Close method is idempotent — calling it twice on the same client
// must not panic, since the Registry's defer chains can race during
// shutdown.
type Client interface {
	// Name is the registry-side identifier. Same string the user
	// configured under [mcp_servers].name; not the server's
	// self-advertised serverInfo.name.
	Name() string

	// Spec returns transport metadata for status rendering.
	Spec() ClientSpec

	// Start brings the underlying transport online: stdio spawns
	// the subprocess; HTTP performs the initial session-ID
	// negotiation. Idempotent only when already started — calling
	// twice on a fresh client may double-spawn / double-connect,
	// so callers go through Registry.Connect / connectClient.
	Start(ctx context.Context) error

	// Initialize completes the MCP handshake and returns the
	// server's self-advertised info + capabilities. Must be
	// called after Start; the bootstrap path does this
	// immediately after spawning / connecting.
	Initialize(ctx context.Context) (*InitializeResult, error)

	// ListTools / CallTool — required for every server.
	ListTools(ctx context.Context) ([]ToolDef, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error)

	// ListResources / ReadResource — only meaningful when the
	// server advertises the Resources capability. Servers without
	// it should return a JSON-RPC method-not-found error so the
	// caller can surface "this server doesn't expose resources"
	// rather than hang.
	ListResources(ctx context.Context) ([]Resource, error)
	ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error)

	// ListPrompts / GetPrompt — same conditional contract as
	// resources, gated on the Prompts capability.
	ListPrompts(ctx context.Context) ([]Prompt, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error)

	// IsHealthy returns true while the connection is usable. Used
	// by the REPL status bar + bootstrap diagnostics; the registry
	// itself doesn't gate on it (calls fail loudly on unhealthy
	// transports rather than failing fast).
	IsHealthy() bool

	// Ping sends the protocol-level `ping` request. Returns nil
	// when the server responded (server is alive); error when
	// the round-trip failed. The HealthMonitor uses this as its
	// liveness probe — cheaper than tools/list and standardised
	// by the MCP spec.
	Ping(ctx context.Context) error

	// Reconnect tears down the underlying transport and brings it
	// back up. For stdio, that means SIGKILL the old subprocess
	// and re-fork. For HTTP, drop the cached session id and re-
	// initialize. After a successful Reconnect the caller must
	// also re-list tools (via Initialize + ListTools) since the
	// server may have changed catalogs across the restart.
	//
	// Idempotent on a healthy connection: reconnecting an already-
	// alive client is allowed and just refreshes state.
	Reconnect(ctx context.Context) error

	// Close releases the transport. Stdio kills the subprocess;
	// HTTP terminates the SSE stream / cancels in-flight requests.
	// Idempotent.
	Close() error
}

// Compile-time assertion that StdioClient implements Client. If a
// future refactor breaks the surface, this line fails to compile —
// far better than discovering it at runtime when registry.Connect
// tries to store a non-conforming type.
var _ Client = (*StdioClient)(nil)
