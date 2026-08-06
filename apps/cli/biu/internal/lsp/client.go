// Package lsp drives Language Server Protocol servers (gopls,
// pyright, typescript-language-server, …) over stdio JSON-RPC and
// exposes a small subset of methods to the LSP tool.
//
// The supported surface is scoped to the 9 operations exposed in the
// tool schema (goToDefinition, findReferences, hover, documentSymbol,
// workspaceSymbol, goToImplementation, prepareCallHierarchy,
// incomingCalls, outgoingCalls).
//
// Design notes:
//
//   * One Pool per session. Servers are spawned lazily on the first
//     request for a file extension and cached for reuse. The pool
//     also tears them down when Close() is called.
//   * Each server speaks LSP-over-stdio with the standard
//     Content-Length-framed JSON-RPC. We don't implement the full
//     spec — only `initialize`, `initialized`, `textDocument/*`, and
//     `shutdown` / `exit`.
//   * All operations are synchronous from the caller's POV: send,
//     wait for response, return. Notifications (e.g. diagnostics) are
//     drained and discarded — the agent doesn't need them today.

package lsp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/biumind/biumind/apps/cli/biu/internal/tools/web"
)

// ServerSpec describes how to spawn one language server. Pool routes
// requests to the right spec by file extension.
type ServerSpec struct {
	Name       string   // friendly label for logs
	Command    string   // executable, looked up in PATH
	Args       []string // CLI args
	Extensions []string // ".go", ".py", ".ts", …
}

// DefaultServers returns a sensible spec for common languages. Empty
// list when no language server is on PATH (Pool soft-errors per
// extension instead of failing wholesale).
func DefaultServers() []ServerSpec {
	candidates := []ServerSpec{
		{Name: "gopls", Command: "gopls", Extensions: []string{".go"}},
		{Name: "pyright", Command: "pyright-langserver",
			Args: []string{"--stdio"}, Extensions: []string{".py"}},
		{Name: "tsserver", Command: "typescript-language-server",
			Args: []string{"--stdio"},
			Extensions: []string{".ts", ".tsx", ".js", ".jsx"}},
	}
	out := []ServerSpec{}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.Command); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// Pool manages a set of language servers, lazily started on demand.
// Concurrency-safe.
type Pool struct {
	specs []ServerSpec
	root  string

	mu      sync.Mutex
	servers map[string]*server // keyed by command name
}

// NewPool returns a configured pool. `root` is the project root sent
// to every server's `initialize` call. Empty specs disable LSP
// entirely; the pool will soft-error per request.
func NewPool(root string, specs []ServerSpec) *Pool {
	return &Pool{
		specs:   specs,
		root:    root,
		servers: map[string]*server{},
	}
}

// LSP implements web.Backend. It picks the right server for the file
// extension, lazily starts it, and forwards the request. Operations
// that don't depend on a file (workspaceSymbol) use the first server.
func (p *Pool) LSP(ctx context.Context, req web.LSPRequest) (any, error) {
	if len(p.specs) == 0 {
		return nil, errors.New("no LSP server configured")
	}
	spec := p.pick(req)
	if spec == nil {
		return nil, fmt.Errorf("no LSP server for %s", filepath.Ext(req.FilePath))
	}
	srv, err := p.serverFor(*spec)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	return srv.dispatch(ctx, req, p.root)
}

func (p *Pool) pick(req web.LSPRequest) *ServerSpec {
	if req.Operation == "workspaceSymbol" || req.FilePath == "" {
		// Use first available server.
		s := p.specs[0]
		return &s
	}
	ext := strings.ToLower(filepath.Ext(req.FilePath))
	for i := range p.specs {
		for _, e := range p.specs[i].Extensions {
			if e == ext {
				return &p.specs[i]
			}
		}
	}
	return nil
}

func (p *Pool) serverFor(spec ServerSpec) (*server, error) {
	p.mu.Lock()
	if s, ok := p.servers[spec.Command]; ok {
		p.mu.Unlock()
		return s, nil
	}
	p.mu.Unlock()

	s, err := startServer(spec, p.root)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if existing, ok := p.servers[spec.Command]; ok {
		// Lost a race — close the duplicate.
		s.close()
		p.mu.Unlock()
		return existing, nil
	}
	p.servers[spec.Command] = s
	p.mu.Unlock()
	return s, nil
}

// Close shuts down every cached server. Non-blocking — the underlying
// processes are sent `shutdown` + `exit` and we return immediately.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.servers {
		s.close()
	}
	p.servers = map[string]*server{}
	return nil
}

// ─── single server ────────────────────────────────────

type server struct {
	spec ServerSpec
	cmd  *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	// docs tracks every file we've sent didOpen for. Keyed by absolute
	// URI; value records the last-seen content hash + LSP version
	// number so dispatch can decide:
	//
	//   * not-tracked → didOpen, version=1
	//   * hash matches → no notification needed
	//   * hash differs → didChange, version++
	//
	// Mutex docMu is separate from `mu` because the JSON-RPC nextID
	// counter is hot-path and we don't want doc bookkeeping
	// contending with it.
	docMu sync.Mutex
	docs  map[string]*docState

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rawResponse
	closed  atomic.Bool
}

type rawResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func startServer(spec ServerSpec, root string) (*server, error) {
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &server{
		spec: spec, cmd: cmd, stdin: stdin,
		stdout: bufio.NewReader(stdout),
		pending: map[int64]chan rawResponse{},
		docs:    map[string]*docState{},
	}
	go s.readLoop()

	// initialize handshake.
	rootURI := pathToURI(root)
	if err := s.requestSync(context.Background(), "initialize", map[string]any{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition":     map[string]any{},
				"references":     map[string]any{},
				"hover":          map[string]any{},
				"documentSymbol": map[string]any{},
				"implementation": map[string]any{},
				"callHierarchy":  map[string]any{},
			},
		},
	}, nil); err != nil {
		s.close()
		return nil, err
	}
	if err := s.notify("initialized", map[string]any{}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *server) close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	_ = s.notify("shutdown", nil)
	_ = s.notify("exit", nil)
	_ = s.stdin.Close()
	_ = s.cmd.Process.Kill()
}

// docState is the per-file bookkeeping the server keeps for incremental
// sync. Hash compares cheap (sha-256 of body); version monotonically
// increments per LSP spec.
type docState struct {
	hash    string
	version int
}

// dispatch maps an LSPRequest to the right LSP method, sends, returns
// the result.
func (s *server) dispatch(ctx context.Context, req web.LSPRequest, root string) (any, error) {
	if req.FilePath != "" {
		s.syncDocument(req.FilePath)
	}

	method, params := buildMethod(req)
	if method == "" {
		return nil, fmt.Errorf("unsupported operation: %s", req.Operation)
	}
	var raw json.RawMessage
	if err := s.requestSync(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return generic, nil
}

// syncDocument keeps the server's view of the file in sync with disk.
// First call ⇒ didOpen. Subsequent calls compare a content hash; if
// it changed (Edit/Write happened externally) we send didChange with
// a bumped version. Identical content ⇒ no-op (cheap path).
//
// Failures to read the file produce no notification — gopls will
// fall back to its own watcher / error gracefully on the request.
func (s *server) syncDocument(path string) {
	uri := pathToURI(path)
	body, err := readSnippet(path)
	if err != nil {
		return
	}
	hash := sha256Hex(body)

	s.docMu.Lock()
	prev, known := s.docs[uri]
	if !known {
		s.docs[uri] = &docState{hash: hash, version: 1}
		s.docMu.Unlock()
		_ = s.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageID(path),
				"version":    1,
				"text":       body,
			},
		})
		return
	}
	if prev.hash == hash {
		s.docMu.Unlock()
		return // unchanged
	}
	prev.version++
	prev.hash = hash
	version := prev.version
	s.docMu.Unlock()
	// Full-text didChange — simpler than range-based incremental sync
	// and gopls accepts it. Mirrors what most LSP clients (vscode,
	// neovim) send when they don't track edit ranges.
	_ = s.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": body},
		},
	})
}

// Touch marks a file dirty so the next dispatch re-reads + diffs it.
// File tools (Edit/Write) call this to invalidate the LSP server's
// cached view. We don't fire didChange immediately because (a) most
// edits don't trigger a downstream LSP query and (b) batching means
// fewer wasted notifications.
func (s *server) Touch(path string) {
	uri := pathToURI(path)
	s.docMu.Lock()
	if doc, ok := s.docs[uri]; ok {
		// Force a hash mismatch on next syncDocument by zeroing the
		// hash. Version bump still happens in syncDocument so the
		// monotonic invariant holds.
		doc.hash = ""
	}
	s.docMu.Unlock()
}

// Touch is the Pool-level entry the file tools call. Iterates over
// every active server and invalidates the path on each — gopls won't
// have it but pyright/tsserver might, and the cost is a single map
// lookup per server.
func (p *Pool) Touch(path string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	servers := make([]*server, 0, len(p.servers))
	for _, s := range p.servers {
		servers = append(servers, s)
	}
	p.mu.Unlock()
	for _, s := range servers {
		s.Touch(path)
	}
}

// sha256Hex is a tiny helper so syncDocument doesn't inline the
// import dance.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// buildMethod converts our request shape into (LSP method, params).
func buildMethod(req web.LSPRequest) (string, map[string]any) {
	uri := pathToURI(req.FilePath)
	pos := map[string]any{
		"line": req.Line - 1, "character": req.Character - 1,
	}
	td := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}
	switch req.Operation {
	case "goToDefinition":
		return "textDocument/definition", td
	case "findReferences":
		return "textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     pos,
			"context":      map[string]any{"includeDeclaration": true},
		}
	case "hover":
		return "textDocument/hover", td
	case "documentSymbol":
		return "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}
	case "workspaceSymbol":
		return "workspace/symbol", map[string]any{"query": req.Query}
	case "goToImplementation":
		return "textDocument/implementation", td
	case "prepareCallHierarchy":
		return "textDocument/prepareCallHierarchy", td
	case "incomingCalls":
		return "callHierarchy/incomingCalls", map[string]any{
			"item": map[string]any{
				"uri": uri, "range": map[string]any{
					"start": pos, "end": pos,
				},
				"selectionRange": map[string]any{
					"start": pos, "end": pos,
				},
			},
		}
	case "outgoingCalls":
		return "callHierarchy/outgoingCalls", map[string]any{
			"item": map[string]any{
				"uri": uri, "range": map[string]any{"start": pos, "end": pos},
				"selectionRange": map[string]any{"start": pos, "end": pos},
			},
		}
	}
	return "", nil
}

// requestSync sends a request and blocks until the server replies.
// `out`, when non-nil, receives the unmarshalled `result` field.
func (s *server) requestSync(ctx context.Context, method string, params any, out *json.RawMessage) error {
	id := atomic.AddInt64(&s.nextID, 1)
	ch := make(chan rawResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("lsp %s: %s (code=%d)",
				method, resp.Error.Message, resp.Error.Code)
		}
		if out != nil {
			*out = resp.Result
		}
		return nil
	}
}

func (s *server) notify(method string, params any) error {
	return s.write(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	})
}

func (s *server) write(payload any) error {
	if s.stdin == nil {
		// Defensive: server was constructed without a real subprocess
		// (tests, post-close races). Silently drop the message — LSP
		// notifications are advisory; nothing the agent does depends
		// on the server actually receiving them.
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.stdin, header); err != nil {
		return err
	}
	_, err = s.stdin.Write(body)
	return err
}

// readLoop consumes Content-Length framed messages forever. Responses
// route to the pending map; notifications are ignored.
func (s *server) readLoop() {
	for {
		msg, err := readFrame(s.stdout)
		if err != nil {
			return
		}
		var raw struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}
		if raw.ID == nil {
			continue // notification — drop
		}
		s.mu.Lock()
		ch := s.pending[*raw.ID]
		delete(s.pending, *raw.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- rawResponse{Result: raw.Result, Error: raw.Error}
		}
	}
}

// readFrame parses one Content-Length framed JSON-RPC payload.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			fmt.Sscanf(line, "Content-Length: %d", &contentLength)
		}
	}
	if contentLength == 0 {
		return nil, errors.New("missing Content-Length header")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// pathToURI returns "file:///abs/path" with proper URL-encoding of the
// path segments. LSP servers reject malformed URIs.
func pathToURI(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	u := url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// languageID maps a file extension to LSP's languageId vocabulary.
func languageID(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	}
	return "plaintext"
}

// readFileBest is best-effort file reading for didOpen. Failures
// return an empty string — the server will diagnose.
func readFileBest(p string) string {
	b, err := readSnippet(p)
	if err != nil {
		return ""
	}
	return b
}

// readSnippet exposes the same logic as a separate function so tests
// can swap in a fixture.
func readSnippet(p string) (string, error) {
	f, err := openReader(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// openReader is the variable seam for tests. Default is os.Open
// behind a tiny adapter.
var openReader = func(p string) (io.ReadCloser, error) {
	return openFileNative(p)
}
