//go:build integration

// Package mcpfixture exposes an in-process Streamable HTTP MCP server
// for biu's Layer I integration tests. Wire-compatible with biu's
// internal/mcp/http.go client (2025-03-26 spec subset):
//
//   POST  /     JSONRPCRequest  → application/json or text/event-stream
//   GET   /     (server-push)   → not implemented; biu doesn't issue these
//   DELETE /    end session     → 204
//
// The fixture honours the Mcp-Session-Id header — `initialize`
// allocates a fresh id, subsequent requests must echo it back.
// Mismatches return 404 so we can exercise biu's session-rotation
// path in I10.

package mcpfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// Toolset names the canned tool sets the fixture can advertise.
type Toolset int

const (
	// ToolsetDefault: one echo tool.
	ToolsetDefault Toolset = iota
	// ToolsetExtended: echo + upper.
	ToolsetExtended
	// ToolsetMinimal: zero tools (used by I5 catalog-diff).
	ToolsetMinimal
)

// Options configure a fixture HTTP server. Zero value = sensible
// defaults (default toolset, no auth, valid sessions).
type Options struct {
	Toolset    Toolset
	AuthToken  string // when non-empty, fixture rejects requests without `Authorization: Bearer <token>`.
	StreamSSE  bool   // when true, responses go out as text/event-stream instead of application/json.
	InitFail   bool   // initialize replies with JSON-RPC error.
	ExpireAt   int64  // when >0: after this many requests, return 404 on session lookup (forces reconnect).
	OnRequest  func(method string)            // optional hook called per method invocation; tests use it to assert ordering.
	OnSessionExpired func()                   // fired once after ExpireAt trips.
}

// Server is the running fixture wrapped around an httptest.Server.
type Server struct {
	HTTP *httptest.Server

	mu        sync.Mutex
	sessions  map[string]bool
	requests  atomic.Int64
	expiredID atomic.Pointer[string]
	opt       Options
}

// Start spins up the fixture on a random port. Caller must Close().
func Start(opt Options) *Server {
	s := &Server{
		sessions: map[string]bool{},
		opt:      opt,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.HTTP = httptest.NewServer(mux)
	return s
}

// Close shuts the fixture down.
func (s *Server) Close() { s.HTTP.Close() }

// URL is the fixture's base URL — used to build the [[mcp_servers]]
// entry seeded into config.toml.
func (s *Server) URL() string { return s.HTTP.URL }

// SetToolset live-changes the advertised toolset. Used by I5 to
// shrink/grow the catalog mid-test so the catalog-diff path fires.
func (s *Server) SetToolset(t Toolset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opt.Toolset = t
}

// jsonrpcReq is what biu's HTTP client posts.
type jsonrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResp is what the fixture writes back.
type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcErr     `json:"error,omitempty"`
}

type jsonrpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// Auth gate.
	if s.opt.AuthToken != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.opt.AuthToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	switch r.Method {
	case http.MethodDelete:
		// Close session — biu's client doesn't routinely call this
		// but it's part of the spec.
		w.WriteHeader(http.StatusNoContent)
		return

	case http.MethodPost:
		// fall through
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req jsonrpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if s.opt.OnRequest != nil {
		s.opt.OnRequest(req.Method)
	}

	// Notifications carry no id and need no response.
	notification := strings.HasPrefix(req.Method, "notifications/")

	// Session id check — initialize allocates one; everything else
	// must echo it.
	clientSID := r.Header.Get("Mcp-Session-Id")
	if req.Method == "initialize" {
		// Allocate a fresh id and echo it back via header.
		sid := uuid.NewString()
		s.mu.Lock()
		s.sessions[sid] = true
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", sid)
	} else if !notification {
		s.mu.Lock()
		ok := clientSID != "" && s.sessions[clientSID]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Expire after N requests (I10 path).
		if s.opt.ExpireAt > 0 {
			n := s.requests.Add(1)
			if n >= s.opt.ExpireAt {
				exp := s.expiredID.Swap(&clientSID)
				if exp == nil && s.opt.OnSessionExpired != nil {
					s.opt.OnSessionExpired()
				}
				s.mu.Lock()
				delete(s.sessions, clientSID)
				s.mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
	}

	if notification {
		// Per spec: 202 Accepted with empty body.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.respond(req)

	if s.opt.StreamSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// respond builds the JSON-RPC reply for one method.
func (s *Server) respond(req jsonrpcReq) jsonrpcResp {
	r := jsonrpcResp{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		if s.opt.InitFail {
			r.Error = &jsonrpcErr{Code: -32603, Message: "forced init failure"}
			return r
		}
		r.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "fake-http", "version": "0.1"},
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}},
		}
	case "ping":
		r.Result = map[string]any{}
	case "tools/list":
		r.Result = map[string]any{"tools": s.toolsList()}
	case "tools/call":
		r.Result = s.callTool(req.Params)
	case "resources/list":
		r.Result = map[string]any{"resources": []map[string]any{
			{"uri": "fake-http://docs/readme", "name": "readme", "description": "Fake README", "mimeType": "text/plain"},
		}}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		r.Result = map[string]any{"contents": []map[string]any{
			{"uri": p.URI, "mimeType": "text/plain", "text": "FIXTURE-HTTP-BODY-ZX9K"},
		}}
	case "prompts/list":
		r.Result = map[string]any{"prompts": []map[string]any{
			{"name": "greet", "description": "greets a person", "arguments": []map[string]any{{"name": "name", "required": true}}},
		}}
	case "prompts/get":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		who, _ := p.Arguments["name"].(string)
		if who == "" {
			who = "friend"
		}
		r.Result = map[string]any{
			"description": "greets",
			"messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": "Greet " + who + " enthusiastically."}},
			},
		}
	default:
		r.Error = &jsonrpcErr{Code: -32601, Message: "method not implemented in fixture: " + req.Method}
	}
	return r
}

func (s *Server) toolsList() []map[string]any {
	s.mu.Lock()
	t := s.opt.Toolset
	s.mu.Unlock()
	switch t {
	case ToolsetExtended:
		return []map[string]any{
			{"name": "echo", "description": "echoes its input", "inputSchema": map[string]any{
				"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}, "required": []string{"msg"},
			}},
			{"name": "upper", "description": "uppercases the input", "inputSchema": map[string]any{
				"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"},
			}},
		}
	case ToolsetMinimal:
		return []map[string]any{}
	default:
		return []map[string]any{
			{"name": "echo", "description": "echoes its input", "inputSchema": map[string]any{
				"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}, "required": []string{"msg"},
			}},
		}
	}
}

func (s *Server) callTool(params json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	switch p.Name {
	case "echo":
		msg, _ := p.Arguments["msg"].(string)
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "echoed: " + msg}}}
	case "upper":
		txt, _ := p.Arguments["text"].(string)
		return map[string]any{"content": []map[string]any{{"type": "text", "text": strings.ToUpper(txt)}}}
	default:
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "unknown tool: " + p.Name}}, "isError": true}
	}
}
