// Cross-language end-to-end tests for the Streamable HTTP MCP
// transport. Same shape as crosslang_test.go (which covers stdio):
// a Python and a Node fixture server are spawned as subprocesses,
// listen on a free TCP port, and biu's HTTPClient + Registry drive
// them through the full handshake + tools/resources/prompts surface.
//
// The fixture servers use ONLY the standard library on each side
// (no aiohttp / express / npm / pip) so the test suite is
// dependency-free. Each server prints its bound port to stdout on
// startup; the Go test reads that line, builds the URL, and runs
// the assertions.
//
// Skip behaviour mirrors crosslang_test.go: missing python3 / node
// → t.Skip, Windows → t.Skip the whole file's tests.

package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// pythonHTTPServerSource is a Streamable HTTP MCP server using only
// http.server from the stdlib. Listens on a random port (port=0),
// prints "PORT=N\n" to stdout so the parent can connect, then
// services every POST as one MCP method dispatch. Identical
// capability surface to pythonServerSource (echo + add tools, hello
// + logo resources, summarise + greet prompts) for cross-transport
// parity assertions.
//
// Implementation notes worth carrying forward:
//
//   - We use http.server.HTTPServer + a custom handler. POST is
//     handled inline; GET (which the spec reserves for long-lived
//     SSE notifications) returns 405 since this fixture doesn't
//     ship server-initiated frames.
//   - The handler reads the JSON-RPC body, dispatches on method,
//     and replies as application/json (the inline branch). SSE
//     replies are exercised by the `_test.go` httptest fixtures —
//     covering them in this slow integration suite would just
//     duplicate that coverage.
//   - sys.stdout is unbuffered so the parent test sees the PORT=
//     line promptly (otherwise we'd race with HTTPServer.serve()
//     blocking before the print flushes).
const pythonHTTPServerSource = `#!/usr/bin/env python3
import base64
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

sys.stdout.reconfigure(line_buffering=True)

class Handler(BaseHTTPRequestHandler):
    # Quiet down — stdlib's default access log noise interleaves
    # with the parent test's stderr scan and would confuse skip
    # detection. We don't need access logs for a 1-min test.
    def log_message(self, *_args, **_kwargs):
        pass

    def do_POST(self):
        accept = self.headers.get("Accept", "")
        if "application/json" not in accept or "text/event-stream" not in accept:
            self.send_error(406, "Not Acceptable: dual Accept required")
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            req = json.loads(body)
        except json.JSONDecodeError:
            self.send_error(400, "bad json")
            return
        method = req.get("method", "")
        params = req.get("params", {}) or {}
        rid = req.get("id")
        if method == "initialize":
            self._reply(rid, {
                "protocolVersion": "2024-11-05",
                "serverInfo": {"name": "py-http", "version": "0.1"},
                "capabilities": {"tools": {}, "resources": {}, "prompts": {}},
            })
        elif method == "notifications/initialized":
            self.send_response(202)
            self.end_headers()
        elif method == "tools/list":
            self._reply(rid, {"tools": [
                {"name": "echo", "description": "echo input",
                 "inputSchema": {"type": "object",
                    "properties": {"msg": {"type": "string"}},
                    "required": ["msg"]}},
                {"name": "add", "description": "sum two ints",
                 "inputSchema": {"type": "object",
                    "properties": {"a": {"type":"integer"}, "b": {"type":"integer"}},
                    "required": ["a","b"]}},
            ]})
        elif method == "tools/call":
            name = params.get("name")
            args = params.get("arguments") or {}
            if name == "echo":
                self._reply(rid, {"content": [
                    {"type": "text", "text": "ECHO: " + args.get("msg", "")},
                ]})
            elif name == "add":
                total = int(args.get("a", 0)) + int(args.get("b", 0))
                self._reply(rid, {"content": [
                    {"type": "text", "text": "SUM: " + str(total)},
                ]})
            else:
                self._error(rid, -32601, "unknown tool")
        elif method == "resources/list":
            self._reply(rid, {"resources": [
                {"uri": "data://hello", "name": "hello.txt", "mimeType": "text/plain", "description": "greeting"},
                {"uri": "data://logo", "name": "logo.png", "mimeType": "image/png"},
            ]})
        elif method == "resources/read":
            uri = params.get("uri", "")
            if uri == "data://hello":
                self._reply(rid, {"contents": [
                    {"uri": uri, "mimeType": "text/plain", "text": "hello world"},
                ]})
            elif uri == "data://logo":
                blob = base64.b64encode(b"\x89PNG\r\n\x1a\n").decode("ascii")
                self._reply(rid, {"contents": [
                    {"uri": uri, "mimeType": "image/png", "blob": blob},
                ]})
            else:
                self._error(rid, -32602, "unknown uri")
        elif method == "prompts/list":
            self._reply(rid, {"prompts": [
                {"name": "summarise", "description": "Summarise"},
                {"name": "greet", "description": "Greet someone",
                 "arguments": [{"name": "who", "required": True}]},
            ]})
        elif method == "prompts/get":
            name = params.get("name")
            args = params.get("arguments") or {}
            if name == "summarise":
                self._reply(rid, {"description": "Summary", "messages": [
                    {"role": "user", "content": {"type": "text", "text": "Please summarise"}},
                ]})
            elif name == "greet":
                self._reply(rid, {"description": "Greeting", "messages": [
                    {"role": "user", "content": {"type": "text",
                        "text": "Hello " + args.get("who", "stranger")}},
                ]})
            else:
                self._error(rid, -32602, "unknown prompt")
        else:
            self._error(rid, -32601, "method not found: " + method)

    def do_GET(self):
        # No long-lived SSE channel in this fixture.
        self.send_error(405, "Method Not Allowed")

    def _reply(self, rid, result):
        body = json.dumps({"jsonrpc": "2.0", "id": rid, "result": result}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _error(self, rid, code, msg):
        body = json.dumps({"jsonrpc": "2.0", "id": rid,
                           "error": {"code": code, "message": msg}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

server = HTTPServer(("127.0.0.1", 0), Handler)
print("PORT=" + str(server.server_address[1]))
server.serve_forever()
`

// nodeHTTPServerSource: same MCP surface as the Python fixture, but
// in plain Node using the stdlib http module. No npm install
// required.
//
// Quirks:
//
//   - Node's http server sets Connection: close by default for
//     HTTP/1.0; some MCP clients would reuse the connection. We
//     don't pin keep-alive — the test isn't load-sensitive enough
//     to care.
//   - res.end() must be called explicitly; failing to call it
//     leaves the client hanging on the read.
const nodeHTTPServerSource = `#!/usr/bin/env node
const http = require('http');

function reply(res, rid, result) {
  const body = JSON.stringify({ jsonrpc: '2.0', id: rid, result });
  res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
  res.end(body);
}
function error(res, rid, code, message) {
  const body = JSON.stringify({ jsonrpc: '2.0', id: rid, error: { code, message } });
  res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
  res.end(body);
}

const server = http.createServer((req, res) => {
  if (req.method === 'GET') {
    res.writeHead(405); return res.end('Method Not Allowed');
  }
  if (req.method !== 'POST') {
    res.writeHead(405); return res.end();
  }
  const accept = req.headers.accept || '';
  if (!accept.includes('application/json') || !accept.includes('text/event-stream')) {
    res.writeHead(406); return res.end('Not Acceptable: dual Accept required');
  }

  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    let parsed;
    try { parsed = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
    catch (_) { res.writeHead(400); return res.end('bad json'); }
    const { method, params = {}, id: rid } = parsed;

    switch (method) {
      case 'initialize':
        return reply(res, rid, {
          protocolVersion: '2024-11-05',
          serverInfo: { name: 'node-http', version: '0.1' },
          capabilities: { tools: {}, resources: {}, prompts: {} },
        });
      case 'notifications/initialized':
        res.writeHead(202); return res.end();
      case 'tools/list':
        return reply(res, rid, { tools: [
          { name: 'echo', description: 'echo input',
            inputSchema: { type: 'object',
              properties: { msg: { type: 'string' } }, required: ['msg'] } },
          { name: 'add', description: 'sum two ints',
            inputSchema: { type: 'object',
              properties: { a: { type: 'integer' }, b: { type: 'integer' } }, required: ['a', 'b'] } },
        ] });
      case 'tools/call': {
        const name = params.name; const args = params.arguments || {};
        if (name === 'echo') {
          return reply(res, rid, { content: [{ type: 'text', text: 'ECHO: ' + (args.msg || '') }] });
        }
        if (name === 'add') {
          const total = Number(args.a || 0) + Number(args.b || 0);
          return reply(res, rid, { content: [{ type: 'text', text: 'SUM: ' + total }] });
        }
        return error(res, rid, -32601, 'unknown tool');
      }
      case 'resources/list':
        return reply(res, rid, { resources: [
          { uri: 'data://hello', name: 'hello.txt', mimeType: 'text/plain', description: 'greeting' },
          { uri: 'data://logo', name: 'logo.png', mimeType: 'image/png' },
        ] });
      case 'resources/read': {
        const uri = params.uri || '';
        if (uri === 'data://hello') {
          return reply(res, rid, { contents: [{ uri, mimeType: 'text/plain', text: 'hello world' }] });
        }
        if (uri === 'data://logo') {
          const blob = Buffer.from('\x89PNG\r\n\x1a\n').toString('base64');
          return reply(res, rid, { contents: [{ uri, mimeType: 'image/png', blob }] });
        }
        return error(res, rid, -32602, 'unknown uri');
      }
      case 'prompts/list':
        return reply(res, rid, { prompts: [
          { name: 'summarise', description: 'Summarise' },
          { name: 'greet', description: 'Greet someone',
            arguments: [{ name: 'who', required: true }] },
        ] });
      case 'prompts/get': {
        const name = params.name; const args = params.arguments || {};
        if (name === 'summarise') {
          return reply(res, rid, { description: 'Summary', messages: [
            { role: 'user', content: { type: 'text', text: 'Please summarise' } },
          ] });
        }
        if (name === 'greet') {
          return reply(res, rid, { description: 'Greeting', messages: [
            { role: 'user', content: { type: 'text', text: 'Hello ' + (args.who || 'stranger') } },
          ] });
        }
        return error(res, rid, -32602, 'unknown prompt');
      }
      default:
        return error(res, rid, -32601, 'method not found: ' + method);
    }
  });
});

server.listen(0, '127.0.0.1', () => {
  process.stdout.write('PORT=' + server.address().port + '\n');
});
`

// startHTTPFixture writes `source` to a temp file, runs it via
// `interpreter`, and parses the "PORT=N" line from stdout. Returns
// (url, cleanup) — the cleanup is registered with t.Cleanup so a
// crashed subprocess doesn't leak between tests.
func startHTTPFixture(t *testing.T, interpreter, source, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(interpreter, path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr // route stderr to the test log for diagnosis
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Read the PORT= line. Server should print it within milliseconds
	// of binding; if we don't see it within 5s, something's wrong.
	portCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "PORT=") {
				portCh <- strings.TrimPrefix(line, "PORT=")
				// drain remaining stdout in background so the
				// pipe doesn't fill up
				go io.Copy(io.Discard, stdout)
				return
			}
		}
		errCh <- fmt.Errorf("fixture %s: stdout closed without PORT= line", name)
	}()
	select {
	case port := <-portCh:
		return "http://127.0.0.1:" + port + "/"
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatalf("fixture %s: timeout waiting for PORT= line", name)
	}
	return ""
}

// connectHTTPBoth spawns both Python and Node HTTP fixtures and
// connects them into one Registry. Skips when either interpreter
// is missing.
func connectHTTPBoth(t *testing.T, ctx context.Context) (*Registry, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows: skipping cross-language HTTP fixtures")
	}
	py := pythonAvailable()
	node := nodeAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	if node == "" {
		t.Skip("node not available")
	}
	pyURL := startHTTPFixture(t, py, pythonHTTPServerSource, "py-http-mcp.py")
	nodeURL := startHTTPFixture(t, node, nodeHTTPServerSource, "node-http-mcp.js")

	r := NewRegistry()
	t.Cleanup(func() { r.Close() })
	if err := r.ConnectHTTP(ctx, HTTPConfig{Name: "pyhttp", URL: pyURL}); err != nil {
		t.Fatalf("connect python http: %v", err)
	}
	if err := r.ConnectHTTP(ctx, HTTPConfig{Name: "nodehttp", URL: nodeURL}); err != nil {
		t.Fatalf("connect node http: %v", err)
	}
	return r, pyURL, nodeURL
}

// ─── Single-language smoke tests ──────────────────────

func TestCrossLangHTTPInitializePython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows")
	}
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	url := startHTTPFixture(t, py, pythonHTTPServerSource, "py-http-mcp.py")
	c := NewHTTP(HTTPConfig{Name: "py", URL: url})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if res.ServerInfo.Name != "py-http" {
		t.Errorf("server name: %q", res.ServerInfo.Name)
	}
	if res.Capabilities.Resources == nil || res.Capabilities.Prompts == nil {
		t.Errorf("missing capability bundle: %+v", res.Capabilities)
	}
}

func TestCrossLangHTTPInitializeNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows")
	}
	node := nodeAvailable()
	if node == "" {
		t.Skip("node not available")
	}
	url := startHTTPFixture(t, node, nodeHTTPServerSource, "node-http-mcp.js")
	c := NewHTTP(HTTPConfig{Name: "node", URL: url})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if res.ServerInfo.Name != "node-http" {
		t.Errorf("server name: %q", res.ServerInfo.Name)
	}
}

// ─── Dual-language registry tests ─────────────────────

func TestCrossLangHTTPToolCallEchoBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, _, _ := connectHTTPBoth(t, ctx)
	for _, server := range []string{"pyhttp", "nodehttp"} {
		res, err := r.Call(ctx, QualifyName(server, "echo"), map[string]any{"msg": "hello"})
		if err != nil {
			t.Fatalf("%s echo: %v", server, err)
		}
		if got := res.FlattenText(); got != "ECHO: hello" {
			t.Errorf("%s body: got %q want %q", server, got, "ECHO: hello")
		}
	}
}

func TestCrossLangHTTPResourcesFanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, _, _ := connectHTTPBoth(t, ctx)
	got, errs := r.ListResources(ctx, "")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 resources (2 per server), got %d", len(got))
	}
	// Both servers' Server label populates correctly through the
	// HTTP transport.
	seen := map[string]bool{}
	for _, e := range got {
		seen[e.Server] = true
	}
	if !seen["pyhttp"] || !seen["nodehttp"] {
		t.Errorf("server labels missing: %+v", got)
	}
}

func TestCrossLangHTTPReadResourceBlobBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, _, _ := connectHTTPBoth(t, ctx)
	for _, server := range []string{"pyhttp", "nodehttp"} {
		res, _, err := r.ReadResource(ctx, server, "data://logo")
		if err != nil {
			t.Fatalf("%s blob: %v", server, err)
		}
		if len(res.Contents) != 1 || res.Contents[0].Blob == "" {
			t.Errorf("%s blob payload empty: %+v", server, res.Contents)
		}
	}
}

func TestCrossLangHTTPGetPromptArgSubstitution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, _, _ := connectHTTPBoth(t, ctx)
	for _, server := range []string{"pyhttp", "nodehttp"} {
		res, _, err := r.GetPrompt(ctx, server, "greet", map[string]string{"who": "biu"})
		if err != nil {
			t.Fatalf("%s greet: %v", server, err)
		}
		if !strings.Contains(res.Messages[0].Content.Text, "Hello biu") {
			t.Errorf("%s body lost arg: %q", server, res.Messages[0].Content.Text)
		}
	}
}

// ─── Engine adapter routing ───────────────────────────

func TestCrossLangHTTPEngineAdapterRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, _, _ := connectHTTPBoth(t, ctx)
	engReg := engine.NewRegistry()
	r.RegisterEngineTools(engReg)
	for _, server := range []string{"pyhttp", "nodehttp"} {
		name := QualifyName(server, "echo")
		tool, ok := engReg.Get(name)
		if !ok {
			t.Fatalf("%s: engine tool not registered", name)
		}
		out, err := tool.Call(ctx, map[string]any{"msg": "hi"}, &engine.ToolEnv{})
		if err != nil {
			t.Fatalf("%s call: %v", name, err)
		}
		if out.IsError {
			t.Fatalf("%s flagged IsError: %+v", name, out)
		}
		var got string
		for _, b := range out.Content {
			got += b.Text
		}
		if !strings.Contains(got, "ECHO: hi") {
			t.Errorf("%s body %q missing 'ECHO: hi'", name, got)
		}
	}
}

// ─── Mixed transport: stdio + http together ───────────

// Connect a Python stdio server and a Node HTTP server in the same
// registry; verify tools from both are reachable through the same
// engine path. This is the realistic deployment shape — most users
// will have a mix of local subprocess MCPs and one or two hosted
// HTTP MCPs.
func TestCrossLangMixedStdioPlusHTTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows")
	}
	py := pythonAvailable()
	node := nodeAvailable()
	if py == "" || node == "" {
		t.Skip("need both python3 and node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// stdio side: reuse the cross-lang Python server.
	stdioPath := writeServer(t, pythonServerSource, "py-stdio-mcp.py")

	// http side: spawn the Node HTTP fixture.
	httpURL := startHTTPFixture(t, node, nodeHTTPServerSource, "node-http-mcp.js")

	r := NewRegistry()
	defer r.Close()
	if err := r.Connect(ctx, StdioConfig{
		Name: "local", Command: py, Args: []string{stdioPath},
	}); err != nil {
		t.Fatalf("stdio connect: %v", err)
	}
	if err := r.ConnectHTTP(ctx, HTTPConfig{
		Name: "remote", URL: httpURL,
	}); err != nil {
		t.Fatalf("http connect: %v", err)
	}

	// Both transports show up in Servers().
	transports := map[string]Transport{}
	for _, s := range r.Servers() {
		transports[s.Name] = s.Transport
	}
	if transports["local"] != TransportStdio {
		t.Errorf("local transport label wrong: %+v", transports)
	}
	if transports["remote"] != TransportHTTP {
		t.Errorf("remote transport label wrong: %+v", transports)
	}

	// Tool calls work through both.
	res, err := r.Call(ctx, QualifyName("local", "echo"), map[string]any{"msg": "via-stdio"})
	if err != nil {
		t.Errorf("stdio call: %v", err)
	} else if res.FlattenText() != "ECHO: via-stdio" {
		t.Errorf("stdio body: %q", res.FlattenText())
	}
	res, err = r.Call(ctx, QualifyName("remote", "echo"), map[string]any{"msg": "via-http"})
	if err != nil {
		t.Errorf("http call: %v", err)
	} else if res.FlattenText() != "ECHO: via-http" {
		t.Errorf("http body: %q", res.FlattenText())
	}
}
