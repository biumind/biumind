package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fake MCP server for testing — a tiny shell script that:
//   1. Reads NDJSON requests on stdin
//   2. Replies with hardcoded responses by method name
//
// Lets us exercise the full handshake → list → call path without a
// real npm install. Skipped on Windows (no /bin/sh).

const fakeServer = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"fake","version":"0.1"},"capabilities":{"tools":{}}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"tools":[{"name":"echo","description":"echoes its input","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]}}'
      ;;
    *'"method":"tools/call"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      msg=$(echo "$line" | sed -n 's/.*"msg":"\([^"]*\)".*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"content":[{"type":"text","text":"echoed: '"$msg"'"}]}}'
      ;;
  esac
done
`

func writeFakeServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mcp.sh")
	if err := os.WriteFile(path, []byte(fakeServer), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStdioHandshake(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeFakeServer(t)
	c := NewStdio(StdioConfig{
		Name: "fake", Command: "/bin/sh", Args: []string{path},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "fake" {
		t.Errorf("name: %+v", res)
	}
}

func TestStdioListAndCall(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeFakeServer(t)
	c := NewStdio(StdioConfig{
		Name: "fake", Command: "/bin/sh", Args: []string{path},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("tools: %+v", tools)
	}
	call, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := call.FlattenText()
	if got != "echoed: hi" {
		t.Errorf("call result: %q", got)
	}
}

func TestRegistryConnectAndCall(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeFakeServer(t)
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := r.Connect(ctx, StdioConfig{
		Name: "fake", Command: "/bin/sh", Args: []string{path},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	all := r.All()
	if len(all) != 1 || all[0].QualifiedName != "mcp__fake__echo" {
		t.Errorf("registry: %+v", all)
	}
	res, err := r.Call(ctx, "mcp__fake__echo", map[string]any{"msg": "yo"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.FlattenText() != "echoed: yo" {
		t.Errorf("got %q", res.FlattenText())
	}
}

// dyingServer replies to initialize OK then closes stdout, so any
// subsequent call sees "stream closed".
const dyingServer = `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"dyer","version":"0.1"},"capabilities":{}}}'
read line  # eat the initialized notification
exit 0
`

func writeDyingServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dying.sh")
	if err := os.WriteFile(path, []byte(dyingServer), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCircuitBreakerTrips(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeDyingServer(t)
	c := NewStdio(StdioConfig{
		Name: "dyer", Command: "/bin/sh", Args: []string{path},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Server died after handshake — every subsequent call fails. After
	// MaxConsecutiveErrors, IsHealthy returns false.
	for i := 0; i < MaxConsecutiveErrors; i++ {
		_, _ = c.ListTools(ctx)
	}
	if c.IsHealthy() {
		t.Errorf("circuit breaker should have tripped after %d errors",
			MaxConsecutiveErrors)
	}
}

func TestRegistryRejectsBadName(t *testing.T) {
	r := NewRegistry()
	defer r.Close()
	err := r.Connect(context.Background(), StdioConfig{
		Name: "Bad Name", Command: "/bin/true",
	})
	if err == nil {
		t.Errorf("expected validation error")
	}
}
