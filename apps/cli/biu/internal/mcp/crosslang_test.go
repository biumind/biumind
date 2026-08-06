// Cross-language end-to-end tests for the MCP client + registry.
// Drives two real MCP servers — one Python, one Node — through the
// full stdio handshake and exercises every capability surface
// (tools, resources, prompts). The servers are embedded as source
// strings so the test is self-contained: no pip / npm install, no
// external test fixtures.
//
// Skip behaviour:
//
//   - Tests that need python3 skip when /usr/bin/env python3 is
//     missing (or fails its --version probe).
//   - Tests that need node skip the same way.
//   - On Windows everything skips because the embedded servers
//     assume POSIX line endings + /bin/sh-style shebangs.
//
// CI coverage matrix: macOS (where biu is primarily developed),
// Linux (production deployment), with both interpreters present.
// Failures here mean a real biu user with a Python or Node MCP
// server in their config will hit something this suite missed.
//
// Why two languages: regressions in the wire framing, cursor loop,
// or content-block decoding tend to be subtle in one runtime and
// loud in another (e.g. Python's json.dumps emits spaces, Node's
// JSON.stringify doesn't; Python flushes line-by-line by default
// when stdout is unbuffered, Node needs explicit \n + drain). Both
// must round-trip cleanly through the same StdioClient code path.

package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// Both servers expose this fixed capability surface so tests can
// assert the same shape against either runtime. The servers are
// kept intentionally small (one method dispatch per JSON-RPC verb)
// so a regression there is obviously a server bug, not a biu bug.
const (
	pyServerName   = "py"
	nodeServerName = "node"

	// `echo` returns its input back. `add` does integer arithmetic
	// on two arguments. Together they cover the two main wire
	// shapes: arbitrary string passthrough and parsed-from-JSON
	// numeric inputs. Both return a single text content block.
	wantToolEchoOutput = "ECHO: hello"
	wantToolAddOutput  = "SUM: 3"

	// `data://hello` is a text resource; `data://logo` is a tiny
	// binary returned as a base64 blob. Reading both verifies the
	// text/blob branches in ResourceContent.
	wantResourceText = "hello world"

	// `greet` substitutes a `who` argument; `summarise` ignores
	// arguments. Together they cover the two prompt shapes the spec
	// describes.
	wantPromptGreetText = "Hello biu"
)

// pythonServerSource is the full source of a stdio MCP server in
// Python 3. It implements just enough of the spec to service this
// test suite — handshake + the three capability methods + ping.
//
// Style notes for readers extending this in the future:
//
//   - Stdin is read line-by-line; every JSON-RPC message must fit
//     on one line. We don't support multi-line frames (the spec
//     doesn't either).
//   - Stdout is unbuffered (sys.stdout = ... with line_buffering)
//     so responses flush before stdin's next read; without this a
//     subtle deadlock appears when biu's StdioClient is waiting on
//     stdout while Python's buffer holds the reply.
//   - Errors that aren't method-not-found return a JSON-RPC error
//     envelope with code -32603 (internal). Method-not-found uses
//     -32601, the standard.
const pythonServerSource = `#!/usr/bin/env python3
import base64
import json
import sys

# Line buffering so each json.dumps + print flushes immediately —
# avoids deadlock with the parent reading stdout line-by-line.
sys.stdout.reconfigure(line_buffering=True)

def reply(msg_id, result=None, error=None):
    out = {"jsonrpc": "2.0", "id": msg_id}
    if error is not None:
        out["error"] = error
    else:
        out["result"] = result
    print(json.dumps(out))

def handle(req):
    method = req.get("method", "")
    params = req.get("params", {}) or {}
    msg_id = req.get("id")

    if method == "initialize":
        reply(msg_id, {
            "protocolVersion": "2024-11-05",
            "serverInfo": {"name": "py-fixture", "version": "0.1"},
            "capabilities": {"tools": {}, "resources": {}, "prompts": {}},
        })
    elif method == "notifications/initialized":
        # Notifications have no id; do not reply.
        pass
    elif method == "ping":
        reply(msg_id, {})
    elif method == "tools/list":
        reply(msg_id, {"tools": [
            {
                "name": "echo",
                "description": "Echo the input message",
                "inputSchema": {
                    "type": "object",
                    "properties": {"msg": {"type": "string"}},
                    "required": ["msg"],
                },
            },
            {
                "name": "add",
                "description": "Sum two integers",
                "inputSchema": {
                    "type": "object",
                    "properties": {
                        "a": {"type": "integer"},
                        "b": {"type": "integer"},
                    },
                    "required": ["a", "b"],
                },
            },
        ]})
    elif method == "tools/call":
        name = params.get("name")
        args = params.get("arguments", {}) or {}
        if name == "echo":
            reply(msg_id, {"content": [
                {"type": "text", "text": "ECHO: " + args.get("msg", "")},
            ]})
        elif name == "add":
            total = int(args.get("a", 0)) + int(args.get("b", 0))
            reply(msg_id, {"content": [
                {"type": "text", "text": "SUM: " + str(total)},
            ]})
        else:
            reply(msg_id, error={"code": -32601, "message": "unknown tool"})
    elif method == "resources/list":
        reply(msg_id, {"resources": [
            {"uri": "data://hello", "name": "hello.txt", "mimeType": "text/plain", "description": "greeting"},
            {"uri": "data://logo", "name": "logo.png", "mimeType": "image/png"},
        ]})
    elif method == "resources/read":
        uri = params.get("uri", "")
        if uri == "data://hello":
            reply(msg_id, {"contents": [
                {"uri": uri, "mimeType": "text/plain", "text": "hello world"},
            ]})
        elif uri == "data://logo":
            blob = base64.b64encode(b"\x89PNG\r\n\x1a\n").decode("ascii")
            reply(msg_id, {"contents": [
                {"uri": uri, "mimeType": "image/png", "blob": blob},
            ]})
        else:
            reply(msg_id, error={"code": -32602, "message": "unknown uri"})
    elif method == "prompts/list":
        reply(msg_id, {"prompts": [
            {"name": "summarise", "description": "Summarise the project"},
            {"name": "greet", "description": "Greet someone",
             "arguments": [{"name": "who", "required": True}]},
        ]})
    elif method == "prompts/get":
        name = params.get("name")
        args = params.get("arguments", {}) or {}
        if name == "summarise":
            reply(msg_id, {"description": "Summary prompt", "messages": [
                {"role": "user", "content": {"type": "text", "text": "Please summarise"}},
            ]})
        elif name == "greet":
            reply(msg_id, {"description": "Greeting", "messages": [
                {"role": "user", "content": {
                    "type": "text",
                    "text": "Hello " + args.get("who", "stranger"),
                }},
            ]})
        else:
            reply(msg_id, error={"code": -32602, "message": "unknown prompt"})
    else:
        if msg_id is not None:
            reply(msg_id, error={"code": -32601, "message": "method not found: " + method})

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError as e:
            # Malformed input: best we can do is report and keep
            # reading — closing stdout would terminate the parent.
            sys.stderr.write("py-fixture: bad json: " + str(e) + "\n")
            continue
        handle(req)

if __name__ == "__main__":
    main()
`

// nodeServerSource is the same MCP surface as pythonServerSource,
// but in plain Node (no npm dependencies). Uses readline for line
// framing — Node's default stdin handling buffers across newlines
// in ways that make a hand-rolled parser unreliable.
//
// Differences worth noting versus the Python version:
//
//   - JSON.stringify emits compact output by default (no spaces);
//     server-side that's a non-issue, but biu's parser must handle
//     either.
//   - process.stdout.write does not append \n; we add it explicitly
//     so each frame is a single line on the wire.
//   - Errors thrown inside handlers surface as -32603 (internal)
//     rather than crashing the loop — keeps the test deterministic
//     when a future test adds a bad request.
const nodeServerSource = `#!/usr/bin/env node
const readline = require('readline');

const rl = readline.createInterface({ input: process.stdin });

function reply(id, result, error) {
  const out = { jsonrpc: '2.0', id };
  if (error !== undefined) out.error = error;
  else out.result = result;
  process.stdout.write(JSON.stringify(out) + '\n');
}

function handle(req) {
  const method = req.method || '';
  const params = req.params || {};
  const id = req.id;

  switch (method) {
    case 'initialize':
      return reply(id, {
        protocolVersion: '2024-11-05',
        serverInfo: { name: 'node-fixture', version: '0.1' },
        capabilities: { tools: {}, resources: {}, prompts: {} },
      });
    case 'notifications/initialized':
      return;
    case 'ping':
      return reply(id, {});
    case 'tools/list':
      return reply(id, { tools: [
        {
          name: 'echo', description: 'Echo the input message',
          inputSchema: { type: 'object', properties: { msg: { type: 'string' } }, required: ['msg'] },
        },
        {
          name: 'add', description: 'Sum two integers',
          inputSchema: { type: 'object', properties: { a: { type: 'integer' }, b: { type: 'integer' } }, required: ['a', 'b'] },
        },
      ] });
    case 'tools/call': {
      const name = params.name;
      const args = params.arguments || {};
      if (name === 'echo') {
        return reply(id, { content: [{ type: 'text', text: 'ECHO: ' + (args.msg || '') }] });
      }
      if (name === 'add') {
        const total = Number(args.a || 0) + Number(args.b || 0);
        return reply(id, { content: [{ type: 'text', text: 'SUM: ' + total }] });
      }
      return reply(id, undefined, { code: -32601, message: 'unknown tool' });
    }
    case 'resources/list':
      return reply(id, { resources: [
        { uri: 'data://hello', name: 'hello.txt', mimeType: 'text/plain', description: 'greeting' },
        { uri: 'data://logo', name: 'logo.png', mimeType: 'image/png' },
      ] });
    case 'resources/read': {
      const uri = params.uri || '';
      if (uri === 'data://hello') {
        return reply(id, { contents: [{ uri, mimeType: 'text/plain', text: 'hello world' }] });
      }
      if (uri === 'data://logo') {
        const blob = Buffer.from('\x89PNG\r\n\x1a\n').toString('base64');
        return reply(id, { contents: [{ uri, mimeType: 'image/png', blob }] });
      }
      return reply(id, undefined, { code: -32602, message: 'unknown uri' });
    }
    case 'prompts/list':
      return reply(id, { prompts: [
        { name: 'summarise', description: 'Summarise the project' },
        { name: 'greet', description: 'Greet someone',
          arguments: [{ name: 'who', required: true }] },
      ] });
    case 'prompts/get': {
      const name = params.name;
      const args = params.arguments || {};
      if (name === 'summarise') {
        return reply(id, { description: 'Summary prompt', messages: [
          { role: 'user', content: { type: 'text', text: 'Please summarise' } },
        ] });
      }
      if (name === 'greet') {
        return reply(id, { description: 'Greeting', messages: [
          { role: 'user', content: { type: 'text', text: 'Hello ' + (args.who || 'stranger') } },
        ] });
      }
      return reply(id, undefined, { code: -32602, message: 'unknown prompt' });
    }
    default:
      if (id !== undefined && id !== null) {
        return reply(id, undefined, { code: -32601, message: 'method not found: ' + method });
      }
  }
}

rl.on('line', (line) => {
  const trimmed = line.trim();
  if (!trimmed) return;
  let req;
  try { req = JSON.parse(trimmed); }
  catch (e) {
    process.stderr.write('node-fixture: bad json: ' + e.message + '\n');
    return;
  }
  try { handle(req); }
  catch (e) {
    if (req && req.id !== undefined && req.id !== null) {
      reply(req.id, undefined, { code: -32603, message: 'handler crashed: ' + e.message });
    }
  }
});
`

// limitedServerSource is a minimal MCP server that ONLY supports
// tools — no resources, no prompts. Used to verify that biu's
// resource-tool / prompt-tool gating + capability negotiation
// surface clean errors instead of hanging or crashing when a server
// doesn't support the methods the model invokes.
const limitedServerSource = `#!/usr/bin/env python3
import json
import sys
sys.stdout.reconfigure(line_buffering=True)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    m = req.get("method", "")
    rid = req.get("id")
    if m == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":"2024-11-05",
            "serverInfo":{"name":"limited","version":"0.1"},
            "capabilities":{"tools":{}},  # no resources / prompts
        }}))
    elif m == "notifications/initialized":
        pass
    elif m == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"tools":[
            {"name":"only","description":"only tool",
             "inputSchema":{"type":"object","properties":{}}},
        ]}}))
    elif m == "tools/call":
        # Tools cap is on, so we MUST honour calls. Return a fixed
        # text content block — the test only cares that the call
        # round-trips, not what it returns.
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "content":[{"type":"text","text":"only-result"}],
        }}))
    elif rid is not None:
        # Method not found — every other verb gets this. Lets us
        # verify biu surfaces the JSON-RPC error to the caller as
        # a Go error rather than hanging forever waiting for a
        # response.
        print(json.dumps({"jsonrpc":"2.0","id":rid,"error":{
            "code": -32601, "message": "method not found: " + m,
        }}))
`

// pythonAvailable returns the path to a working python3 binary or
// "" when none is reachable. Centralised so every cross-lang test
// uses the same skip predicate.
func pythonAvailable() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	bin, err := exec.LookPath("python3")
	if err != nil {
		return ""
	}
	if err := exec.Command(bin, "--version").Run(); err != nil {
		return ""
	}
	return bin
}

// nodeAvailable mirrors pythonAvailable for Node.
func nodeAvailable() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	bin, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	if err := exec.Command(bin, "--version").Run(); err != nil {
		return ""
	}
	return bin
}

// writeServer drops a server source string to a temp file, marks it
// executable, and returns the path. The shebang on the first line
// of each source string lets us exec it directly via interpreter +
// path; we ALSO mark it +x so a future change that switches to
// `path/to/file` invocation still works.
func writeServer(t *testing.T, source, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// connectAll bootstraps both Python and Node servers in one
// registry, returns the registry. Skips the test when either
// interpreter is missing — the dual-server tests need both.
func connectAll(t *testing.T, ctx context.Context) *Registry {
	t.Helper()
	py := pythonAvailable()
	node := nodeAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	if node == "" {
		t.Skip("node not available")
	}
	r := NewRegistry()
	t.Cleanup(func() { r.Close() })

	if err := r.Connect(ctx, StdioConfig{
		Name: pyServerName, Command: py,
		Args: []string{writeServer(t, pythonServerSource, "py-mcp.py")},
	}); err != nil {
		t.Fatalf("connect python: %v", err)
	}
	if err := r.Connect(ctx, StdioConfig{
		Name: nodeServerName, Command: node,
		Args: []string{writeServer(t, nodeServerSource, "node-mcp.js")},
	}); err != nil {
		t.Fatalf("connect node: %v", err)
	}
	return r
}

// ─── Handshake + capabilities ─────────────────────────

func TestCrossLangHandshakePython(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	c := NewStdio(StdioConfig{
		Name: pyServerName, Command: py,
		Args: []string{writeServer(t, pythonServerSource, "py-mcp.py")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "py-fixture" {
		t.Errorf("server info: %+v", res.ServerInfo)
	}
	// All three caps advertised — biu's RegisterEngineTools relies
	// on the tools cap to decide whether to wrap them.
	if res.Capabilities.Tools == nil ||
		res.Capabilities.Resources == nil ||
		res.Capabilities.Prompts == nil {
		t.Errorf("missing capability bundle: %+v", res.Capabilities)
	}
}

func TestCrossLangHandshakeNode(t *testing.T) {
	node := nodeAvailable()
	if node == "" {
		t.Skip("node not available")
	}
	c := NewStdio(StdioConfig{
		Name: nodeServerName, Command: node,
		Args: []string{writeServer(t, nodeServerSource, "node-mcp.js")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "node-fixture" {
		t.Errorf("server info: %+v", res.ServerInfo)
	}
}

// ─── Tools ────────────────────────────────────────────

// Both servers expose `echo` + `add`. The registry namespaces by
// server, so the engine catalog ends up with four namespaced names.
// This is the contract the model relies on: same logical tool in
// two servers ⇒ two callable engine.Tools, no collisions.
func TestCrossLangToolsRegisteredUnderBothServers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	wanted := map[string]bool{
		QualifyName(pyServerName, "echo"):   true,
		QualifyName(pyServerName, "add"):    true,
		QualifyName(nodeServerName, "echo"): true,
		QualifyName(nodeServerName, "add"):  true,
	}
	for _, tool := range r.All() {
		delete(wanted, tool.QualifiedName)
	}
	if len(wanted) != 0 {
		t.Errorf("missing tool registrations: %v", wanted)
	}
}

// Round-trip a call through each server. Both must return the same
// canned text — verifies the wire format and content-block decoding
// are language-agnostic.
func TestCrossLangToolCallEchoBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	for _, server := range []string{pyServerName, nodeServerName} {
		res, err := r.Call(ctx, QualifyName(server, "echo"), map[string]any{"msg": "hello"})
		if err != nil {
			t.Fatalf("%s echo: %v", server, err)
		}
		if got := res.FlattenText(); got != wantToolEchoOutput {
			t.Errorf("%s echo: got %q, want %q", server, got, wantToolEchoOutput)
		}
	}
}

// `add` exercises the integer-arg path — JSON encoders in Python
// (json.dumps) and Node (JSON.stringify) emit numbers slightly
// differently (Python often uses int repr, Node uses Number); the
// test confirms biu's parser doesn't choke on either.
func TestCrossLangToolCallAddBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	for _, server := range []string{pyServerName, nodeServerName} {
		res, err := r.Call(ctx, QualifyName(server, "add"), map[string]any{"a": 1, "b": 2})
		if err != nil {
			t.Fatalf("%s add: %v", server, err)
		}
		if got := res.FlattenText(); got != wantToolAddOutput {
			t.Errorf("%s add: got %q, want %q", server, got, wantToolAddOutput)
		}
	}
}

// ─── Resources ────────────────────────────────────────

func TestCrossLangResourcesFanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	got, errs := r.ListResources(ctx, "")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// 2 resources × 2 servers
	if len(got) != 4 {
		t.Fatalf("expected 4 entries (2 per server); got %d (%+v)", len(got), got)
	}
	// Every entry must carry a non-empty Server label so the model
	// can disambiguate.
	for _, e := range got {
		if e.Server != pyServerName && e.Server != nodeServerName {
			t.Errorf("unexpected server label: %+v", e)
		}
	}
}

// Server-scoped fetch returns the right server's contents. We test
// both servers because regressions in client.ReadResource often
// only show up in one direction (e.g. Node's compact JSON sneaks
// past a parser that worked against Python's pretty output).
func TestCrossLangReadResourceTextBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	for _, server := range []string{pyServerName, nodeServerName} {
		res, fromServer, err := r.ReadResource(ctx, server, "data://hello")
		if err != nil {
			t.Fatalf("%s read text: %v", server, err)
		}
		if fromServer != server {
			t.Errorf("server label mismatch: %s vs %s", fromServer, server)
		}
		if len(res.Contents) != 1 {
			t.Fatalf("%s expected 1 part, got %d", server, len(res.Contents))
		}
		if res.Contents[0].Text != wantResourceText {
			t.Errorf("%s body: got %q want %q", server, res.Contents[0].Text, wantResourceText)
		}
		if res.Contents[0].MimeType != "text/plain" {
			t.Errorf("%s mime: %q", server, res.Contents[0].MimeType)
		}
	}
}

// Blob branch: the resource decodes as base64. Both servers emit
// the same fixed PNG header — biu doesn't decode the blob, just
// ferries the base64 string through. Test asserts the field is set
// and not empty (verifying the blob/text branches of ResourceContent
// don't collide).
func TestCrossLangReadResourceBlobBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	for _, server := range []string{pyServerName, nodeServerName} {
		res, _, err := r.ReadResource(ctx, server, "data://logo")
		if err != nil {
			t.Fatalf("%s read blob: %v", server, err)
		}
		if len(res.Contents) != 1 || res.Contents[0].Blob == "" {
			t.Errorf("%s blob: %+v", server, res.Contents)
		}
		if res.Contents[0].Text != "" {
			t.Errorf("%s blob entry should not also carry text: %+v", server, res.Contents[0])
		}
	}
}

// Scan mode (server="") finds the URI on whichever server has it.
// With both servers exposing the same URI we just need to confirm
// SOMEONE answered — the contract is "first hit wins".
func TestCrossLangReadResourceScanWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	res, fromServer, err := r.ReadResource(ctx, "", "data://hello")
	if err != nil {
		t.Fatalf("scan read: %v", err)
	}
	if fromServer != pyServerName && fromServer != nodeServerName {
		t.Errorf("unexpected server label: %q", fromServer)
	}
	if res.Contents[0].Text != wantResourceText {
		t.Errorf("body: %q", res.Contents[0].Text)
	}
}

// ─── Prompts ──────────────────────────────────────────

func TestCrossLangPromptsFanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	got, errs := r.ListPrompts(ctx, "")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 prompts (2 per server); got %d", len(got))
	}
	// `greet` carries a required argument — verify the schema flows
	// through both runtimes.
	for _, e := range got {
		if e.Name != "greet" {
			continue
		}
		if len(e.Arguments) != 1 || e.Arguments[0].Name != "who" || !e.Arguments[0].Required {
			t.Errorf("%s greet args wrong: %+v", e.Server, e.Arguments)
		}
	}
}

// Argument substitution — the server inserts the `who` value into
// the rendered text. Both runtimes must produce the same output.
func TestCrossLangGetPromptArgSubstitutionBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	for _, server := range []string{pyServerName, nodeServerName} {
		res, fromServer, err := r.GetPrompt(ctx, server, "greet", map[string]string{"who": "biu"})
		if err != nil {
			t.Fatalf("%s get: %v", server, err)
		}
		if fromServer != server {
			t.Errorf("server label: %s vs %s", fromServer, server)
		}
		if len(res.Messages) != 1 {
			t.Fatalf("%s expected 1 message, got %d", server, len(res.Messages))
		}
		if res.Messages[0].Role != "user" {
			t.Errorf("%s role: %q", server, res.Messages[0].Role)
		}
		if !strings.Contains(res.Messages[0].Content.Text, wantPromptGreetText) {
			t.Errorf("%s body missing %q: %q", server, wantPromptGreetText, res.Messages[0].Content.Text)
		}
	}
}

// ─── Engine adapter integration ───────────────────────

// The full pipeline: registry → RegisterEngineTools → engine
// catalog → Tool.Call. This is the path the agent loop exercises;
// regressing here breaks every model-driven MCP call. We assert on
// both servers' `echo` to confirm that the namespaced engine name
// routes back through to the right StdioClient.
func TestCrossLangEngineAdapterRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	engReg := engine.NewRegistry()
	r.RegisterEngineTools(engReg)

	for _, server := range []string{pyServerName, nodeServerName} {
		name := QualifyName(server, "echo")
		tool, ok := engReg.Get(name)
		if !ok {
			t.Fatalf("%s: engine tool not registered", name)
		}
		out, err := tool.Call(ctx, map[string]any{"msg": "hello"}, &engine.ToolEnv{})
		if err != nil {
			t.Fatalf("%s call: %v", name, err)
		}
		if out.IsError {
			t.Fatalf("%s flagged IsError: %+v", name, out)
		}
		// Concatenate every text block — the engine adapter merges
		// MCP content blocks into ContentText entries.
		var got string
		for _, b := range out.Content {
			got += b.Text
		}
		if !strings.Contains(got, wantToolEchoOutput) {
			t.Errorf("%s body %q missing %q", name, got, wantToolEchoOutput)
		}
	}
}

// Resource + prompt tools are also installed once a server is
// connected. Drive both via the engine surface so we know the
// gating in RegisterEngineTools fires after a real, language-agnostic
// connection (not just an in-memory fixture).
func TestCrossLangResourceAndPromptToolsRegistered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	engReg := engine.NewRegistry()
	r.RegisterEngineTools(engReg)
	for _, want := range []string{
		"ListMcpResources", "ReadMcpResource",
		"ListMcpPrompts", "GetMcpPrompt",
	} {
		if _, ok := engReg.Get(want); !ok {
			t.Errorf("%s not registered after cross-lang bootstrap", want)
		}
	}
}

// ─── Capability negotiation (degraded server) ──────────

// A server that ONLY advertises tools must reject resources/list
// with -32601 (method not found). biu's Registry surfaces that as
// an error in the errs slice rather than swallowing it — confirms
// the partial-failure path actually fires when one server in a
// fleet is degraded.
func TestCrossLangLimitedServerSurfacesErrors(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "limited", Command: py,
		Args: []string{writeServer(t, limitedServerSource, "limited-mcp.py")},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Tools work — the limited server has them.
	if _, err := r.Call(ctx, QualifyName("limited", "only"), nil); err != nil {
		t.Errorf("only-tool call: %v", err)
	}
	// Resources are NOT supported. ListResources must return an
	// error in the errs slice (not swallow it silently). Without
	// this, a degraded server would silently disappear from the
	// model's view rather than surfacing as a recoverable
	// soft-error.
	_, errs := r.ListResources(ctx, "")
	if len(errs) == 0 {
		t.Errorf("limited server must surface resources/list error; got none")
	}
	// Same for prompts.
	_, errs = r.ListPrompts(ctx, "")
	if len(errs) == 0 {
		t.Errorf("limited server must surface prompts/list error; got none")
	}
}

// ─── Failure modes ────────────────────────────────────

// Sub-process death: kill the server by closing the registry
// (which sends EOF to stdin → server exits). A subsequent call
// returns a clean error, no hang. Catches any deadlock in the
// pending-request map when a server vanishes mid-flight.
func TestCrossLangServerDeathSurfacesError(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	r := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: pyServerName, Command: py,
		Args: []string{writeServer(t, pythonServerSource, "py-mcp.py")},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Sanity: one good call before we shut it down.
	if _, err := r.Call(ctx, QualifyName(pyServerName, "echo"), map[string]any{"msg": "hi"}); err != nil {
		t.Fatalf("pre-kill echo: %v", err)
	}

	// Close drops every connection; subsequent calls must fail
	// fast rather than hang on the now-dead reader goroutine.
	r.Close()

	deadline := time.After(3 * time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := r.Call(ctx, QualifyName(pyServerName, "echo"), map[string]any{"msg": "hi"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("expected error from dead server; got nil")
		}
	case <-deadline:
		t.Fatal("call hung after server close — pending-request map didn't drain")
	}
}

// Concurrent calls to the same server must serialise correctly on
// the JSON-RPC ID. Without per-id pending channels we'd see
// reply-shuffling. Drives 8 concurrent `add` calls against the
// Python server and confirms each gets the right answer.
func TestCrossLangConcurrentCallsSerialiseCorrectly(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: pyServerName, Command: py,
		Args: []string{writeServer(t, pythonServerSource, "py-mcp.py")},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// 8 distinct (a, b) → expected_sum tuples. Distinct sums make
	// shuffling immediately visible in the assertion.
	tuples := []struct {
		a, b, want int
	}{
		{1, 2, 3}, {10, 20, 30}, {7, 8, 15}, {100, 1, 101},
		{0, 0, 0}, {99, 99, 198}, {5, 5, 10}, {12, 34, 46},
	}
	type res struct {
		idx int
		got string
		err error
	}
	results := make(chan res, len(tuples))
	for i, tup := range tuples {
		i, tup := i, tup
		go func() {
			out, err := r.Call(ctx, QualifyName(pyServerName, "add"),
				map[string]any{"a": tup.a, "b": tup.b})
			if err != nil {
				results <- res{idx: i, err: err}
				return
			}
			results <- res{idx: i, got: out.FlattenText()}
		}()
	}
	for i := 0; i < len(tuples); i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("tuple %d: %v", r.idx, r.err)
			continue
		}
		tup := tuples[r.idx]
		want := "SUM: " + itoa(tup.a+tup.b)
		if r.got != want {
			// Reply landed in the wrong slot — the per-id pending
			// channel either dropped a frame or routed it to a
			// stale waiter.
			t.Errorf("tuple %d (%d+%d): got %q, want %q",
				r.idx, tup.a, tup.b, r.got, want)
		}
	}
}

// ─── Wire-format edge cases ───────────────────────────

// Unicode round-trip: send a non-ASCII string through `echo` and
// confirm both runtimes preserve it byte-for-byte. Catches double-
// encoding bugs (UTF-8 → escape → UTF-16 → re-encode) that some
// JSON libraries silently introduce.
func TestCrossLangUnicodeRoundTripBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := connectAll(t, ctx)

	// Cyrillic + CJK + emoji = three different codepoint planes.
	const probe = "Привет 你好 👋"
	for _, server := range []string{pyServerName, nodeServerName} {
		res, err := r.Call(ctx, QualifyName(server, "echo"),
			map[string]any{"msg": probe})
		if err != nil {
			t.Fatalf("%s echo unicode: %v", server, err)
		}
		got := res.FlattenText()
		if !strings.Contains(got, probe) {
			t.Errorf("%s lost unicode: got %q want substring %q", server, got, probe)
		}
	}
}

// noisyServerSource writes log lines to stderr while serving every
// request. Confirms biu's reader goroutine treats stderr as logs
// (drops or routes to logger) rather than trying to parse it as
// JSON-RPC frames — a parser bug here would surface as garbage
// "frame" decoding errors.
const noisyServerSource = `#!/usr/bin/env python3
import json, sys
sys.stdout.reconfigure(line_buffering=True)
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    sys.stderr.write("noisy: got request\n")
    sys.stderr.flush()
    req = json.loads(line)
    rid = req.get("id")
    m = req.get("method", "")
    if m == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":"2024-11-05",
            "serverInfo":{"name":"noisy","version":"0.1"},
            "capabilities":{"tools":{}},
        }}))
    elif m == "notifications/initialized":
        sys.stderr.write("noisy: initialised\n")
        sys.stderr.flush()
    elif m == "tools/list":
        sys.stderr.write("noisy: listing tools\n")
        sys.stderr.flush()
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"tools":[
            {"name":"ping","description":"trivial","inputSchema":{"type":"object","properties":{}}},
        ]}}))
    elif m == "tools/call":
        sys.stderr.write("noisy: calling tool\n")
        sys.stderr.flush()
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "content":[{"type":"text","text":"pong"}],
        }}))
    elif rid is not None:
        print(json.dumps({"jsonrpc":"2.0","id":rid,"error":{
            "code":-32601,"message":"method not found"}}))
`

func TestCrossLangStderrNoiseIgnored(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "noisy", Command: py,
		Args: []string{writeServer(t, noisyServerSource, "noisy-mcp.py")},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	// One call must round-trip cleanly despite stderr writes
	// happening on every line.
	res, err := r.Call(ctx, QualifyName("noisy", "ping"), nil)
	if err != nil {
		t.Fatalf("noisy call: %v", err)
	}
	if got := res.FlattenText(); got != "pong" {
		t.Errorf("noisy result wrong: %q", got)
	}
}

// paginatingServerSource emits two pages of tools. First call has
// nextCursor="page2"; second call carries cursor=page2 and emits
// the rest with no cursor. Verifies biu's StdioClient.ListTools
// (and by extension ListResources / ListPrompts) follows the
// cursor loop instead of stopping at the first page.
const paginatingServerSource = `#!/usr/bin/env python3
import json, sys
sys.stdout.reconfigure(line_buffering=True)

PAGE1 = [
    {"name":"alpha","description":"page-1 tool","inputSchema":{"type":"object","properties":{}}},
    {"name":"beta", "description":"page-1 tool","inputSchema":{"type":"object","properties":{}}},
]
PAGE2 = [
    {"name":"gamma","description":"page-2 tool","inputSchema":{"type":"object","properties":{}}},
    {"name":"delta","description":"page-2 tool","inputSchema":{"type":"object","properties":{}}},
]

for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    m = req.get("method", "")
    rid = req.get("id")
    if m == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":"2024-11-05",
            "serverInfo":{"name":"pager","version":"0.1"},
            "capabilities":{"tools":{}},
        }}))
    elif m == "notifications/initialized":
        pass
    elif m == "tools/list":
        cursor = (req.get("params") or {}).get("cursor", "")
        if cursor == "":
            print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
                "tools": PAGE1, "nextCursor": "page2",
            }}))
        else:
            print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
                "tools": PAGE2,
            }}))
    elif rid is not None:
        print(json.dumps({"jsonrpc":"2.0","id":rid,"error":{
            "code":-32601,"message":"method not found"}}))
`

func TestCrossLangPaginatedToolListAggregates(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	c := NewStdio(StdioConfig{
		Name: "pager", Command: py,
		Args: []string{writeServer(t, paginatingServerSource, "pager-mcp.py")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// 2 pages × 2 entries = 4 — biu must follow nextCursor.
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 4 {
		t.Fatalf("expected 4 paged tools, got %d (%+v)", len(tools), tools)
	}
	// Order must be first-page-then-second so the registry sees a
	// stable enumeration. Asserting the names confirms cursor
	// follow-through, not just count.
	wantOrder := []string{"alpha", "beta", "gamma", "delta"}
	for i, w := range wantOrder {
		if tools[i].Name != w {
			t.Errorf("position %d: got %q want %q", i, tools[i].Name, w)
		}
	}
}

// largePayloadServerSource returns a tool result with ~64KB of
// text. Forces biu's stdio scanner past the typical 4KB default
// and into the explicit MaxScanTokenSize budget. A regression in
// the scanner buffer config would surface as a "bufio.Scanner:
// token too long" failure here.
const largePayloadServerSource = `#!/usr/bin/env python3
import json, sys
sys.stdout.reconfigure(line_buffering=True)
BIG = "x" * 65000  # well above bufio's 4KB default scan buffer
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    rid = req.get("id"); m = req.get("method","")
    if m == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":"2024-11-05",
            "serverInfo":{"name":"big","version":"0.1"},
            "capabilities":{"tools":{}},
        }}))
    elif m == "notifications/initialized":
        pass
    elif m == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"tools":[
            {"name":"big","description":"large output","inputSchema":{"type":"object","properties":{}}},
        ]}}))
    elif m == "tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{
            "content":[{"type":"text","text":BIG}],
        }}))
    elif rid is not None:
        print(json.dumps({"jsonrpc":"2.0","id":rid,"error":{
            "code":-32601,"message":"method not found"}}))
`

func TestCrossLangLargePayloadRoundTrips(t *testing.T) {
	py := pythonAvailable()
	if py == "" {
		t.Skip("python3 not available")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "big", Command: py,
		Args: []string{writeServer(t, largePayloadServerSource, "big-mcp.py")},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := r.Call(ctx, QualifyName("big", "big"), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := res.FlattenText()
	// A scanner-buffer regression usually yields either an error
	// (handled above) or a truncated payload — assert on the full
	// 65000-char body so silent truncation also fails the test.
	if len(got) != 65000 {
		t.Errorf("payload truncated: got %d chars, want 65000", len(got))
	}
}

// itoa avoids importing strconv just to format eight integers.
// Inlined because the parent test is the only caller.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
