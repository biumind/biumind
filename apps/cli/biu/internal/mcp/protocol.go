// MCP (Model Context Protocol) wire types.
//
// Protocol reference: https://modelcontextprotocol.io
// Spec version: 2024-11-05 (compatible with newer 2025-03-26).
//
// Transport-agnostic types — the actual transport (stdio/SSE/HTTP) is
// implemented in sibling files. Each transport delivers framed
// JSON-RPC 2.0 messages back to the client core.

package mcp

import "encoding/json"

const (
	// Protocol version we negotiate with servers. Servers may respond
	// with a different version; we accept anything >= 2024-11-05.
	ProtocolVersion = "2024-11-05"

	// Methods we send.
	MethodInitialize           = "initialize"
	MethodInitialized          = "notifications/initialized"
	MethodToolsList            = "tools/list"
	MethodToolsCall            = "tools/call"
	MethodResourcesList        = "resources/list"
	MethodResourcesRead        = "resources/read"
	MethodPromptsList          = "prompts/list"
	MethodPromptsGet           = "prompts/get"
	MethodPing                 = "ping"
)

// JSONRPCRequest is the standard JSON-RPC 2.0 request envelope. ID is
// `any` because the spec allows int / string / null. We always send
// int.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"` // always "2.0"
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ─── initialize handshake ─────────────────────────────

type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
	Capabilities    ClientCapabilities `json:"capabilities"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ClientCapabilities struct {
	// Empty for now — biu doesn't expose roots / sampling / prompts back
	// to the server. Add fields when we need them (e.g. roots support).
	Roots    *struct{} `json:"roots,omitempty"`
	Sampling *struct{} `json:"sampling,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	Instructions    string             `json:"instructions,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Logging   *struct{}            `json:"logging,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ─── tools ────────────────────────────────────────────

type ListToolsResult struct {
	Tools      []ToolDef `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// ToolDef is the server's declaration of a single tool. Mirrors the
// shape the LLM would see when this tool is registered.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]any         `json:"inputSchema"`
	Annotations map[string]any         `json:"annotations,omitempty"`
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is a typed payload chunk in a tool result. MCP defines:
//   text, image, resource (reference), audio (since 2025-03)
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"` // base64 for image/audio
	MimeType string          `json:"mimeType,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// ─── resources ────────────────────────────────────────
//
// MCP resources are read-only data handles a server exposes — files,
// API responses, search-index snapshots, anything URI-addressable.
// The model lists them via resources/list and fetches one via
// resources/read. Distinct from tools (which are RPC actions).

// Resource is a single resource handle as advertised by a server. The
// shape mirrors the MCP spec resource object verbatim — uri is the
// stable identifier the model passes to resources/read.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ListResourcesResult is the resources/list response. Servers may
// page just like tools/list; we follow the same NextCursor loop.
type ListResourcesResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// ReadResourceParams is the resources/read request body.
type ReadResourceParams struct {
	URI string `json:"uri"`
}

// ReadResourceResult is the resources/read response: an array of
// content envelopes (text or blob) — most resources return a single
// entry, but the spec allows multi-part content (e.g. a directory
// listing returning each file).
type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ResourceContent is one envelope inside a ReadResourceResult.
// Either Text (UTF-8 payload) or Blob (base64) is set — never both.
// MimeType is optional but typically present for blob payloads so
// the model can decide how to interpret them.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// ─── prompts ──────────────────────────────────────────
//
// MCP prompts are server-defined message templates the model can
// invoke by name with optional arguments. Distinct from resources
// (read-only data) and tools (RPC actions): prompts are *content*
// the server pre-composes and returns as a sequence of role/content
// messages, which the caller then folds into its conversation.
//
// Typical use: a documentation MCP server might expose a
// `summarise_changelog` prompt that takes a `version` argument and
// returns a `[user]` message asking the model to summarise the
// release notes for that version.

// Prompt is a single prompt template the server advertises. The
// arguments slice describes inputs the caller can provide; servers
// may render the prompt with no arguments when none are listed.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes one parameter on a prompt template.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ListPromptsResult is the prompts/list response. Pages via cursor
// just like tools/list.
type ListPromptsResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

// GetPromptParams is the prompts/get request body.
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult is the prompts/get response: a description plus
// an ordered sequence of messages ready to inject into a conversation.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage is one message inside a GetPromptResult. role is
// "user" or "assistant"; content reuses the same ContentBlock shape
// MCP tool results emit so renderers / aggregators don't need a
// second type.
type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}

// flattenText returns just the text content of a tool result, useful
// for surfacing in a CLI/REPL where image/audio content can't render.
// Non-text blocks become "[image: png 12kb]" placeholders.
func (r *CallToolResult) FlattenText() string {
	out := ""
	for i, b := range r.Content {
		if i > 0 {
			out += "\n"
		}
		switch b.Type {
		case "text":
			out += b.Text
		case "image":
			out += "[image: " + b.MimeType + "]"
		case "audio":
			out += "[audio: " + b.MimeType + "]"
		case "resource":
			out += "[resource]"
		default:
			out += "[" + b.Type + "]"
		}
	}
	return out
}
